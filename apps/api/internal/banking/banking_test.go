package banking

import (
	"context"
	"io"
	"log/slog"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// ------------------------------------------------------------------------------
// Dobles en memoria
//
// El ledger falso lleva contabilidad de verdad: aplica la regla de fondos
// insuficientes y el ciclo de dos fases. Así se puede verificar la lógica del
// servicio sin levantar TigerBeetle, y la suite corre en milisegundos.
// ------------------------------------------------------------------------------

type fakeAccountRepo struct {
	mu       sync.Mutex
	accounts map[string]domain.Account
	counter  int
}

func newFakeAccountRepo() *fakeAccountRepo {
	return &fakeAccountRepo{accounts: make(map[string]domain.Account)}
}

func (f *fakeAccountRepo) Create(_ context.Context, account domain.Account) (domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	account.ID = uuid.NewString()
	account.CreatedAt = time.Now()
	f.accounts[account.ID] = account
	return account, nil
}

func (f *fakeAccountRepo) FindByID(_ context.Context, id string) (domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	account, ok := f.accounts[id]
	if !ok {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	return account, nil
}

func (f *fakeAccountRepo) FindByNumber(_ context.Context, number string) (domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, account := range f.accounts {
		if account.AccountNumber == number {
			return account, nil
		}
	}
	return domain.Account{}, domain.ErrAccountNotFound
}

func (f *fakeAccountRepo) ListByUser(_ context.Context, userID string) ([]domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []domain.Account
	for _, account := range f.accounts {
		if account.UserID == userID {
			out = append(out, account)
		}
	}
	return out, nil
}

func (f *fakeAccountRepo) NextAccountNumber(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.counter++
	return "4001-0000-0000-" + pad4(f.counter), nil
}

func (f *fakeAccountRepo) NumbersByTigerBeetleIDs(_ context.Context, ids []*big.Int) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make(map[string]string, len(ids))
	for _, id := range ids {
		for _, account := range f.accounts {
			if account.TigerBeetleID != nil && account.TigerBeetleID.Cmp(id) == 0 {
				out[id.String()] = account.AccountNumber
			}
		}
	}
	return out, nil
}

func pad4(n int) string {
	digits := []byte{
		byte('0' + (n/1000)%10),
		byte('0' + (n/100)%10),
		byte('0' + (n/10)%10),
		byte('0' + n%10),
	}
	return string(digits)
}

// fakeLedger reproduce el comportamiento contable de TigerBeetle.
type fakeLedger struct {
	mu       sync.Mutex
	balances map[string]*ledgerAccount
	pending  map[string]*pendingTransfer
	history  map[string][]domain.Transaction
	nextID   int64
}

type ledgerAccount struct {
	creditsPosted domain.Money
	debitsPosted  domain.Money
	debitsPending domain.Money
	// Las cuentas de cliente no pueden quedar en negativo; la del operador sí.
	restrictOverdraft bool
}

type pendingTransfer struct {
	from     string
	to       string
	amount   domain.Money
	resolved bool
}

func newFakeLedger() *fakeLedger {
	l := &fakeLedger{
		balances: make(map[string]*ledgerAccount),
		pending:  make(map[string]*pendingTransfer),
		history:  make(map[string][]domain.Transaction),
		nextID:   1000,
	}

	// La cuenta del operador puede quedar en negativo: su saldo representa el
	// pasivo del banco frente a los clientes.
	l.balances[operatorID().String()] = &ledgerAccount{restrictOverdraft: false}
	return l
}

func (f *fakeLedger) CreateAccount(_ context.Context, id *big.Int, _ domain.AccountType) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := id.String()
	if _, exists := f.balances[key]; exists {
		return domain.ErrAccountAlreadyExists
	}
	f.balances[key] = &ledgerAccount{restrictOverdraft: true}
	return nil
}

func (f *fakeLedger) available(acc *ledgerAccount) domain.Money {
	return domain.CalculateAvailable(acc.creditsPosted, acc.debitsPosted, acc.debitsPending)
}

