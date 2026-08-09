package http

import (
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Moisinho/banca-ai/apps/api/internal/banking"
	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/http/middleware"
	"github.com/Moisinho/banca-ai/apps/api/internal/http/response"
	"github.com/Moisinho/banca-ai/apps/api/internal/logger"
)

// maxHistoryLimit acota cuántas transacciones puede pedir un cliente por página.
const maxHistoryLimit = 100

// BankingHandler expone los endpoints de cuentas y transacciones.
type BankingHandler struct {
	accounts     *banking.AccountService
	transactions *banking.TransactionService
	log          *slog.Logger
}

func NewBankingHandler(accounts *banking.AccountService, transactions *banking.TransactionService, log *slog.Logger) *BankingHandler {
	return &BankingHandler{accounts: accounts, transactions: transactions, log: log}
}

// AccountRoutes monta las rutas de cuentas.
func (h *BankingHandler) AccountRoutes(r chi.Router) {
	r.Get("/", h.listAccounts)
	r.Get("/{accountID}", h.getAccount)
	r.Get("/{accountID}/balance", h.getBalance)
	r.Get("/{accountID}/transactions", h.getHistory)
	r.Get("/{accountID}/transactions/export", h.exportHistory)
}

// TransactionRoutes monta las rutas de transacciones.
func (h *BankingHandler) TransactionRoutes(r chi.Router) {
	r.Post("/deposit", h.deposit)
	r.Post("/withdraw", h.withdraw)
	r.Post("/transfer", h.transfer)
	r.Post("/{transferID}/confirm", h.confirmPending)
	r.Post("/{transferID}/reject", h.rejectPending)
}

// ------------------------------------------------------------------------------
// Representaciones de salida
// ------------------------------------------------------------------------------

type accountResponse struct {
	ID            string `json:"id"`
	AccountNumber string `json:"accountNumber"`
	AccountType   string `json:"accountType"`
	Currency      string `json:"currency"`
	CreatedAt     string `json:"createdAt"`

	// Los montos viajan como cadena, no como número. Un número JSON es un
	// float de doble precisión y no puede representar todos los decimales de
	// forma exacta: serializarlo como texto conserva el valor intacto.
	Available string `json:"available"`
	Posted    string `json:"posted"`
	Pending   string `json:"pending"`
}

func toAccountResponse(a banking.AccountWithBalance) accountResponse {
	return accountResponse{
		ID:            a.Account.ID,
		AccountNumber: a.Account.AccountNumber,
		AccountType:   string(a.Account.Type),
		Currency:      a.Account.Currency,
		CreatedAt:     a.Account.CreatedAt.Format(time.RFC3339),
		Available:     a.Balance.Available.String(),
		Posted:        a.Balance.Posted.String(),
		Pending:       a.Balance.Pending.String(),
	}
}

type transactionResponse struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	FromAccount string `json:"fromAccount"`
	ToAccount   string `json:"toAccount"`
	Description string `json:"description"`
	Direction   string `json:"direction"`
	Timestamp   string `json:"timestamp"`
}

func toTransactionResponse(t domain.Transaction) transactionResponse {
	id := ""
	if t.ID != nil {
		id = t.ID.String()
	}

	return transactionResponse{
		ID:          id,
		Type:        string(t.Type),
		Status:      string(t.Status),
		Amount:      t.Amount.String(),
		Currency:    t.Currency,
		FromAccount: t.FromAccount,
		ToAccount:   t.ToAccount,
		Description: t.Description,
		Direction:   string(t.Direction),
		Timestamp:   t.Timestamp.Format(time.RFC3339),
	}
}

// ------------------------------------------------------------------------------
// Cuentas
// ------------------------------------------------------------------------------

func (h *BankingHandler) listAccounts(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesita iniciar sesión")
		return
	}

	accounts, err := h.accounts.ListByUser(r.Context(), userID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	items := make([]accountResponse, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, toAccountResponse(account))
	}

	response.JSON(w, r, http.StatusOK, map[string]any{"accounts": items})
}

