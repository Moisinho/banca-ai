package http

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Moisinho/banca-ai/apps/api/internal/adapters/openrouter"
	"github.com/Moisinho/banca-ai/apps/api/internal/chat"
	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/http/middleware"
	"github.com/Moisinho/banca-ai/apps/api/internal/http/response"
	"github.com/Moisinho/banca-ai/apps/api/internal/logger"
)

// maxMessageLength acota el tamaño de un mensaje del usuario.
//
// Un texto enorme desperdicia cuota del modelo y no aporta: las consultas
// bancarias son cortas por naturaleza.
const maxMessageLength = 2000

// ChatHandler expone los endpoints del asistente.
type ChatHandler struct {
	service *chat.Service
	log     *slog.Logger
	// enabled es false si no hay API key configurada. En ese caso el chat
	// responde 503 y el resto de la aplicación sigue funcionando.
	enabled bool
}

func NewChatHandler(service *chat.Service, log *slog.Logger, enabled bool) *ChatHandler {
	return &ChatHandler{service: service, log: log, enabled: enabled}
}

func (h *ChatHandler) Routes(r chi.Router) {
	r.Post("/messages", h.sendMessage)
	r.Get("/messages", h.history)
	r.Post("/operations/{transferID}/confirm", h.confirm)
	r.Post("/operations/{transferID}/reject", h.reject)
}

type sendMessageRequest struct {
	Message string `json:"message"`
}

type chatMessageResponse struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt"`

	PendingOperation *pendingOperationResponse `json:"pendingOperation,omitempty"`
}