func (f *fakeLedger) GetBalance(_ context.Context, id *big.Int) (domain.Balance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	acc, ok := f.balances[id.String()]
	if !ok {
		return domain.Balance{}, domain.ErrAccountNotFound
	}

	return domain.Balance{
		Available: f.available(acc),
		Posted:    acc.creditsPosted - acc.debitsPosted,
		Pending:   acc.debitsPending,
		Currency:  "USD",
	}, nil
}

func (f *fakeLedger) GetBalances(_ context.Context, ids []*big.Int) (map[string]domain.Balance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make(map[string]domain.Balance, len(ids))
	for _, id := range ids {
		if acc, ok := f.balances[id.String()]; ok {
			out[id.String()] = domain.Balance{
				Available: f.available(acc),
				Posted:    acc.creditsPosted - acc.debitsPosted,
				Pending:   acc.debitsPending,
				Currency:  "USD",
			}
		}
	}
	return out, nil
}

func (f *fakeLedger) Transfer(_ context.Context, req domain.TransferRequest) (*big.Int, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	fromKey := req.FromTigerBeetleID.String()
	toKey := req.ToTigerBeetleID.String()

	from, ok := f.balances[fromKey]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	to, ok := f.balances[toKey]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}

	// La regla que en TigerBeetle impone debits_must_not_exceed_credits.
	if from.restrictOverdraft && f.available(from) < req.Amount {
		return nil, domain.ErrInsufficientFunds
	}

	f.nextID++
	id := big.NewInt(f.nextID)

	if req.Pending {
		// Reserva: el disponible baja, pero el dinero todavía no se movió.
		from.debitsPending += req.Amount
		f.pending[id.String()] = &pendingTransfer{from: fromKey, to: toKey, amount: req.Amount}
	} else {
		from.debitsPosted += req.Amount
		to.creditsPosted += req.Amount
	}

	tx := domain.Transaction{
		ID:        id,
		Type:      req.Type,
		Amount:    req.Amount,
		Timestamp: time.Now(),
		Status:    domain.TransactionStatusCompleted,
	}
	if req.Pending {
		tx.Status = domain.TransactionStatusPending
	}

	// Cada lado ve como contraparte al opuesto, igual que en TigerBeetle.
	outgoing := tx
	outgoing.Direction = domain.DirectionOut
	outgoing.CounterpartyID = req.ToTigerBeetleID
	f.history[fromKey] = append([]domain.Transaction{outgoing}, f.history[fromKey]...)

	incoming := tx
	incoming.Direction = domain.DirectionIn
	incoming.CounterpartyID = req.FromTigerBeetleID
	f.history[toKey] = append([]domain.Transaction{incoming}, f.history[toKey]...)

	return id, nil
}

func (f *fakeLedger) PostPending(_ context.Context, pendingID *big.Int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	p, ok := f.pending[pendingID.String()]
	if !ok {
		return domain.ErrTransferNotFound
	}
	if p.resolved {
		return domain.ErrTransferResolved
	}

	// La reserva se convierte en movimiento definitivo.
	f.balances[p.from].debitsPending -= p.amount
	f.balances[p.from].debitsPosted += p.amount
	f.balances[p.to].creditsPosted += p.amount
	p.resolved = true

	return nil
}

func (f *fakeLedger) VoidPending(_ context.Context, pendingID *big.Int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	p, ok := f.pending[pendingID.String()]
	if !ok {
		return domain.ErrTransferNotFound
	}
	if p.resolved {
		return domain.ErrTransferResolved
	}

	// Se libera la reserva sin mover dinero.
	f.balances[p.from].debitsPending -= p.amount
	p.resolved = true

	return nil
}

func (f *fakeLedger) ListTransfers(_ context.Context, id *big.Int, filter ports.TransferFilter) ([]domain.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	all := f.history[id.String()]

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	// El adaptador real devuelve uno de más para detectar si hay página siguiente.
	if len(all) > limit+1 {
		all = all[:limit+1]
	}

	out := make([]domain.Transaction, len(all))
	copy(out, all)
	return out, nil
}

func (f *fakeLedger) LookupTransfer(_ context.Context, id *big.Int) (domain.Transaction, error) {
	return domain.Transaction{}, domain.ErrTransferNotFound
}

func (f *fakeLedger) Close() error { return nil }

