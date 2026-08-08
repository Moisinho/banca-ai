package chat

// Dobles compartidos con el paquete banking.
//
// Se replican acá porque los helpers de test no se exportan entre paquetes en
// Go. El ledger falso aplica las mismas reglas contables que TigerBeetle:
// fondos insuficientes y ciclo de dos fases.

import (
	"context"
	"math/big"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// operatorID devuelve el identificador de la cuenta del operador.
func operatorID() *big.Int {
	return new(big.Int).SetUint64(domain.OperatorAccountID)
}

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
