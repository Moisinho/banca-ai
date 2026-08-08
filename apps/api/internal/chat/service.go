// Package chat orquesta la conversación con el modelo de lenguaje.
//
// Conecta tres piezas: el proveedor de IA (OpenRouter), las herramientas
// bancarias expuestas por MCP, y la persistencia de mensajes y operaciones
// pendientes.
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Moisinho/banca-ai/apps/api/internal/adapters/mcp"
	"github.com/Moisinho/banca-ai/apps/api/internal/banking"
	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// maxToolRounds acota cuántas veces seguidas puede el modelo invocar
// herramientas dentro de un mismo turno.
//
// Sin este límite, un modelo que se confunde puede entrar en un bucle de
// llamadas y consumir la cuota sin avanzar.
const maxToolRounds = 5

// maxHistoryMessages es cuántos mensajes previos se le envían al modelo.
//
// La conversación completa crecería sin control y cada petición costaría más.
// Con los últimos mensajes alcanza para mantener el hilo.
const maxHistoryMessages = 20

// systemPrompt define el comportamiento del asistente.
//
// Importante: este texto NO es un control de seguridad. Un modelo puede ser
// convencido de ignorarlo. Las garantías reales son arquitectónicas: las
// herramientas no aceptan un id de usuario, y ninguna mueve dinero por su
// cuenta. Esto sólo define el tono y la utilidad.
const systemPrompt = `Sos el asistente bancario de Banca AI. Ayudás a las personas a gestionar su dinero.

Podés:
- Consultar saldos y datos de cuentas
- Mostrar el historial de movimientos
- Preparar depósitos, retiros y transferencias

Reglas de comportamiento:
1. Las operaciones que mueven dinero NO se ejecutan cuando las preparás: quedan
   esperando que la persona las confirme en la interfaz. Nunca digas que una
   operación ya se realizó. Decí que está preparada y pendiente de confirmación.
2. Antes de preparar una transferencia, repetí en tu respuesta el monto y la
   cuenta destino para que la persona pueda revisarlos.
3. Si una herramienta devuelve un error, explicá en lenguaje claro qué pasó.
4. Respondé siempre en español, de forma breve y concreta.
5. Los montos van siempre con dos decimales y su moneda.
6. Si te piden algo que no podés hacer, decilo con claridad en lugar de inventar.

Sólo tenés acceso a las cuentas de la persona con la que estás hablando. No
existe forma de consultar ni operar cuentas de terceros.`

// Service maneja la conversación con el modelo.
type Service struct {
	provider     ports.AIProvider
	mcpServer    *mcp.Server
	messages     ports.ChatMessageRepository
	transactions *banking.TransactionService
	accounts     *banking.AccountService
	log          *slog.Logger
}

func NewService(
	provider ports.AIProvider,
	mcpServer *mcp.Server,
	messages ports.ChatMessageRepository,
	transactions *banking.TransactionService,
	accounts *banking.AccountService,
	log *slog.Logger,
) *Service {
	return &Service{
		provider:     provider,
		mcpServer:    mcpServer,
		messages:     messages,
		transactions: transactions,
		accounts:     accounts,
		log:          log,
	}
}

// Reply es la respuesta a un mensaje del usuario.
type Reply struct {
	Message          domain.ChatMessage
	PendingOperation *domain.PendingOperation
}