type fakeMetadataRepo struct {
	mu           sync.Mutex
	descriptions map[string]string
}

func newFakeMetadataRepo() *fakeMetadataRepo {
	return &fakeMetadataRepo{descriptions: make(map[string]string)}
}

func (f *fakeMetadataRepo) Store(_ context.Context, transferID *big.Int, description string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.descriptions[transferID.String()] = description
	return nil
}

func (f *fakeMetadataRepo) GetMany(_ context.Context, ids []*big.Int) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if desc, ok := f.descriptions[id.String()]; ok {
			out[id.String()] = desc
		}
	}
	return out, nil
}

// ------------------------------------------------------------------------------
// Preparación
// ------------------------------------------------------------------------------

type testEnv struct {
	accounts     *AccountService
	transactions *TransactionService
	ledger       *fakeLedger
	repo         *fakeAccountRepo
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := newFakeAccountRepo()
	ledger := newFakeLedger()
	metadata := newFakeMetadataRepo()

	return &testEnv{
		accounts:     NewAccountService(repo, ledger, log),
		transactions: NewTransactionService(repo, ledger, metadata, log),
		ledger:       ledger,
		repo:         repo,
	}
}

// createAccount da de alta una cuenta en el repositorio y en el libro contable.
func (e *testEnv) createAccount(t *testing.T, userID string) domain.Account {
	t.Helper()
	ctx := context.Background()

	number, err := e.repo.NextAccountNumber(ctx)
	require.NoError(t, err)

	tigerBeetleID := big.NewInt(time.Now().UnixNano() + int64(len(e.repo.accounts)))
	require.NoError(t, e.ledger.CreateAccount(ctx, tigerBeetleID, domain.AccountTypeChecking))

	account, err := e.repo.Create(ctx, domain.Account{
		UserID:        userID,
		AccountNumber: number,
		TigerBeetleID: tigerBeetleID,
		Type:          domain.AccountTypeChecking,
		Currency:      "USD",
	})
	require.NoError(t, err)

	return account
}

// fund deposita dinero en una cuenta para preparar un escenario.
func (e *testEnv) fund(t *testing.T, userID string, account domain.Account, amount string) {
	t.Helper()

	money, err := domain.ParseMoney(amount)
	require.NoError(t, err)

	_, err = e.transactions.Deposit(context.Background(), DepositInput{
		UserID:    userID,
		AccountID: account.ID,
		Amount:    money,
	})
	require.NoError(t, err)
}

func money(t *testing.T, s string) domain.Money {
	t.Helper()
	m, err := domain.ParseMoney(s)
	require.NoError(t, err)
	return m
}

// ------------------------------------------------------------------------------
// Depósitos
// ------------------------------------------------------------------------------

func TestDepositoAumentaElSaldo(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	userID := uuid.NewString()

	account := env.createAccount(t, userID)

	tx, err := env.transactions.Deposit(ctx, DepositInput{
		UserID:      userID,
		AccountID:   account.ID,
		Amount:      money(t, "1000.00"),
		Description: "Depósito inicial",
	})
	require.NoError(t, err)

	assert.Equal(t, domain.TransactionTypeDeposit, tx.Type)
	assert.Equal(t, domain.DirectionIn, tx.Direction)
	assert.Equal(t, domain.ExternalAccountNumber, tx.FromAccount,
		"un depósito viene de fuera del banco")

	balance, err := env.accounts.GetBalance(ctx, userID, account.ID)
	require.NoError(t, err)
	assert.Equal(t, "1000.00", balance.Available.String())
}

func TestDepositoRechazaMontosInvalidos(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	userID := uuid.NewString()
	account := env.createAccount(t, userID)

	casos := []struct {
		nombre   string
		amount   domain.Money
		esperado error
	}{
		{"cero", 0, domain.ErrAmountNotPositive},
		{"negativo", -100, domain.ErrAmountNotPositive},
		{"excede el máximo", domain.MaxAmount + 1, domain.ErrAmountTooLarge},
	}

	for _, tc := range casos {
		t.Run(tc.nombre, func(t *testing.T) {
			_, err := env.transactions.Deposit(ctx, DepositInput{
				UserID:    userID,
				AccountID: account.ID,
				Amount:    tc.amount,
			})
			assert.ErrorIs(t, err, tc.esperado)
		})
	}
}

