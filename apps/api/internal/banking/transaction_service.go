package banking

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"strconv"
	"time"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// DefaultPendingTimeout es cuánto vive una operación esperando confirmación
// antes de liberarse sola.
//
// Cinco minutos es tiempo suficiente para que una persona lea lo que la IA
// propuso y decida, sin dejar fondos bloqueados si abandona la conversación.
const DefaultPendingTimeout = 5 * time.Minute

// TransactionService son los casos de uso de movimiento de dinero.
type TransactionService struct {
	accounts ports.AccountRepository
	ledger   ports.Ledger
	metadata ports.TransactionMetadataRepository
	log      *slog.Logger
}

func NewTransactionService(
	accounts ports.AccountRepository,
	ledger ports.Ledger,
	metadata ports.TransactionMetadataRepository,
	log *slog.Logger,
) *TransactionService {
	return &TransactionService{
		accounts: accounts,
		ledger:   ledger,
		metadata: metadata,
		log:      log,
	}
}

// DepositInput son los datos de un depósito.
type DepositInput struct {
	UserID      string
	AccountID   string
	Amount      domain.Money
	Description string
}

// Deposit ingresa dinero desde fuera del banco.
//
// No es "sumarle a una cuenta": es una transferencia desde la cuenta del
// operador hacia la del usuario. En contabilidad de doble entrada el dinero no
// aparece de la nada, siempre sale de algún lado.
func (s *TransactionService) Deposit(ctx context.Context, in DepositInput) (domain.Transaction, error) {
	account, err := s.ownedAccount(ctx, in.UserID, in.AccountID)
	if err != nil {
		return domain.Transaction{}, err
	}

	if err := in.Amount.Validate(); err != nil {
		return domain.Transaction{}, err
	}

	transferID, err := s.ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: operatorID(),
		ToTigerBeetleID:   account.TigerBeetleID,
		Amount:            in.Amount,
		Type:              domain.TransactionTypeDeposit,
	})
	if err != nil {
		return domain.Transaction{}, err
	}

	s.storeDescription(ctx, transferID, in.Description)

	s.log.Info("depósito realizado",
		"user_id", in.UserID,
		"account", account.AccountNumber,
		"amount", in.Amount.String(),
	)

	return domain.Transaction{
		ID:          transferID,
		Type:        domain.TransactionTypeDeposit,
		Status:      domain.TransactionStatusCompleted,
		Amount:      in.Amount,
		Currency:    account.Currency,
		FromAccount: domain.ExternalAccountNumber,
		ToAccount:   account.AccountNumber,
		Description: in.Description,
		Direction:   domain.DirectionIn,
		Timestamp:   time.Now(),
	}, nil
}

// WithdrawInput son los datos de un retiro.
type WithdrawInput struct {
	UserID      string
	AccountID   string
	Amount      domain.Money
	Description string
}

// Withdraw saca dinero hacia fuera del banco.
//
// La validación de fondos NO se hace acá: la hace TigerBeetle mediante el flag
// debits_must_not_exceed_credits. Un `if saldo < monto` en Go tendría una
// ventana de carrera entre la lectura y la escritura.
func (s *TransactionService) Withdraw(ctx context.Context, in WithdrawInput) (domain.Transaction, error) {
	account, err := s.ownedAccount(ctx, in.UserID, in.AccountID)
	if err != nil {
		return domain.Transaction{}, err
	}

	if err := in.Amount.Validate(); err != nil {
		return domain.Transaction{}, err
	}

	transferID, err := s.ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: account.TigerBeetleID,
		ToTigerBeetleID:   operatorID(),
		Amount:            in.Amount,
		Type:              domain.TransactionTypeWithdrawal,
	})
	if err != nil {
		// Fondos insuficientes es un caso esperable, no un fallo del sistema:
		// se registra como aviso y no como error.
		if errors.Is(err, domain.ErrInsufficientFunds) {
			s.log.Warn("retiro rechazado por fondos insuficientes",
				"user_id", in.UserID,
				"account", account.AccountNumber,
				"amount", in.Amount.String(),
			)
		}
		return domain.Transaction{}, err
	}

	s.storeDescription(ctx, transferID, in.Description)

	s.log.Info("retiro realizado",
		"user_id", in.UserID,
		"account", account.AccountNumber,
		"amount", in.Amount.String(),
	)

	return domain.Transaction{
		ID:          transferID,
		Type:        domain.TransactionTypeWithdrawal,
		Status:      domain.TransactionStatusCompleted,
		Amount:      in.Amount,
		Currency:    account.Currency,
		FromAccount: account.AccountNumber,
		ToAccount:   domain.ExternalAccountNumber,
		Description: in.Description,
		Direction:   domain.DirectionOut,
		Timestamp:   time.Now(),
	}, nil
}