func (h *BankingHandler) getAccount(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesita iniciar sesión")
		return
	}

	account, err := h.accounts.GetByID(r.Context(), userID, chi.URLParam(r, "accountID"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	response.JSON(w, r, http.StatusOK, toAccountResponse(account))
}

func (h *BankingHandler) getBalance(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesita iniciar sesión")
		return
	}

	balance, err := h.accounts.GetBalance(r.Context(), userID, chi.URLParam(r, "accountID"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	response.JSON(w, r, http.StatusOK, map[string]string{
		"accountId":     balance.AccountID,
		"accountNumber": balance.AccountNumber,
		"available":     balance.Available.String(),
		"posted":        balance.Posted.String(),
		"pending":       balance.Pending.String(),
		"currency":      balance.Currency,
	})
}

// ------------------------------------------------------------------------------
// Historial
// ------------------------------------------------------------------------------

func (h *BankingHandler) getHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesita iniciar sesión")
		return
	}

	input, err := h.parseHistoryInput(r, userID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	page, err := h.transactions.History(r.Context(), input)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	items := make([]transactionResponse, 0, len(page.Items))
	for _, tx := range page.Items {
		items = append(items, toTransactionResponse(tx))
	}

	response.JSON(w, r, http.StatusOK, map[string]any{
		"items":      items,
		"nextCursor": page.NextCursor,
		"hasMore":    page.HasMore,
	})
}

// exportHistory descarga el historial en CSV o PDF.
func (h *BankingHandler) exportHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesita iniciar sesión")
		return
	}

	input, err := h.parseHistoryInput(r, userID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	// Para exportar traemos bastante más que una página: el archivo se descarga
	// una vez y tiene sentido que sea completo.
	input.Limit = 500

	account, err := h.accounts.GetByID(r.Context(), userID, input.AccountID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	page, err := h.transactions.History(r.Context(), input)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}

	timestamp := time.Now().Format("2006-01-02")

	switch format {
	case "csv":
		data, err := banking.ExportCSV(page.Items, account.Account.AccountNumber)
		if err != nil {
			h.writeError(w, r, err)
			return
		}

		filename := fmt.Sprintf("movimientos-%s-%s.csv", account.Account.AccountNumber, timestamp)
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)

	case "pdf":
		data, err := banking.ExportPDF(page.Items, account.Account.AccountNumber, time.Now())
		if err != nil {
			h.writeError(w, r, err)
			return
		}

		filename := fmt.Sprintf("movimientos-%s-%s.pdf", account.Account.AccountNumber, timestamp)
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)

	default:
		response.Error(w, r, http.StatusBadRequest,
			"INVALID_FORMAT", "El formato debe ser csv o pdf")
	}
}

// parseHistoryInput arma la consulta a partir de los parámetros de la URL.
func (h *BankingHandler) parseHistoryInput(r *http.Request, userID string) (banking.HistoryInput, error) {
	query := r.URL.Query()

	input := banking.HistoryInput{
		UserID:    userID,
		AccountID: chi.URLParam(r, "accountID"),
		Cursor:    query.Get("cursor"),
	}

	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			return banking.HistoryInput{}, domain.ErrInvalidLimit
		}
		if limit > maxHistoryLimit {
			limit = maxHistoryLimit
		}
		input.Limit = limit
	}

	if raw := query.Get("from"); raw != "" {
		from, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return banking.HistoryInput{}, domain.ErrInvalidDateRange
		}
		input.From = from
	}

	if raw := query.Get("to"); raw != "" {
		to, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return banking.HistoryInput{}, domain.ErrInvalidDateRange
		}
		input.To = to
	}

	return input, nil
}

// ------------------------------------------------------------------------------
// Transacciones
// ------------------------------------------------------------------------------

type depositRequest struct {
	AccountID   string `json:"accountId"`
	Amount      string `json:"amount"`
	Description string `json:"description"`
}