// ------------------------------------------------------------------------------
// Retiros
// ------------------------------------------------------------------------------

func TestRetiroDisminuyeElSaldo(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	userID := uuid.NewString()

	account := env.createAccount(t, userID)
	env.fund(t, userID, account, "1000.00")

	tx, err := env.transactions.Withdraw(ctx, WithdrawInput{
		UserID:      userID,
		AccountID:   account.ID,
		Amount:      money(t, "250.00"),
		Description: "Retiro en cajero",
	})
	require.NoError(t, err)

	assert.Equal(t, domain.TransactionTypeWithdrawal, tx.Type)
	assert.Equal(t, domain.DirectionOut, tx.Direction)

	balance, err := env.accounts.GetBalance(ctx, userID, account.ID)
	require.NoError(t, err)
	assert.Equal(t, "750.00", balance.Available.String())
}

// La prueba pide explícitamente "validar disponibilidad" en los retiros.
func TestRetiroSinFondosSeRechaza(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	userID := uuid.NewString()

	account := env.createAccount(t, userID)
	env.fund(t, userID, account, "100.00")

	_, err := env.transactions.Withdraw(ctx, WithdrawInput{
		UserID:    userID,
		AccountID: account.ID,
		Amount:    money(t, "500.00"),
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientFunds)

	// El saldo tiene que quedar intacto tras el rechazo.
	balance, err := env.accounts.GetBalance(ctx, userID, account.ID)
	require.NoError(t, err)
	assert.Equal(t, "100.00", balance.Available.String())
}

// ------------------------------------------------------------------------------
// Transferencias
// ------------------------------------------------------------------------------

func TestTransferenciaEntreUsuarios(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	emisor := uuid.NewString()
	receptor := uuid.NewString()

	cuentaEmisor := env.createAccount(t, emisor)
	cuentaReceptor := env.createAccount(t, receptor)
	env.fund(t, emisor, cuentaEmisor, "1000.00")

	tx, err := env.transactions.Transfer(ctx, TransferInput{
		UserID:          emisor,
		FromAccountID:   cuentaEmisor.ID,
		ToAccountNumber: cuentaReceptor.AccountNumber,
		Amount:          money(t, "300.00"),
		Description:     "Pago de alquiler",
	})
	require.NoError(t, err)

	assert.Equal(t, domain.TransactionTypeTransfer, tx.Type)
	assert.Equal(t, cuentaReceptor.AccountNumber, tx.ToAccount)

	saldoEmisor, err := env.accounts.GetBalance(ctx, emisor, cuentaEmisor.ID)
	require.NoError(t, err)
	assert.Equal(t, "700.00", saldoEmisor.Available.String())

	saldoReceptor, err := env.accounts.GetBalance(ctx, receptor, cuentaReceptor.ID)
	require.NoError(t, err)
	assert.Equal(t, "300.00", saldoReceptor.Available.String())
}

// Entre cuentas del mismo usuario se registra como movimiento interno, que es
// la distinción que hacen los datos de prueba.
func TestTransferenciaEntreCuentasPropiasEsInterna(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	userID := uuid.NewString()

	origen := env.createAccount(t, userID)
	destino := env.createAccount(t, userID)
	env.fund(t, userID, origen, "500.00")

	tx, err := env.transactions.Transfer(ctx, TransferInput{
		UserID:          userID,
		FromAccountID:   origen.ID,
		ToAccountNumber: destino.AccountNumber,
		Amount:          money(t, "200.00"),
	})
	require.NoError(t, err)

	assert.Equal(t, domain.TransactionTypeInternalTransfer, tx.Type)
}

func TestTransferenciaValidaLaCuentaDestino(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	userID := uuid.NewString()

	account := env.createAccount(t, userID)
	env.fund(t, userID, account, "500.00")

	t.Run("cuenta inexistente", func(t *testing.T) {
		_, err := env.transactions.Transfer(ctx, TransferInput{
			UserID:          userID,
			FromAccountID:   account.ID,
			ToAccountNumber: "4001-9999-9999-9999",
			Amount:          money(t, "100.00"),
		})
		assert.ErrorIs(t, err, domain.ErrAccountNotFound)
	})

	t.Run("formato inválido", func(t *testing.T) {
		_, err := env.transactions.Transfer(ctx, TransferInput{
			UserID:          userID,
			FromAccountID:   account.ID,
			ToAccountNumber: "no-es-una-cuenta",
			Amount:          money(t, "100.00"),
		})
		assert.ErrorIs(t, err, domain.ErrAccountNotFound)
	})

	t.Run("a la misma cuenta", func(t *testing.T) {
		_, err := env.transactions.Transfer(ctx, TransferInput{
			UserID:          userID,
			FromAccountID:   account.ID,
			ToAccountNumber: account.AccountNumber,
			Amount:          money(t, "100.00"),
		})
		assert.ErrorIs(t, err, domain.ErrSameAccount)
	})
}

func TestTransferenciaSinFondosSeRechaza(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	emisor := uuid.NewString()
	receptor := uuid.NewString()

	cuentaEmisor := env.createAccount(t, emisor)
	cuentaReceptor := env.createAccount(t, receptor)
	env.fund(t, emisor, cuentaEmisor, "50.00")

	_, err := env.transactions.Transfer(ctx, TransferInput{
		UserID:          emisor,
		FromAccountID:   cuentaEmisor.ID,
		ToAccountNumber: cuentaReceptor.AccountNumber,
		Amount:          money(t, "500.00"),
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientFunds)

	// Nada se movió en ninguna de las dos cuentas.
	saldoReceptor, err := env.accounts.GetBalance(ctx, receptor, cuentaReceptor.ID)
	require.NoError(t, err)
	assert.Equal(t, "0.00", saldoReceptor.Available.String())
}

// ------------------------------------------------------------------------------
// Aislamiento entre usuarios
//
// Estos tests cubren la vulnerabilidad más común en APIs de este tipo: exponer
// un recurso por id sin verificar quién lo está pidiendo.
// ------------------------------------------------------------------------------

func TestNoSePuedeOperarSobreCuentasAjenas(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	dueño := uuid.NewString()
	atacante := uuid.NewString()

	cuenta := env.createAccount(t, dueño)
	env.fund(t, dueño, cuenta, "1000.00")

	t.Run("no puede ver el saldo", func(t *testing.T) {
		_, err := env.accounts.GetBalance(ctx, atacante, cuenta.ID)
		assert.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("no puede depositar", func(t *testing.T) {
		_, err := env.transactions.Deposit(ctx, DepositInput{
			UserID:    atacante,
			AccountID: cuenta.ID,
			Amount:    money(t, "100.00"),
		})
		assert.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("no puede retirar", func(t *testing.T) {
		_, err := env.transactions.Withdraw(ctx, WithdrawInput{
			UserID:    atacante,
			AccountID: cuenta.ID,
			Amount:    money(t, "100.00"),
		})
		assert.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("no puede transferir desde ella", func(t *testing.T) {
		destino := env.createAccount(t, atacante)
		_, err := env.transactions.Transfer(ctx, TransferInput{
			UserID:          atacante,
			FromAccountID:   cuenta.ID,
			ToAccountNumber: destino.AccountNumber,
			Amount:          money(t, "500.00"),
		})
		assert.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("no puede ver el historial", func(t *testing.T) {
		_, err := env.transactions.History(ctx, HistoryInput{
			UserID:    atacante,
			AccountID: cuenta.ID,
		})
		assert.ErrorIs(t, err, domain.ErrForbidden)
	})

	// El saldo del dueño quedó intacto después de todos los intentos.
	balance, err := env.accounts.GetBalance(ctx, dueño, cuenta.ID)
	require.NoError(t, err)
	assert.Equal(t, "1000.00", balance.Available.String())
}

// ------------------------------------------------------------------------------
// Operaciones en dos fases (las que usa el chat con IA)
// ------------------------------------------------------------------------------

func TestTransferenciaPendienteReservaFondos(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	emisor := uuid.NewString()
	receptor := uuid.NewString()

	cuentaEmisor := env.createAccount(t, emisor)
	cuentaReceptor := env.createAccount(t, receptor)
	env.fund(t, emisor, cuentaEmisor, "1000.00")

	tx, err := env.transactions.Transfer(ctx, TransferInput{
		UserID:          emisor,
		FromAccountID:   cuentaEmisor.ID,
		ToAccountNumber: cuentaReceptor.AccountNumber,
		Amount:          money(t, "300.00"),
		Pending:         true,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.TransactionStatusPending, tx.Status)

	// Los fondos quedan reservados: el disponible baja aunque el dinero no se
	// haya movido. Sin esto se podría gastar dos veces el mismo saldo.
	saldoEmisor, err := env.accounts.GetBalance(ctx, emisor, cuentaEmisor.ID)
	require.NoError(t, err)
	assert.Equal(t, "700.00", saldoEmisor.Available.String())
	assert.Equal(t, "300.00", saldoEmisor.Pending.String())
	assert.Equal(t, "1000.00", saldoEmisor.Posted.String(), "lo liquidado no cambia")

	saldoReceptor, err := env.accounts.GetBalance(ctx, receptor, cuentaReceptor.ID)
	require.NoError(t, err)
	assert.Equal(t, "0.00", saldoReceptor.Available.String(), "el destino no recibe nada aún")
}

func TestConfirmarOperacionPendienteMueveElDinero(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	emisor := uuid.NewString()
	receptor := uuid.NewString()

	cuentaEmisor := env.createAccount(t, emisor)
	cuentaReceptor := env.createAccount(t, receptor)
	env.fund(t, emisor, cuentaEmisor, "1000.00")

	tx, err := env.transactions.Transfer(ctx, TransferInput{
		UserID:          emisor,
		FromAccountID:   cuentaEmisor.ID,
		ToAccountNumber: cuentaReceptor.AccountNumber,
		Amount:          money(t, "300.00"),
		Pending:         true,
	})
	require.NoError(t, err)

	require.NoError(t, env.transactions.ConfirmPending(ctx, emisor, tx.ID))

	saldoEmisor, err := env.accounts.GetBalance(ctx, emisor, cuentaEmisor.ID)
	require.NoError(t, err)
	assert.Equal(t, "700.00", saldoEmisor.Available.String())
	assert.Equal(t, "0.00", saldoEmisor.Pending.String(), "ya no hay reserva")

	saldoReceptor, err := env.accounts.GetBalance(ctx, receptor, cuentaReceptor.ID)
	require.NoError(t, err)
	assert.Equal(t, "300.00", saldoReceptor.Available.String())
}

func TestRechazarOperacionPendienteLiberaLosFondos(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	emisor := uuid.NewString()
	receptor := uuid.NewString()

	cuentaEmisor := env.createAccount(t, emisor)
	cuentaReceptor := env.createAccount(t, receptor)
	env.fund(t, emisor, cuentaEmisor, "1000.00")

	tx, err := env.transactions.Transfer(ctx, TransferInput{
		UserID:          emisor,
		FromAccountID:   cuentaEmisor.ID,
		ToAccountNumber: cuentaReceptor.AccountNumber,
		Amount:          money(t, "300.00"),
		Pending:         true,
	})
	require.NoError(t, err)

	require.NoError(t, env.transactions.RejectPending(ctx, emisor, tx.ID))

	// Todo vuelve al estado anterior: nada se movió.
	saldoEmisor, err := env.accounts.GetBalance(ctx, emisor, cuentaEmisor.ID)
	require.NoError(t, err)
	assert.Equal(t, "1000.00", saldoEmisor.Available.String())
	assert.Equal(t, "0.00", saldoEmisor.Pending.String())

	saldoReceptor, err := env.accounts.GetBalance(ctx, receptor, cuentaReceptor.ID)
	require.NoError(t, err)
	assert.Equal(t, "0.00", saldoReceptor.Available.String())
}

func TestNoSePuedeConfirmarDosVeces(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	emisor := uuid.NewString()
	receptor := uuid.NewString()

	cuentaEmisor := env.createAccount(t, emisor)
	cuentaReceptor := env.createAccount(t, receptor)
	env.fund(t, emisor, cuentaEmisor, "1000.00")

	tx, err := env.transactions.Transfer(ctx, TransferInput{
		UserID:          emisor,
		FromAccountID:   cuentaEmisor.ID,
		ToAccountNumber: cuentaReceptor.AccountNumber,
		Amount:          money(t, "300.00"),
		Pending:         true,
	})
	require.NoError(t, err)

	require.NoError(t, env.transactions.ConfirmPending(ctx, emisor, tx.ID))

	// Reintentar no puede duplicar el movimiento.
	err = env.transactions.ConfirmPending(ctx, emisor, tx.ID)
	assert.ErrorIs(t, err, domain.ErrTransferResolved)

	saldoReceptor, err := env.accounts.GetBalance(ctx, receptor, cuentaReceptor.ID)
	require.NoError(t, err)
	assert.Equal(t, "300.00", saldoReceptor.Available.String(),
		"el destino debe recibir el dinero una sola vez")
}

// ------------------------------------------------------------------------------
// Historial y exportación
// ------------------------------------------------------------------------------

func TestHistorialIncluyeLasDescripciones(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	userID := uuid.NewString()

	account := env.createAccount(t, userID)

	_, err := env.transactions.Deposit(ctx, DepositInput{
		UserID:      userID,
		AccountID:   account.ID,
		Amount:      money(t, "1000.00"),
		Description: "Regalo familiar",
	})
	require.NoError(t, err)

	page, err := env.transactions.History(ctx, HistoryInput{
		UserID:    userID,
		AccountID: account.ID,
	})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)

	// La descripción vive en Postgres, no en TigerBeetle: este test verifica
	// que ambas fuentes se combinan correctamente.
	assert.Equal(t, "Regalo familiar", page.Items[0].Description)
	assert.Equal(t, domain.ExternalAccountNumber, page.Items[0].FromAccount)
	assert.Equal(t, account.AccountNumber, page.Items[0].ToAccount)
}

// El historial debe mostrar el número de cuenta de la otra parte.
//
// TigerBeetle sólo guarda ids numéricos, así que el número legible hay que
// resolverlo contra Postgres. Sin eso, una transferencia aparecía con el
// destinatario vacío.
func TestHistorialResuelveLaCuentaDeContraparte(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	emisor := uuid.NewString()
	receptor := uuid.NewString()

	cuentaEmisor := env.createAccount(t, emisor)
	cuentaReceptor := env.createAccount(t, receptor)
	env.fund(t, emisor, cuentaEmisor, "1000.00")

	_, err := env.transactions.Transfer(ctx, TransferInput{
		UserID:          emisor,
		FromAccountID:   cuentaEmisor.ID,
		ToAccountNumber: cuentaReceptor.AccountNumber,
		Amount:          money(t, "300.00"),
		Description:     "Pago de alquiler",
	})
	require.NoError(t, err)

	// Visto desde el emisor: la transferencia sale hacia la cuenta del receptor.
	pageEmisor, err := env.transactions.History(ctx, HistoryInput{
		UserID:    emisor,
		AccountID: cuentaEmisor.ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, pageEmisor.Items)

	salida := pageEmisor.Items[0]
	assert.Equal(t, cuentaEmisor.AccountNumber, salida.FromAccount)
	assert.Equal(t, cuentaReceptor.AccountNumber, salida.ToAccount,
		"el destinatario no puede quedar vacío")

	// Visto desde el receptor: la misma transferencia entra desde el emisor.
	pageReceptor, err := env.transactions.History(ctx, HistoryInput{
		UserID:    receptor,
		AccountID: cuentaReceptor.ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, pageReceptor.Items)

	entrada := pageReceptor.Items[0]
	assert.Equal(t, cuentaEmisor.AccountNumber, entrada.FromAccount,
		"el remitente no puede quedar vacío")
	assert.Equal(t, cuentaReceptor.AccountNumber, entrada.ToAccount)
	assert.Equal(t, domain.DirectionIn, entrada.Direction,
		"para quien recibe, el dinero entra")
}

func TestExportarCSV(t *testing.T) {
	transactions := []domain.Transaction{
		{
			ID:          big.NewInt(1),
			Type:        domain.TransactionTypeDeposit,
			Status:      domain.TransactionStatusCompleted,
			Amount:      money(t, "1000.00"),
			Currency:    "USD",
			FromAccount: domain.ExternalAccountNumber,
			ToAccount:   "4001-1234-5678-9012",
			Description: "Depósito de Isabel Hernández",
			Direction:   domain.DirectionIn,
			Timestamp:   time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC),
		},
		{
			ID:          big.NewInt(2),
			Type:        domain.TransactionTypeWithdrawal,
			Status:      domain.TransactionStatusCompleted,
			Amount:      money(t, "250.50"),
			Currency:    "USD",
			FromAccount: "4001-1234-5678-9012",
			ToAccount:   domain.ExternalAccountNumber,
			Description: "Pago de gimnasio",
			Direction:   domain.DirectionOut,
			Timestamp:   time.Date(2024, 3, 16, 14, 0, 0, 0, time.UTC),
		},
	}

	data, err := ExportCSV(transactions, "4001-1234-5678-9012")
	require.NoError(t, err)

	content := string(data)

	// El BOM hace que Excel en Windows lea el archivo como UTF-8. Sin él,
	// "Hernández" aparecería como "HernÃ¡ndez".
	assert.True(t, len(data) > 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF,
		"el CSV debe empezar con el BOM UTF-8")

	assert.Contains(t, content, "Fecha")
	assert.Contains(t, content, "Depósito de Isabel Hernández", "los acentos deben conservarse")
	assert.Contains(t, content, "1000.00")

	// Los retiros llevan signo negativo para poder sumar la columna.
	assert.Contains(t, content, "-250.50")
}

func TestExportarPDF(t *testing.T) {
	transactions := []domain.Transaction{
		{
			ID:          big.NewInt(1),
			Type:        domain.TransactionTypeDeposit,
			Status:      domain.TransactionStatusCompleted,
			Amount:      money(t, "1000.00"),
			Currency:    "USD",
			Description: "Depósito con acentos: Muñoz",
			Direction:   domain.DirectionIn,
			Timestamp:   time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC),
		},
	}

	data, err := ExportPDF(transactions, "4001-1234-5678-9012", time.Now())
	require.NoError(t, err)

	content := string(data)

	// Estructura mínima que exige el formato.
	assert.True(t, len(data) > 4 && content[:4] == "%PDF", "debe empezar con la cabecera PDF")
	assert.Contains(t, content, "%%EOF", "debe terminar con el marcador de fin")
	assert.Contains(t, content, "/Type /Catalog")
	assert.Contains(t, content, "xref")
}

func TestExportarHistorialVacioNoFalla(t *testing.T) {
	// Un usuario sin movimientos debe poder exportar igual: recibe un archivo
	// con sólo las cabeceras, no un error.
	csv, err := ExportCSV(nil, "4001-1234-5678-9012")
	require.NoError(t, err)
	assert.Contains(t, string(csv), "Fecha")

	pdf, err := ExportPDF(nil, "4001-1234-5678-9012", time.Now())
	require.NoError(t, err)
	assert.Contains(t, string(pdf), "%PDF")
}

// ------------------------------------------------------------------------------
// Listado de cuentas
// ------------------------------------------------------------------------------

func TestListarCuentasDevuelveLosSaldos(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	userID := uuid.NewString()

	primera := env.createAccount(t, userID)
	env.createAccount(t, userID)
	env.fund(t, userID, primera, "500.00")

	accounts, err := env.accounts.ListByUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, accounts, 2)

	// Los saldos se piden en una sola llamada al libro contable, no una por
	// cuenta: con muchas cuentas eso sería el problema N+1.
	total := domain.Money(0)
	for _, account := range accounts {
		total += account.Balance.Available
	}
	assert.Equal(t, "500.00", total.String())
}

func TestListarCuentasDeUsuarioSinCuentas(t *testing.T) {
	env := newTestEnv(t)

	accounts, err := env.accounts.ListByUser(context.Background(), uuid.NewString())
	require.NoError(t, err)
	assert.Empty(t, accounts, "un usuario sin cuentas devuelve una lista vacía, no un error")
}