// TransferInput son los datos de una transferencia entre cuentas.
type TransferInput struct {
	UserID          string
	FromAccountID   string
	ToAccountNumber string
	Amount          domain.Money
	Description     string

	// Pending reserva los fondos en lugar de moverlos, a la espera de una
	// confirmación explícita. Es lo que usa el chat con IA: el modelo propone,
	// la persona confirma.
	Pending bool
}

// Transfer envía dinero a otra cuenta.
//
// La cuenta origen tiene que ser del usuario; la destino puede ser de
// cualquiera. Si ambas pertenecen a la misma persona se registra como
// transferencia interna, que es la distinción que hacen los datos de prueba.
func (s *TransactionService) Transfer(ctx context.Context, in TransferInput) (domain.Transaction, error) {
	from, err := s.ownedAccount(ctx, in.UserID, in.FromAccountID)
	if err != nil {
		return domain.Transaction{}, err
	}

	if err := in.Amount.Validate(); err != nil {
		return domain.Transaction{}, err
	}

	if !domain.ValidAccountNumber(in.ToAccountNumber) {
		return domain.Transaction{}, domain.ErrAccountNotFound
	}

	// La prueba pide explícitamente "Validar cuenta destino".
	to, err := s.accounts.FindByNumber(ctx, in.ToAccountNumber)
	if err != nil {
		return domain.Transaction{}, err
	}

	if from.AccountNumber == to.AccountNumber {
		return domain.Transaction{}, domain.ErrSameAccount
	}

	// Entre cuentas propias es un movimiento interno, no una transferencia a
	// terceros. Los datos de prueba distinguen ambos casos.
	transferType := domain.TransactionTypeTransfer
	if to.UserID == in.UserID {
		transferType = domain.TransactionTypeInternalTransfer
	}

	transferID, err := s.ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: from.TigerBeetleID,
		ToTigerBeetleID:   to.TigerBeetleID,
		Amount:            in.Amount,
		Type:              transferType,
		Pending:           in.Pending,
		PendingTimeout:    DefaultPendingTimeout,
	})
	if err != nil {
		if errors.Is(err, domain.ErrInsufficientFunds) {
			s.log.Warn("transferencia rechazada por fondos insuficientes",
				"user_id", in.UserID,
				"from", from.AccountNumber,
				"amount", in.Amount.String(),
			)
		}
		return domain.Transaction{}, err
	}

	s.storeDescription(ctx, transferID, in.Description)

	status := domain.TransactionStatusCompleted
	if in.Pending {
		status = domain.TransactionStatusPending
	}

	s.log.Info("transferencia realizada",
		"user_id", in.UserID,
		"from", from.AccountNumber,
		"to", to.AccountNumber,
		"amount", in.Amount.String(),
		"pending", in.Pending,
	)

	return domain.Transaction{
		ID:          transferID,
		Type:        transferType,
		Status:      status,
		Amount:      in.Amount,
		Currency:    from.Currency,
		FromAccount: from.AccountNumber,
		ToAccount:   to.AccountNumber,
		Description: in.Description,
		Direction:   domain.DirectionOut,
		Timestamp:   time.Now(),
	}, nil
}