func (h *BankingHandler) deposit(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesita iniciar sesión")
		return
	}

	var req depositRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	amount, err := domain.ParseMoney(req.Amount)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	tx, err := h.transactions.Deposit(r.Context(), banking.DepositInput{
		UserID:      userID,
		AccountID:   req.AccountID,
		Amount:      amount,
		Description: req.Description,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	response.JSON(w, r, http.StatusCreated, toTransactionResponse(tx))
}

type withdrawRequest struct {
	AccountID   string `json:"accountId"`
	Amount      string `json:"amount"`
	Description string `json:"description"`
}

func (h *BankingHandler) withdraw(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesita iniciar sesión")
		return
	}

	var req withdrawRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	amount, err := domain.ParseMoney(req.Amount)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	tx, err := h.transactions.Withdraw(r.Context(), banking.WithdrawInput{
		UserID:      userID,
		AccountID:   req.AccountID,
		Amount:      amount,
		Description: req.Description,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	response.JSON(w, r, http.StatusCreated, toTransactionResponse(tx))
}

type transferRequest struct {
	FromAccountID   string `json:"fromAccountId"`
	ToAccountNumber string `json:"toAccountNumber"`
	Amount          string `json:"amount"`
	Description     string `json:"description"`
}

func (h *BankingHandler) transfer(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesita iniciar sesión")
		return
	}

	var req transferRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	amount, err := domain.ParseMoney(req.Amount)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	tx, err := h.transactions.Transfer(r.Context(), banking.TransferInput{
		UserID:          userID,
		FromAccountID:   req.FromAccountID,
		ToAccountNumber: req.ToAccountNumber,
		Amount:          amount,
		Description:     req.Description,
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	response.JSON(w, r, http.StatusCreated, toTransactionResponse(tx))
}

func (h *BankingHandler) confirmPending(w http.ResponseWriter, r *http.Request) {
	h.resolvePending(w, r, true)
}

func (h *BankingHandler) rejectPending(w http.ResponseWriter, r *http.Request) {
	h.resolvePending(w, r, false)
}

func (h *BankingHandler) resolvePending(w http.ResponseWriter, r *http.Request, confirm bool) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesita iniciar sesión")
		return
	}

	transferID, ok := new(big.Int).SetString(chi.URLParam(r, "transferID"), 10)
	if !ok {
		response.Error(w, r, http.StatusBadRequest,
			"INVALID_TRANSFER_ID", "El identificador de la transferencia no es válido")
		return
	}

	var err error
	var message string
	if confirm {
		err = h.transactions.ConfirmPending(r.Context(), userID, transferID)
		message = "Operación confirmada"
	} else {
		err = h.transactions.RejectPending(r.Context(), userID, transferID)
		message = "Operación cancelada"
	}

	if err != nil {
		h.writeError(w, r, err)
		return
	}

	response.JSON(w, r, http.StatusOK, map[string]string{"message": message})
}

// ------------------------------------------------------------------------------
// Traducción de errores
// ------------------------------------------------------------------------------

// writeError convierte un error del dominio en una respuesta HTTP.
func (h *BankingHandler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	code := domain.ErrorCode(err)

	switch {
	case errors.Is(err, domain.ErrInsufficientFunds):
		response.Error(w, r, http.StatusUnprocessableEntity, code,
			"No tiene fondos suficientes para esta operación")

	case errors.Is(err, domain.ErrAccountNotFound):
		response.Error(w, r, http.StatusNotFound, code, "La cuenta no existe")

	case errors.Is(err, domain.ErrForbidden):
		// El mismo 404 que una cuenta inexistente: responder 403 confirmaría
		// que la cuenta existe y es de otra persona.
		response.Error(w, r, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "La cuenta no existe")

	case errors.Is(err, domain.ErrSameAccount):
		response.Error(w, r, http.StatusUnprocessableEntity, code,
			"No puede transferir dinero a la misma cuenta")

	case errors.Is(err, domain.ErrTransferNotFound):
		response.Error(w, r, http.StatusNotFound, code, "La operación no existe")

	case errors.Is(err, domain.ErrTransferResolved):
		response.Error(w, r, http.StatusConflict, code,
			"Esta operación ya fue confirmada o cancelada")

	case errors.Is(err, domain.ErrTransferExpired):
		response.Error(w, r, http.StatusGone, code,
			"La operación expiró. Volvé a intentarlo.")

	case errors.Is(err, domain.ErrAmountNotPositive):
		response.Error(w, r, http.StatusUnprocessableEntity, code,
			"El monto debe ser mayor a cero")

	case errors.Is(err, domain.ErrAmountTooLarge):
		response.Error(w, r, http.StatusUnprocessableEntity, code,
			"El monto excede el máximo permitido por operación")

	case errors.Is(err, domain.ErrAmountFormat), errors.Is(err, domain.ErrAmountPrecision):
		response.Error(w, r, http.StatusUnprocessableEntity, code,
			"El monto no tiene un formato válido. Usá hasta dos decimales.")

	case errors.Is(err, domain.ErrInvalidCursor),
		errors.Is(err, domain.ErrInvalidLimit),
		errors.Is(err, domain.ErrInvalidDateRange):
		response.Error(w, r, http.StatusBadRequest, code, err.Error())

	default:
		logger.FromContext(r.Context(), h.log).Error("error inesperado en una operación bancaria", "error", err)
		response.Error(w, r, http.StatusInternalServerError,
			"INTERNAL_ERROR", "Ocurrió un error inesperado. Intentá de nuevo.")
	}
}
