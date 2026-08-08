package chat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Moisinho/banca-ai/apps/api/internal/adapters/mcp"
	"github.com/Moisinho/banca-ai/apps/api/internal/banking"
	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// ------------------------------------------------------------------------------
// Proveedor de IA falso
//
// Devuelve respuestas guionadas. Permite verificar la orquestación completa —
// invocación de herramientas, reserva de fondos, confirmación — sin gastar
// llamadas reales al modelo ni depender de que responda algo concreto.
// ------------------------------------------------------------------------------

type scriptedProvider struct {
	mu        sync.Mutex
	responses []ports.CompletionResponse
	calls     int

	// received guarda lo que se le envió, para poder verificar que el prompt
	// de sistema y el historial llegan bien.
	received []ports.CompletionRequest
}

func (p *scriptedProvider) Complete(_ context.Context, req ports.CompletionRequest) (ports.CompletionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.received = append(p.received, req)

	if p.calls >= len(p.responses) {
		return ports.CompletionResponse{Content: "Sin más respuestas guionadas."}, nil
	}

	response := p.responses[p.calls]
	p.calls++
	return response, nil
}

// failingProvider simula una caída del proveedor de IA.
type failingProvider struct{}

func (failingProvider) Complete(context.Context, ports.CompletionRequest) (ports.CompletionResponse, error) {
	return ports.CompletionResponse{}, errors.New("el proveedor no responde")
}

// ------------------------------------------------------------------------------
// Repositorio de mensajes falso
// ------------------------------------------------------------------------------

type fakeMessageRepo struct {
	mu       sync.Mutex
	messages []domain.ChatMessage
}

func newFakeMessageRepo() *fakeMessageRepo {
	return &fakeMessageRepo{}
}

func (f *fakeMessageRepo) Store(_ context.Context, message domain.ChatMessage) (domain.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	message.ID = uuid.NewString()
	message.CreatedAt = time.Now()
	f.messages = append(f.messages, message)
	return message, nil
}