// Send procesa un mensaje del usuario y devuelve la respuesta del asistente.
func (s *Service) Send(ctx context.Context, userID, text string) (Reply, error) {
	if text == "" {
		return Reply{}, domain.ErrEmptyMessage
	}

	// Se guarda el mensaje del usuario antes de llamar al modelo: si el
	// proveedor falla, la conversación no pierde lo que la persona escribió.
	userMessage := domain.ChatMessage{
		UserID:  userID,
		Role:    domain.ChatRoleUser,
		Content: text,
	}
	if _, err := s.messages.Store(ctx, userMessage); err != nil {
		return Reply{}, err
	}

	history, err := s.messages.ListRecent(ctx, userID, maxHistoryMessages)
	if err != nil {
		return Reply{}, err
	}

	// Conexión in-process al servidor MCP.
	//
	// No hay proceso aparte ni puerto abierto: nadie puede invocar estas
	// herramientas saltándose la autenticación de la API.
	session, err := s.connectMCP(ctx, userID)
	if err != nil {
		return Reply{}, err
	}
	defer session.Close()

	tools, err := s.listTools(ctx, session)
	if err != nil {
		return Reply{}, err
	}

	conversation := buildConversation(history)

	var pending *domain.PendingOperation

	// El modelo puede necesitar varias vueltas: consultar el saldo, después
	// preparar una transferencia, y recién ahí responder.
	for round := 0; round < maxToolRounds; round++ {
		completion, err := s.provider.Complete(ctx, ports.CompletionRequest{
			Messages: conversation,
			Tools:    tools,
		})
		if err != nil {
			return Reply{}, err
		}

		// Sin llamadas a herramientas, la respuesta es definitiva.
		if len(completion.ToolCalls) == 0 {
			assistant := domain.ChatMessage{
				UserID:  userID,
				Role:    domain.ChatRoleAssistant,
				Content: completion.Content,
			}

			if pending != nil {
				assistant.PendingTransferID = pending.TransferID
				assistant.ConfirmationStatus = domain.ConfirmationPending
			}

			stored, err := s.messages.Store(ctx, assistant)
			if err != nil {
				return Reply{}, err
			}

			return Reply{Message: stored, PendingOperation: pending}, nil
		}

		// Se agrega la decisión del modelo antes de los resultados: el formato
		// exige que cada respuesta de herramienta siga a su llamada.
		conversation = append(conversation, ports.ChatMessage{
			Role:      "assistant",
			Content:   completion.Content,
			ToolCalls: completion.ToolCalls,
		})

		for _, call := range completion.ToolCalls {
			result, proposal, err := s.executeTool(ctx, session, userID, call)
			if err != nil {
				return Reply{}, err
			}

			if proposal != nil {
				pending = proposal
			}

			conversation = append(conversation, ports.ChatMessage{
				Role:       "tool",
				Content:    result,
				ToolCallID: call.ID,
			})
		}
	}

	// Se agotaron las vueltas sin una respuesta final.
	s.log.Warn("el modelo agotó el límite de llamadas a herramientas", "user_id", userID)

	assistant := domain.ChatMessage{
		UserID:  userID,
		Role:    domain.ChatRoleAssistant,
		Content: "No pude completar la consulta. ¿Podés reformularla?",
	}
	stored, err := s.messages.Store(ctx, assistant)
	if err != nil {
		return Reply{}, err
	}

	return Reply{Message: stored, PendingOperation: pending}, nil
}

// executeTool invoca una herramienta MCP y devuelve su resultado en texto.
//
// Si la herramienta preparó una operación que mueve dinero, acá se crea la
// reserva en TigerBeetle y se devuelve para que el frontend muestre la tarjeta
// de confirmación.
func (s *Service) executeTool(ctx context.Context, session *mcpsdk.ClientSession, userID string, call ports.ToolCall) (string, *domain.PendingOperation, error) {
	s.log.Info("la IA invocó una herramienta",
		"user_id", userID,
		"tool", call.Name,
	)

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      call.Name,
		Arguments: call.Arguments,
	})
	if err != nil {
		return "", nil, fmt.Errorf("falló la herramienta %s: %w", call.Name, err)
	}

	text := extractText(result)

	// Las herramientas de lectura no proponen nada.
	if !movesMoney(call.Name) || result.IsError {
		return text, nil, nil
	}

	proposal, err := decodeProposal(result)
	if err != nil {
		s.log.Error("no se pudo interpretar la propuesta de la herramienta",
			"tool", call.Name,
			"error", err,
		)
		return text, nil, nil
	}

	// Recién acá se reservan los fondos en TigerBeetle.
	//
	// Se hace después de que la herramienta valide, para que una propuesta
	// rechazada no llegue a bloquear dinero.
	pending, err := s.reserveFunds(ctx, userID, proposal)
	if err != nil {
		// La reserva falló: el modelo debe enterarse para explicárselo a la
		// persona, en vez de anunciar una operación que no existe.
		return fmt.Sprintf("No se pudo preparar la operación: %s", domainMessage(err)), nil, nil
	}

	return text, pending, nil
}