type pendingOperationResponse struct {
	TransferID  string `json:"transferId"`
	Operation   string `json:"operation"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	FromAccount string `json:"fromAccount"`
	ToAccount   string `json:"toAccount"`
	Description string `json:"description"`
	Status      string `json:"status"`
	ExpiresAt   string `json:"expiresAt"`
}

func toPendingOperationResponse(p *domain.PendingOperation) *pendingOperationResponse {
	if p == nil {
		return nil
	}

	return &pendingOperationResponse{
		TransferID:  p.TransferID.String(),
		Operation:   string(p.Operation),
		Amount:      p.Amount.String(),
		Currency:    p.Currency,
		FromAccount: p.FromAccount,
		ToAccount:   p.ToAccount,
		Description: p.Description,
		Status:      string(p.Status),
		ExpiresAt:   p.ExpiresAt.Format(time.RFC3339),
	}
}

func (h *ChatHandler) sendMessage(w http.ResponseWriter, r *http.Request) {
	if !h.enabled {
		response.Error(w, r, http.StatusServiceUnavailable,
			"AI_DISABLED", "El asistente no está disponible. Falta configurar la clave de OpenRouter.")
		return
	}

	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesita iniciar sesión")
		return
	}

	var req sendMessageRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	if len([]rune(req.Message)) > maxMessageLength {
		response.Error(w, r, http.StatusUnprocessableEntity,
			"MESSAGE_TOO_LONG", "El mensaje es demasiado largo")
		return
	}

	reply, err := h.service.Send(r.Context(), userID, req.Message)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	response.JSON(w, r, http.StatusOK, chatMessageResponse{
		ID:               reply.Message.ID,
		Role:             string(reply.Message.Role),
		Content:          reply.Message.Content,
		CreatedAt:        reply.Message.CreatedAt.Format(time.RFC3339),
		PendingOperation: toPendingOperationResponse(reply.PendingOperation),
	})
}

func (h *ChatHandler) history(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesita iniciar sesión")
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	var (
		messages []domain.ChatMessage
		hasMore  bool
		err      error
	)

	// Con `before` se piden los mensajes anteriores a uno concreto: es como la
	// interfaz carga el tramo siguiente cuando la persona sube en el hilo.
	if before := r.URL.Query().Get("before"); before != "" {
		messages, hasMore, err = h.service.HistoryBefore(r.Context(), userID, before, limit)
	} else {
		messages, err = h.service.History(r.Context(), userID, limit)
		// En la carga inicial se traen los más recientes. Si llegó una página
		// completa es probable que haya más hacia atrás; la petición siguiente
		// lo confirma.
		hasMore = len(messages) == limit
	}

	if err != nil {
		h.writeError(w, r, err)
		return
	}

	items := make([]chatMessageResponse, 0, len(messages))
	for _, m := range messages {
		item := chatMessageResponse{
			ID:        m.ID,
			Role:      string(m.Role),
			Content:   m.Content,
			CreatedAt: m.CreatedAt.Format(time.RFC3339),
		}

		// Se reconstruye lo suficiente de la operación para que el frontend
		// pueda mostrar su estado al recargar la conversación.
		if m.PendingTransferID != nil {
			item.PendingOperation = &pendingOperationResponse{
				TransferID: m.PendingTransferID.String(),
				Status:     string(m.ConfirmationStatus),
			}
		}

		items = append(items, item)
	}

	response.JSON(w, r, http.StatusOK, map[string]any{
		"messages": items,
		"hasMore":  hasMore,
	})
}

func (h *ChatHandler) confirm(w http.ResponseWriter, r *http.Request) {
	h.resolve(w, r, true)
}

func (h *ChatHandler) reject(w http.ResponseWriter, r *http.Request) {
	h.resolve(w, r, false)
}

// resolve confirma o rechaza una operación propuesta por el asistente.
//
// Este endpoint es la barrera que impide que el modelo mueva dinero: la
// decisión llega por HTTP autenticado, desde la interfaz, nunca desde la IA.
func (h *ChatHandler) resolve(w http.ResponseWriter, r *http.Request, confirm bool) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesita iniciar sesión")
		return
	}

	transferID, ok := new(big.Int).SetString(chi.URLParam(r, "transferID"), 10)
	if !ok {
		response.Error(w, r, http.StatusBadRequest,
			"INVALID_TRANSFER_ID", "El identificador de la operación no es válido")
		return
	}

	var err error
	var message string
	if confirm {
		err = h.service.Confirm(r.Context(), userID, transferID)
		message = "Operación confirmada"
	} else {
		err = h.service.Reject(r.Context(), userID, transferID)
		message = "Operación cancelada"
	}

	if err != nil {
		h.writeError(w, r, err)
		return
	}

	response.JSON(w, r, http.StatusOK, map[string]string{"message": message})
}

func (h *ChatHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	code := domain.ErrorCode(err)

	switch {
	case errors.Is(err, domain.ErrEmptyMessage):
		response.Error(w, r, http.StatusUnprocessableEntity, code, "El mensaje no puede estar vacío")

	case errors.Is(err, domain.ErrTransferNotFound):
		response.Error(w, r, http.StatusNotFound, code, "La operación no existe")

	case errors.Is(err, domain.ErrForbidden):
		// Mismo 404 que una operación inexistente: un 403 confirmaría que
		// existe y es de otra persona.
		response.Error(w, r, http.StatusNotFound, "TRANSFER_NOT_FOUND", "La operación no existe")

	case errors.Is(err, domain.ErrTransferResolved):
		response.Error(w, r, http.StatusConflict, code, "Esta operación ya fue confirmada o cancelada")

	case errors.Is(err, domain.ErrTransferExpired):
		response.Error(w, r, http.StatusGone, code, "La operación expiró. Pedile al asistente que la prepare de nuevo.")

	case errors.Is(err, domain.ErrInsufficientFunds):
		response.Error(w, r, http.StatusUnprocessableEntity, code, "No tiene fondos suficientes para esta operación")

	// Fallos del proveedor de IA. Se distinguen del error genérico porque el
	// problema no está en la aplicación sino en la configuración del servicio,
	// y el mensaje debe orientar a quien lo administra.
	case errors.Is(err, openrouter.ErrInsufficientCredits):
		logger.FromContext(r.Context(), h.log).Error("la cuenta de OpenRouter se quedó sin créditos")
		response.Error(w, r, http.StatusServiceUnavailable,
			"AI_QUOTA_EXCEEDED", "El asistente no está disponible: la cuenta del proveedor de IA no tiene créditos.")

	case errors.Is(err, openrouter.ErrRateLimited),
		errors.Is(err, openrouter.ErrProviderOverloaded):
		response.Error(w, r, http.StatusServiceUnavailable,
			"AI_OVERLOADED",
			"El asistente está saturado en este momento. Espere unos segundos e intente de nuevo.")

	// Una consulta que tardó demasiado. Se distingue de un fallo real para que
	// la persona sepa que puede reintentar.
	case errors.Is(err, context.DeadlineExceeded):
		response.Error(w, r, http.StatusGatewayTimeout,
			"AI_TIMEOUT", "El asistente tardó demasiado en responder. Intentá de nuevo.")

	case errors.Is(err, openrouter.ErrInvalidAPIKey):
		logger.FromContext(r.Context(), h.log).Error("la clave de OpenRouter no es válida")
		response.Error(w, r, http.StatusServiceUnavailable,
			"AI_MISCONFIGURED", "El asistente no está configurado correctamente.")

	default:
		logger.FromContext(r.Context(), h.log).Error("error inesperado en el chat", "error", err)
		response.Error(w, r, http.StatusInternalServerError,
			"INTERNAL_ERROR", "El asistente no está disponible en este momento. Intentá de nuevo.")
	}
}