// ConfirmPending confirma una operación reservada: el dinero se mueve.
func (s *TransactionService) ConfirmPending(ctx context.Context, userID string, transferID *big.Int) error {
	if err := s.ledger.PostPending(ctx, transferID); err != nil {
		return err
	}

	s.log.Info("operación pendiente confirmada", "user_id", userID, "transfer_id", transferID.String())
	return nil
}

// RejectPending cancela una operación reservada y libera los fondos.
func (s *TransactionService) RejectPending(ctx context.Context, userID string, transferID *big.Int) error {
	if err := s.ledger.VoidPending(ctx, transferID); err != nil {
		return err
	}

	s.log.Info("operación pendiente rechazada", "user_id", userID, "transfer_id", transferID.String())
	return nil
}

// HistoryInput acota una consulta de historial.
type HistoryInput struct {
	UserID    string
	AccountID string
	Limit     int
	Cursor    string
	From      time.Time
	To        time.Time
}

// HistoryPage es una página de resultados.
type HistoryPage struct {
	Items      []domain.Transaction
	NextCursor string
	HasMore    bool
}

// History devuelve los movimientos de una cuenta, más reciente primero.
//
// Usa paginación por cursor y no por offset: con offset, insertar una
// transacción nueva desplaza todo y la página siguiente repetiría o se saltaría
// registros. El cursor apunta a una posición estable en el tiempo.
func (s *TransactionService) History(ctx context.Context, in HistoryInput) (HistoryPage, error) {
	account, err := s.ownedAccount(ctx, in.UserID, in.AccountID)
	if err != nil {
		return HistoryPage{}, err
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}

	var cursor uint64
	if in.Cursor != "" {
		parsed, err := strconv.ParseUint(in.Cursor, 10, 64)
		if err != nil {
			return HistoryPage{}, domain.ErrInvalidCursor
		}
		cursor = parsed
	}

	transactions, err := s.ledger.ListTransfers(ctx, account.TigerBeetleID, ports.TransferFilter{
		Limit:  limit,
		Cursor: cursor,
		From:   in.From,
		To:     in.To,
	})
	if err != nil {
		return HistoryPage{}, err
	}

	// El adaptador pide un registro de más para saber si hay página siguiente
	// sin necesidad de una consulta extra.
	hasMore := len(transactions) > limit
	if hasMore {
		transactions = transactions[:limit]
	}

	s.enrich(ctx, transactions, account)

	var nextCursor string
	if hasMore && len(transactions) > 0 {
		last := transactions[len(transactions)-1]
		// El cursor es el timestamp del último resultado menos uno: la próxima
		// página arranca justo antes, sin repetirlo.
		nextCursor = strconv.FormatUint(uint64(last.Timestamp.UnixNano())-1, 10)
	}

	return HistoryPage{
		Items:      transactions,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

// enrich completa las transacciones con datos que TigerBeetle no guarda:
// las descripciones y los números de cuenta legibles.
func (s *TransactionService) enrich(ctx context.Context, transactions []domain.Transaction, viewpoint domain.Account) {
	if len(transactions) == 0 {
		return
	}

	ids := make([]*big.Int, 0, len(transactions))
	for _, tx := range transactions {
		ids = append(ids, tx.ID)
	}

	descriptions, err := s.metadata.GetMany(ctx, ids)
	if err != nil {
		// Sin descripciones el historial sigue siendo utilizable: son texto
		// complementario, no el dato principal. Se registra y se continúa.
		s.log.Error("no se pudieron cargar las descripciones", "error", err)
		descriptions = map[string]string{}
	}

	for i := range transactions {
		tx := &transactions[i]
		tx.Description = descriptions[tx.ID.String()]
		tx.Currency = viewpoint.Currency

		// TigerBeetle guarda ids numéricos, no números de cuenta. Reconstruimos
		// la vista desde la perspectiva de la cuenta consultada.
		switch tx.Type {
		case domain.TransactionTypeDeposit:
			tx.FromAccount = domain.ExternalAccountNumber
			tx.ToAccount = viewpoint.AccountNumber
		case domain.TransactionTypeWithdrawal:
			tx.FromAccount = viewpoint.AccountNumber
			tx.ToAccount = domain.ExternalAccountNumber
		default:
			// En una transferencia la contraparte es otra cuenta del sistema.
			// Se resuelve más abajo consultando Postgres en lote.
			if tx.Direction == domain.DirectionOut {
				tx.FromAccount = viewpoint.AccountNumber
			} else {
				tx.ToAccount = viewpoint.AccountNumber
			}
		}
	}

	s.resolveCounterparties(ctx, transactions)
}

// resolveCounterparties completa el número de cuenta de la otra parte en las
// transferencias.
//
// TigerBeetle sólo guarda el id numérico de las cuentas, así que el número
// legible ("4001-...") hay que buscarlo en Postgres. Se hace en lote para no
// caer en el problema N+1.
func (s *TransactionService) resolveCounterparties(ctx context.Context, transactions []domain.Transaction) {
	// Junta los ids que faltan resolver, sin repetir.
	pending := make(map[string]*big.Int)
	for i := range transactions {
		tx := &transactions[i]
		if tx.CounterpartyID == nil {
			continue
		}
		if tx.FromAccount == "" || tx.ToAccount == "" {
			pending[tx.CounterpartyID.String()] = tx.CounterpartyID
		}
	}

	if len(pending) == 0 {
		return
	}

	ids := make([]*big.Int, 0, len(pending))
	for _, id := range pending {
		ids = append(ids, id)
	}

	numbers, err := s.accounts.NumbersByTigerBeetleIDs(ctx, ids)
	if err != nil {
		// Sin el número de la contraparte el historial sigue siendo legible:
		// el monto, la fecha y la dirección son lo esencial.
		s.log.Error("no se pudieron resolver las cuentas de contraparte", "error", err)
		return
	}

	for i := range transactions {
		tx := &transactions[i]
		if tx.CounterpartyID == nil {
			continue
		}

		number, ok := numbers[tx.CounterpartyID.String()]
		if !ok {
			// Una cuenta que no está en Postgres es la del operador: el
			// movimiento vino de fuera del banco o fue hacia fuera.
			number = domain.ExternalAccountNumber
		}

		if tx.FromAccount == "" {
			tx.FromAccount = number
		}
		if tx.ToAccount == "" {
			tx.ToAccount = number
		}
	}
}

// ownedAccount busca una cuenta y verifica que pertenezca al usuario.
//
// Es la comprobación que impide que alguien opere sobre cuentas ajenas pasando
// un id que no es suyo.
func (s *TransactionService) ownedAccount(ctx context.Context, userID, accountID string) (domain.Account, error) {
	account, err := s.accounts.FindByID(ctx, accountID)
	if err != nil {
		return domain.Account{}, err
	}

	if account.UserID != userID {
		s.log.Warn("intento de operar sobre una cuenta ajena",
			"user_id", userID,
			"account_id", accountID,
		)
		return domain.Account{}, domain.ErrForbidden
	}

	return account, nil
}

// storeDescription guarda el texto libre de una transacción.
//
// Si falla no se aborta la operación: el dinero ya se movió en TigerBeetle y
// perder una descripción es preferible a dejar el sistema en un estado
// inconsistente.
func (s *TransactionService) storeDescription(ctx context.Context, transferID *big.Int, description string) {
	if description == "" {
		return
	}

	if err := s.metadata.Store(ctx, transferID, description); err != nil {
		s.log.Error("no se pudo guardar la descripción de la transacción",
			"transfer_id", transferID.String(),
			"error", err,
		)
	}
}

// operatorID devuelve el identificador de la cuenta del operador.
func operatorID() *big.Int {
	return new(big.Int).SetUint64(domain.OperatorAccountID)
}