// reserveFunds crea la transferencia pendiente en TigerBeetle.
func (s *Service) reserveFunds(ctx context.Context, userID string, p mcp.PendingOperation) (*domain.PendingOperation, error) {
	var tx domain.Transaction
	var err error

	switch p.Operation {
	case domain.TransactionTypeDeposit:
		// Un depósito no puede fallar por fondos, pero igual pasa por
		// confirmación: la persona debe poder revisar el monto antes.
		tx, err = s.transactions.DepositPending(ctx, banking.DepositInput{
			UserID:      userID,
			AccountID:   p.AccountID,
			Amount:      p.Amount,
			Description: p.Description,
		})

	case domain.TransactionTypeWithdrawal:
		tx, err = s.transactions.WithdrawPending(ctx, banking.WithdrawInput{
			UserID:      userID,
			AccountID:   p.AccountID,
			Amount:      p.Amount,
			Description: p.Description,
		})

	case domain.TransactionTypeTransfer:
		tx, err = s.transactions.Transfer(ctx, banking.TransferInput{
			UserID:          userID,
			FromAccountID:   p.AccountID,
			ToAccountNumber: p.ToAccount,
			Amount:          p.Amount,
			Description:     p.Description,
			Pending:         true,
		})

	default:
		return nil, fmt.Errorf("operación no soportada: %s", p.Operation)
	}

	if err != nil {
		return nil, err
	}

	return &domain.PendingOperation{
		TransferID:  tx.ID,
		Operation:   p.Operation,
		Amount:      p.Amount,
		Currency:    p.Currency,
		FromAccount: p.FromAccount,
		ToAccount:   p.ToAccount,
		Description: p.Description,
		Status:      domain.ConfirmationPending,
		ExpiresAt:   time.Now().Add(banking.DefaultPendingTimeout),
	}, nil
}

// Confirm ejecuta una operación que el usuario aprobó.
func (s *Service) Confirm(ctx context.Context, userID string, transferID *big.Int) error {
	message, err := s.messages.FindByPendingTransfer(ctx, userID, transferID)
	if err != nil {
		return err
	}

	// Sin esta comprobación, alguien podría confirmar la operación pendiente de
	// otra persona conociendo su identificador.
	if message.UserID != userID {
		return domain.ErrForbidden
	}

	if message.ConfirmationStatus != domain.ConfirmationPending {
		return domain.ErrTransferResolved
	}

	if err := s.transactions.ConfirmPending(ctx, userID, transferID); err != nil {
		return err
	}

	if err := s.messages.UpdateConfirmation(ctx, transferID, domain.ConfirmationConfirmed); err != nil {
		// El dinero ya se movió: no revertimos por un fallo al actualizar el
		// estado del mensaje, sólo lo registramos.
		s.log.Error("la operación se confirmó pero no se pudo actualizar el mensaje",
			"transfer_id", transferID.String(),
			"error", err,
		)
	}

	s.log.Info("operación confirmada por el usuario",
		"user_id", userID,
		"transfer_id", transferID.String(),
	)

	return nil
}

// Reject cancela una operación pendiente y libera los fondos reservados.
func (s *Service) Reject(ctx context.Context, userID string, transferID *big.Int) error {
	message, err := s.messages.FindByPendingTransfer(ctx, userID, transferID)
	if err != nil {
		return err
	}

	if message.UserID != userID {
		return domain.ErrForbidden
	}

	if message.ConfirmationStatus != domain.ConfirmationPending {
		return domain.ErrTransferResolved
	}

	if err := s.transactions.RejectPending(ctx, userID, transferID); err != nil {
		return err
	}

	if err := s.messages.UpdateConfirmation(ctx, transferID, domain.ConfirmationRejected); err != nil {
		s.log.Error("la operación se canceló pero no se pudo actualizar el mensaje",
			"transfer_id", transferID.String(),
			"error", err,
		)
	}

	s.log.Info("operación rechazada por el usuario",
		"user_id", userID,
		"transfer_id", transferID.String(),
	)

	return nil
}