func (f *fakeMessageRepo) ListRecent(_ context.Context, userID string, limit int) ([]domain.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []domain.ChatMessage
	for _, m := range f.messages {
		if m.UserID == userID {
			out = append(out, m)
		}
	}

	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (f *fakeMessageRepo) FindByPendingTransfer(_ context.Context, userID string, transferID *big.Int) (domain.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, m := range f.messages {
		if m.PendingTransferID != nil && m.PendingTransferID.Cmp(transferID) == 0 {
			return m, nil
		}
	}
	return domain.ChatMessage{}, domain.ErrTransferNotFound
}

func (f *fakeMessageRepo) UpdateConfirmation(_ context.Context, transferID *big.Int, status domain.ConfirmationStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.messages {
		m := &f.messages[i]
		if m.PendingTransferID != nil && m.PendingTransferID.Cmp(transferID) == 0 {
			if m.ConfirmationStatus != domain.ConfirmationPending {
				return domain.ErrTransferResolved
			}
			m.ConfirmationStatus = status
			return nil
		}
	}
	return domain.ErrTransferNotFound
}

// ------------------------------------------------------------------------------
// Preparación del entorno
// ------------------------------------------------------------------------------

type testEnv struct {
	service      *Service
	provider     *scriptedProvider
	messages     *fakeMessageRepo
	accounts     *banking.AccountService
	transactions *banking.TransactionService
	ledger       *fakeLedger
	repo         *fakeAccountRepo
}

func newTestEnv(t *testing.T, responses ...ports.CompletionResponse) *testEnv {
	t.Helper()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	repo := newFakeAccountRepo()
	ledger := newFakeLedger()
	metadata := newFakeMetadataRepo()

	accounts := banking.NewAccountService(repo, ledger, log)
	transactions := banking.NewTransactionService(repo, ledger, metadata, log)

	provider := &scriptedProvider{responses: responses}
	messages := newFakeMessageRepo()
	mcpServer := mcp.NewServer(accounts, transactions, log)

	return &testEnv{
		service:      NewService(provider, mcpServer, messages, transactions, accounts, log),
		provider:     provider,
		messages:     messages,
		accounts:     accounts,
		transactions: transactions,
		ledger:       ledger,
		repo:         repo,
	}
}

func (e *testEnv) createFundedAccount(t *testing.T, userID, amount string) domain.Account {
	t.Helper()
	ctx := context.Background()

	number, err := e.repo.NextAccountNumber(ctx)
	require.NoError(t, err)

	tigerBeetleID := big.NewInt(time.Now().UnixNano() + int64(len(e.repo.accounts)*7919))
	require.NoError(t, e.ledger.CreateAccount(ctx, tigerBeetleID, domain.AccountTypeChecking))

	account, err := e.repo.Create(ctx, domain.Account{
		UserID:        userID,
		AccountNumber: number,
		TigerBeetleID: tigerBeetleID,
		Type:          domain.AccountTypeChecking,
		Currency:      "USD",
	})
	require.NoError(t, err)

	if amount != "" {
		money, err := domain.ParseMoney(amount)
		require.NoError(t, err)

		_, err = e.transactions.Deposit(ctx, banking.DepositInput{
			UserID:    userID,
			AccountID: account.ID,
			Amount:    money,
		})
		require.NoError(t, err)
	}

	return account
}

// ------------------------------------------------------------------------------
// Conversación básica
// ------------------------------------------------------------------------------

func TestConsultaSinHerramientasDevuelveLaRespuesta(t *testing.T) {
	env := newTestEnv(t, ports.CompletionResponse{
		Content: "Hola, ¿en qué puedo ayudarte?",
	})

	userID := uuid.NewString()
	env.createFundedAccount(t, userID, "")

	reply, err := env.service.Send(context.Background(), userID, "Hola")
	require.NoError(t, err)

	assert.Equal(t, "Hola, ¿en qué puedo ayudarte?", reply.Message.Content)
	assert.Equal(t, domain.ChatRoleAssistant, reply.Message.Role)
	assert.Nil(t, reply.PendingOperation, "una conversación sin operaciones no deja nada pendiente")
}

func TestElMensajeVacioSeRechaza(t *testing.T) {
	env := newTestEnv(t)

	_, err := env.service.Send(context.Background(), uuid.NewString(), "")
	assert.ErrorIs(t, err, domain.ErrEmptyMessage)
}

// El prompt de sistema tiene que llegar siempre como primer mensaje.
func TestElPromptDeSistemaSeEnviaPrimero(t *testing.T) {
	env := newTestEnv(t, ports.CompletionResponse{Content: "Listo."})

	userID := uuid.NewString()
	env.createFundedAccount(t, userID, "")

	_, err := env.service.Send(context.Background(), userID, "¿Cuánto tengo?")
	require.NoError(t, err)

	require.NotEmpty(t, env.provider.received)
	primero := env.provider.received[0].Messages[0]

	assert.Equal(t, "system", primero.Role)
	assert.Contains(t, primero.Content, "asistente bancario")
}

// Las herramientas se le ofrecen al modelo en cada petición.
func TestLasHerramientasSeOfrecenAlModelo(t *testing.T) {
	env := newTestEnv(t, ports.CompletionResponse{Content: "Listo."})

	userID := uuid.NewString()
	env.createFundedAccount(t, userID, "")

	_, err := env.service.Send(context.Background(), userID, "Hola")
	require.NoError(t, err)

	require.NotEmpty(t, env.provider.received)
	tools := env.provider.received[0].Tools

	nombres := make([]string, 0, len(tools))
	for _, tool := range tools {
		nombres = append(nombres, tool.Name)
	}

	assert.Contains(t, nombres, "get_balance")
	assert.Contains(t, nombres, "transfer")
	assert.Len(t, tools, 6, "el modelo debe recibir las seis herramientas")
}

// ------------------------------------------------------------------------------
// Consultas de sólo lectura
// ------------------------------------------------------------------------------

func TestConsultarSaldoInvocaLaHerramienta(t *testing.T) {
	env := newTestEnv(t,
		// Primera vuelta: el modelo decide consultar el saldo.
		ports.CompletionResponse{
			ToolCalls: []ports.ToolCall{{
				ID:        "call-1",
				Name:      "get_balance",
				Arguments: map[string]any{},
			}},
		},
		// Segunda vuelta: responde con el dato ya en mano.
		ports.CompletionResponse{Content: "Tenés 1500.00 USD disponibles."},
	)

	userID := uuid.NewString()
	env.createFundedAccount(t, userID, "1500.00")

	reply, err := env.service.Send(context.Background(), userID, "¿Cuánto dinero tengo?")
	require.NoError(t, err)

	assert.Contains(t, reply.Message.Content, "1500.00")
	assert.Nil(t, reply.PendingOperation, "consultar el saldo no genera nada pendiente")

	// La segunda petición al modelo debe incluir el resultado de la herramienta.
	require.Len(t, env.provider.received, 2)
	segunda := env.provider.received[1].Messages
	assert.Equal(t, "tool", segunda[len(segunda)-1].Role)
	assert.Contains(t, segunda[len(segunda)-1].Content, "1500.00")
}

// ------------------------------------------------------------------------------
// Operaciones que mueven dinero
//
// Estos son los tests que verifican el requisito explícito de la prueba:
// "La IA debe confirmar acciones críticas antes de ejecutarlas".
// ------------------------------------------------------------------------------

func TestLaTransferenciaQuedaPendienteYNoMueveDinero(t *testing.T) {
	env := newTestEnv(t,
		ports.CompletionResponse{
			ToolCalls: []ports.ToolCall{{
				ID:   "call-1",
				Name: "transfer",
				Arguments: map[string]any{
					"toAccountNumber": "",
					"amount":          "300.00",
					"description":     "Pago de alquiler",
				},
			}},
		},
		ports.CompletionResponse{
			Content: "Preparé una transferencia de 300.00 USD. Confirmala para completarla.",
		},
	)

	emisor := uuid.NewString()
	receptor := uuid.NewString()

	cuentaEmisor := env.createFundedAccount(t, emisor, "1000.00")
	cuentaReceptor := env.createFundedAccount(t, receptor, "")

	// El destino se conoce recién ahora, así que se completa el guion.
	env.provider.responses[0].ToolCalls[0].Arguments["toAccountNumber"] = cuentaReceptor.AccountNumber

	ctx := context.Background()
	reply, err := env.service.Send(ctx, emisor, "Transferí 300 a "+cuentaReceptor.AccountNumber)
	require.NoError(t, err)

	// Hay una operación esperando decisión.
	require.NotNil(t, reply.PendingOperation, "la IA debe dejar la operación pendiente")
	assert.Equal(t, domain.ConfirmationPending, reply.PendingOperation.Status)
	assert.Equal(t, "300.00", reply.PendingOperation.Amount.String())
	assert.Equal(t, cuentaReceptor.AccountNumber, reply.PendingOperation.ToAccount)

	// Los fondos están reservados pero el dinero NO se movió.
	saldoEmisor, err := env.accounts.GetBalance(ctx, emisor, cuentaEmisor.ID)
	require.NoError(t, err)
	assert.Equal(t, "700.00", saldoEmisor.Available.String(), "el disponible baja por la reserva")
	assert.Equal(t, "300.00", saldoEmisor.Pending.String(), "los fondos quedan reservados")
	assert.Equal(t, "1000.00", saldoEmisor.Posted.String(), "lo liquidado no cambia")

	saldoReceptor, err := env.accounts.GetBalance(ctx, receptor, cuentaReceptor.ID)
	require.NoError(t, err)
	assert.Equal(t, "0.00", saldoReceptor.Available.String(),
		"el destinatario NO recibe nada hasta que se confirme")
}

func TestConfirmarLaOperacionMueveElDinero(t *testing.T) {
	env := newTestEnv(t,
		ports.CompletionResponse{
			ToolCalls: []ports.ToolCall{{
				ID:        "call-1",
				Name:      "transfer",
				Arguments: map[string]any{"toAccountNumber": "", "amount": "300.00"},
			}},
		},
		ports.CompletionResponse{Content: "Operación preparada."},
	)

	emisor := uuid.NewString()
	receptor := uuid.NewString()

	cuentaEmisor := env.createFundedAccount(t, emisor, "1000.00")
	cuentaReceptor := env.createFundedAccount(t, receptor, "")
	env.provider.responses[0].ToolCalls[0].Arguments["toAccountNumber"] = cuentaReceptor.AccountNumber

	ctx := context.Background()
	reply, err := env.service.Send(ctx, emisor, "Transferí 300")
	require.NoError(t, err)
	require.NotNil(t, reply.PendingOperation)

	// El usuario confirma desde la interfaz.
	require.NoError(t, env.service.Confirm(ctx, emisor, reply.PendingOperation.TransferID))

	saldoEmisor, err := env.accounts.GetBalance(ctx, emisor, cuentaEmisor.ID)
	require.NoError(t, err)
	assert.Equal(t, "700.00", saldoEmisor.Available.String())
	assert.Equal(t, "0.00", saldoEmisor.Pending.String(), "ya no queda reserva")

	saldoReceptor, err := env.accounts.GetBalance(ctx, receptor, cuentaReceptor.ID)
	require.NoError(t, err)
	assert.Equal(t, "300.00", saldoReceptor.Available.String(), "ahora sí recibió el dinero")
}

func TestRechazarLaOperacionLiberaLosFondos(t *testing.T) {
	env := newTestEnv(t,
		ports.CompletionResponse{
			ToolCalls: []ports.ToolCall{{
				ID:        "call-1",
				Name:      "transfer",
				Arguments: map[string]any{"toAccountNumber": "", "amount": "300.00"},
			}},
		},
		ports.CompletionResponse{Content: "Operación preparada."},
	)

	emisor := uuid.NewString()
	receptor := uuid.NewString()

	cuentaEmisor := env.createFundedAccount(t, emisor, "1000.00")
	cuentaReceptor := env.createFundedAccount(t, receptor, "")
	env.provider.responses[0].ToolCalls[0].Arguments["toAccountNumber"] = cuentaReceptor.AccountNumber

	ctx := context.Background()
	reply, err := env.service.Send(ctx, emisor, "Transferí 300")
	require.NoError(t, err)
	require.NotNil(t, reply.PendingOperation)

	require.NoError(t, env.service.Reject(ctx, emisor, reply.PendingOperation.TransferID))

	// Todo vuelve al estado anterior.
	saldoEmisor, err := env.accounts.GetBalance(ctx, emisor, cuentaEmisor.ID)
	require.NoError(t, err)
	assert.Equal(t, "1000.00", saldoEmisor.Available.String(), "los fondos se liberaron")
	assert.Equal(t, "0.00", saldoEmisor.Pending.String())

	saldoReceptor, err := env.accounts.GetBalance(ctx, receptor, cuentaReceptor.ID)
	require.NoError(t, err)
	assert.Equal(t, "0.00", saldoReceptor.Available.String(), "nunca recibió nada")
}

func TestNoSePuedeConfirmarDosVeces(t *testing.T) {
	env := newTestEnv(t,
		ports.CompletionResponse{
			ToolCalls: []ports.ToolCall{{
				ID:        "call-1",
				Name:      "transfer",
				Arguments: map[string]any{"toAccountNumber": "", "amount": "100.00"},
			}},
		},
		ports.CompletionResponse{Content: "Operación preparada."},
	)

	emisor := uuid.NewString()
	receptor := uuid.NewString()

	env.createFundedAccount(t, emisor, "1000.00")
	cuentaReceptor := env.createFundedAccount(t, receptor, "")
	env.provider.responses[0].ToolCalls[0].Arguments["toAccountNumber"] = cuentaReceptor.AccountNumber

	ctx := context.Background()
	reply, err := env.service.Send(ctx, emisor, "Transferí 100")
	require.NoError(t, err)
	require.NotNil(t, reply.PendingOperation)

	require.NoError(t, env.service.Confirm(ctx, emisor, reply.PendingOperation.TransferID))

	err = env.service.Confirm(ctx, emisor, reply.PendingOperation.TransferID)
	assert.ErrorIs(t, err, domain.ErrTransferResolved,
		"confirmar dos veces no puede duplicar el movimiento")

	// El destinatario recibió el dinero una sola vez.
	saldoReceptor, err := env.accounts.GetBalance(ctx, receptor, cuentaReceptor.ID)
	require.NoError(t, err)
	assert.Equal(t, "100.00", saldoReceptor.Available.String())
}

// Nadie puede confirmar la operación pendiente de otra persona.
//
// Sin esta comprobación, conocer el identificador de una operación bastaría
// para aprobarla en nombre ajeno.
func TestNoSePuedeConfirmarLaOperacionDeOtroUsuario(t *testing.T) {
	env := newTestEnv(t,
		ports.CompletionResponse{
			ToolCalls: []ports.ToolCall{{
				ID:        "call-1",
				Name:      "transfer",
				Arguments: map[string]any{"toAccountNumber": "", "amount": "100.00"},
			}},
		},
		ports.CompletionResponse{Content: "Operación preparada."},
	)

	emisor := uuid.NewString()
	receptor := uuid.NewString()
	atacante := uuid.NewString()

	env.createFundedAccount(t, emisor, "1000.00")
	cuentaReceptor := env.createFundedAccount(t, receptor, "")
	env.provider.responses[0].ToolCalls[0].Arguments["toAccountNumber"] = cuentaReceptor.AccountNumber

	ctx := context.Background()
	reply, err := env.service.Send(ctx, emisor, "Transferí 100")
	require.NoError(t, err)
	require.NotNil(t, reply.PendingOperation)

	err = env.service.Confirm(ctx, atacante, reply.PendingOperation.TransferID)
	assert.Error(t, err, "un tercero no puede confirmar una operación ajena")

	err = env.service.Reject(ctx, atacante, reply.PendingOperation.TransferID)
	assert.Error(t, err, "un tercero no puede rechazar una operación ajena")
}

// Un retiro sin fondos no debe generar una propuesta: la herramienta avisa
// antes, para que la persona no confirme algo destinado a fallar.
func TestElRetiroSinFondosNoGeneraPropuesta(t *testing.T) {
	env := newTestEnv(t,
		ports.CompletionResponse{
			ToolCalls: []ports.ToolCall{{
				ID:        "call-1",
				Name:      "withdraw",
				Arguments: map[string]any{"amount": "5000.00"},
			}},
		},
		ports.CompletionResponse{Content: "No tenés fondos suficientes."},
	)

	userID := uuid.NewString()
	cuenta := env.createFundedAccount(t, userID, "100.00")

	ctx := context.Background()
	reply, err := env.service.Send(ctx, userID, "Retirá 5000")
	require.NoError(t, err)

	assert.Nil(t, reply.PendingOperation, "sin fondos no puede haber propuesta")

	// El saldo queda intacto y sin reservas.
	balance, err := env.accounts.GetBalance(ctx, userID, cuenta.ID)
	require.NoError(t, err)
	assert.Equal(t, "100.00", balance.Available.String())
	assert.Equal(t, "0.00", balance.Pending.String())
}

// ------------------------------------------------------------------------------
// Robustez
// ------------------------------------------------------------------------------

// Si el proveedor de IA falla, el mensaje del usuario no se pierde.
func TestElMensajeDelUsuarioSeGuardaAunqueFalleElProveedor(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	repo := newFakeAccountRepo()
	ledger := newFakeLedger()
	accounts := banking.NewAccountService(repo, ledger, log)
	transactions := banking.NewTransactionService(repo, ledger, newFakeMetadataRepo(), log)
	messages := newFakeMessageRepo()

	service := NewService(
		failingProvider{},
		mcp.NewServer(accounts, transactions, log),
		messages,
		transactions,
		accounts,
		log,
	)

	userID := uuid.NewString()
	ctx := context.Background()

	_, err := service.Send(ctx, userID, "¿Cuánto tengo?")
	assert.Error(t, err, "el fallo del proveedor debe propagarse")

	// El mensaje quedó guardado igual: la persona no pierde lo que escribió.
	stored, err := messages.ListRecent(ctx, userID, 10)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	assert.Equal(t, "¿Cuánto tengo?", stored[0].Content)
	assert.Equal(t, domain.ChatRoleUser, stored[0].Role)
}

// El bucle de herramientas tiene un límite.
//
// Un modelo que se confunde podría invocar herramientas indefinidamente y
// consumir la cuota sin avanzar.
func TestElBucleDeHerramientasTieneLimite(t *testing.T) {
	// Más respuestas con herramientas que vueltas permitidas.
	responses := make([]ports.CompletionResponse, maxToolRounds+3)
	for i := range responses {
		responses[i] = ports.CompletionResponse{
			ToolCalls: []ports.ToolCall{{
				ID:        "call",
				Name:      "get_balance",
				Arguments: map[string]any{},
			}},
		}
	}

	env := newTestEnv(t, responses...)

	userID := uuid.NewString()
	env.createFundedAccount(t, userID, "100.00")

	reply, err := env.service.Send(context.Background(), userID, "Consultá el saldo")
	require.NoError(t, err)

	// Se corta con un mensaje útil en vez de colgarse.
	assert.NotEmpty(t, reply.Message.Content)
	assert.LessOrEqual(t, env.provider.calls, maxToolRounds,
		"no puede superar el límite de vueltas")
}

func TestElHistorialDevuelveLaConversacion(t *testing.T) {
	env := newTestEnv(t,
		ports.CompletionResponse{Content: "Primera respuesta."},
		ports.CompletionResponse{Content: "Segunda respuesta."},
	)

	userID := uuid.NewString()
	env.createFundedAccount(t, userID, "")
	ctx := context.Background()

	_, err := env.service.Send(ctx, userID, "Primer mensaje")
	require.NoError(t, err)
	_, err = env.service.Send(ctx, userID, "Segundo mensaje")
	require.NoError(t, err)

	history, err := env.service.History(ctx, userID, 50)
	require.NoError(t, err)

	// Dos intercambios: cuatro mensajes en total.
	require.Len(t, history, 4)

	// En orden cronológico, del más antiguo al más reciente.
	assert.Equal(t, "Primer mensaje", history[0].Content)
	assert.Equal(t, domain.ChatRoleUser, history[0].Role)
	assert.Equal(t, "Primera respuesta.", history[1].Content)
	assert.Equal(t, domain.ChatRoleAssistant, history[1].Role)
	assert.Equal(t, "Segundo mensaje", history[2].Content)
	assert.Equal(t, "Segunda respuesta.", history[3].Content)
}

// La conversación de cada persona es privada.
func TestElHistorialEsPrivadoPorUsuario(t *testing.T) {
	env := newTestEnv(t,
		ports.CompletionResponse{Content: "Respuesta para Isabel."},
	)

	isabel := uuid.NewString()
	miguel := uuid.NewString()

	env.createFundedAccount(t, isabel, "")
	ctx := context.Background()

	_, err := env.service.Send(ctx, isabel, "Mi consulta privada")
	require.NoError(t, err)

	// Miguel no ve nada de la conversación de Isabel.
	history, err := env.service.History(ctx, miguel, 50)
	require.NoError(t, err)
	assert.Empty(t, history, "la conversación de cada persona es privada")
}