// History devuelve los mensajes recientes de la conversación.
func (s *Service) History(ctx context.Context, userID string, limit int) ([]domain.ChatMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.messages.ListRecent(ctx, userID, limit)
}

// ------------------------------------------------------------------------------
// Utilidades
// ------------------------------------------------------------------------------

// connectMCP abre una sesión in-process con el servidor de herramientas.
//
// El contexto lleva el usuario autenticado: es así como las herramientas saben
// en nombre de quién operan, sin que el modelo pueda influir en ello.
func (s *Service) connectMCP(ctx context.Context, userID string) (*mcpsdk.ClientSession, error) {
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()

	toolCtx := mcp.WithUserID(context.WithoutCancel(ctx), userID)
	if _, err := s.mcpServer.MCPServer().Connect(toolCtx, serverTransport, nil); err != nil {
		return nil, fmt.Errorf("no se pudo iniciar el servidor de herramientas: %w", err)
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "banca-ai-chat",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("no se pudo conectar con el servidor de herramientas: %w", err)
	}

	return session, nil
}

// listTools obtiene las herramientas disponibles y las traduce al formato que
// espera el proveedor de IA.
func (s *Service) listTools(ctx context.Context, session *mcpsdk.ClientSession) ([]ports.ToolDefinition, error) {
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron listar las herramientas: %w", err)
	}

	tools := make([]ports.ToolDefinition, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		schema := map[string]any{"type": "object", "properties": map[string]any{}}

		// El esquema del SDK se traduce vía JSON: es la forma más directa de
		// convertirlo al formato que espera la API del modelo.
		if tool.InputSchema != nil {
			if raw, err := json.Marshal(tool.InputSchema); err == nil {
				_ = json.Unmarshal(raw, &schema)
			}
		}

		tools = append(tools, ports.ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		})
	}

	return tools, nil
}

// buildConversation arma el historial para el modelo, con el prompt de sistema
// al principio.
func buildConversation(history []domain.ChatMessage) []ports.ChatMessage {
	conversation := make([]ports.ChatMessage, 0, len(history)+1)
	conversation = append(conversation, ports.ChatMessage{
		Role:    "system",
		Content: systemPrompt,
	})

	for _, m := range history {
		// Los mensajes de herramientas no se reenvían: pertenecen a un turno ya
		// cerrado y sin su llamada asociada el formato sería inválido.
		if m.Role == domain.ChatRoleTool {
			continue
		}

		conversation = append(conversation, ports.ChatMessage{
			Role:    string(m.Role),
			Content: m.Content,
		})
	}

	return conversation
}

// extractText junta el contenido textual del resultado de una herramienta.
func extractText(result *mcpsdk.CallToolResult) string {
	var text string
	for _, content := range result.Content {
		if textContent, ok := content.(*mcpsdk.TextContent); ok {
			text += textContent.Text
		}
	}
	return text
}

// decodeProposal recupera la operación estructurada del resultado.
func decodeProposal(result *mcpsdk.CallToolResult) (mcp.PendingOperation, error) {
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return mcp.PendingOperation{}, err
	}

	var proposal mcp.PendingOperation
	if err := json.Unmarshal(raw, &proposal); err != nil {
		return mcp.PendingOperation{}, err
	}

	return proposal, nil
}

// movesMoney indica si una herramienta requiere confirmación del usuario.
func movesMoney(toolName string) bool {
	switch toolName {
	case "deposit", "withdraw", "transfer":
		return true
	default:
		return false
	}
}

// domainMessage traduce un error a un texto que el modelo pueda transmitir.
func domainMessage(err error) string {
	switch {
	case domain.IsInsufficientFunds(err):
		return "no hay fondos suficientes"
	case domain.IsAccountNotFound(err):
		return "la cuenta indicada no existe"
	case domain.IsInvalidAmount(err):
		return "el monto no es válido"
	default:
		return "ocurrió un error inesperado"
	}
}
