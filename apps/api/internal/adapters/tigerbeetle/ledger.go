// Package tigerbeetle implementa el puerto Ledger sobre TigerBeetle.
//
// Traduce entre el vocabulario del dominio (cuentas, saldos, transferencias) y
// el modelo contable de doble entrada de TigerBeetle. Todo el conocimiento
// sobre Uint128, flags y códigos de resultado queda encerrado acá.
package tigerbeetle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"time"

	tb "github.com/tigerbeetle/tigerbeetle-go"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// defaultTransferLimit es cuántas transferencias devolver si no se especifica.
const defaultTransferLimit = 50

// maxTransferLimit acota lo que puede pedir un cliente en una sola consulta.
const maxTransferLimit = 200

// Ledger implementa ports.Ledger sobre TigerBeetle.
type Ledger struct {
	client tb.Client
	log    *slog.Logger
}

// Verificación en tiempo de compilación de que cumplimos el contrato.
var _ ports.Ledger = (*Ledger)(nil)

// New conecta con el clúster y devuelve el adaptador.
func New(clusterID uint64, addresses []string, log *slog.Logger) (*Ledger, error) {
	resolved, err := resolveAddresses(addresses)
	if err != nil {
		return nil, err
	}

	client, err := tb.NewClient(tb.ToUint128(clusterID), resolved)
	if err != nil {
		return nil, fmt.Errorf("no se pudo conectar con TigerBeetle en %v: %w", resolved, err)
	}

	log.Info("conectado a TigerBeetle", "addresses", resolved, "cluster_id", clusterID)
	return &Ledger{client: client, log: log}, nil
}

// resolveAddresses traduce nombres de host a direcciones IP.
//
// El cliente de TigerBeetle NO resuelve DNS: espera una IP literal y falla con
// "invalid client cluster address" si recibe un nombre. En docker-compose los
// servicios se referencian por nombre ("tigerbeetle:3000"), así que hay que
// resolverlo acá o el sistema no arranca.
func resolveAddresses(addresses []string) ([]string, error) {
	resolved := make([]string, 0, len(addresses))

	for _, addr := range addresses {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			// Sin puerto explícito: puede ser sólo un puerto ("3000"), que el
			// cliente interpreta como localhost. Se pasa tal cual.
			resolved = append(resolved, addr)
			continue
		}

		// Si ya es una IP, no hay nada que resolver.
		if net.ParseIP(host) != nil {
			resolved = append(resolved, addr)
			continue
		}

		ips, err := net.LookupIP(host)
		if err != nil {
			return nil, fmt.Errorf("no se pudo resolver el host %q de TigerBeetle: %w", host, err)
		}

		// Preferimos IPv4: el cliente no maneja bien las direcciones IPv6.
		var chosen net.IP
		for _, ip := range ips {
			if ipv4 := ip.To4(); ipv4 != nil {
				chosen = ipv4
				break
			}
		}
		if chosen == nil {
			return nil, fmt.Errorf("el host %q de TigerBeetle no tiene una dirección IPv4", host)
		}

		resolved = append(resolved, net.JoinHostPort(chosen.String(), port))
	}

	return resolved, nil
}

// Close libera la conexión.
func (l *Ledger) Close() error {
	l.client.Close()
	return nil
}

// EnsureOperatorAccount crea la cuenta que representa al mundo exterior.
//
// Es el "EXTERNAL" de los datos de prueba: el origen de todo depósito y el
// destino de todo retiro. En contabilidad de doble entrada el dinero no puede
// aparecer de la nada, así que necesitamos una contraparte para el exterior.
//
// A diferencia de las cuentas de usuario, esta NO lleva el flag
// DebitsMustNotExceedCredits: tiene que poder quedar en negativo, porque su
// saldo representa el pasivo del banco frente a sus clientes.
//
// Es idempotente: si la cuenta ya existe, no hace nada.
func (l *Ledger) EnsureOperatorAccount(ctx context.Context) error {
	operatorID := tb.ToUint128(domain.OperatorAccountID)

	existing, err := l.client.LookupAccounts([]tb.Uint128{operatorID})
	if err != nil {
		return fmt.Errorf("no se pudo consultar la cuenta del operador: %w", err)
	}
	if len(existing) > 0 {
		l.log.Debug("la cuenta del operador ya existe")
		return nil
	}

	account := tb.Account{
		ID:     operatorID,
		Ledger: domain.LedgerUSD,
		// Código propio para distinguirla de las cuentas de cliente.
		Code: 999,
		Flags: tb.AccountFlags{
			// History permite consultar el saldo histórico. Es obligatorio para
			// las gráficas de evolución y NO se puede agregar después: las
			// cuentas en TigerBeetle son inmutables.
			History: true,
		}.ToUint16(),
	}

	results, err := l.client.CreateAccounts([]tb.Account{account})
	if err != nil {
		return fmt.Errorf("no se pudo crear la cuenta del operador: %w", err)
	}

	for _, res := range results {
		if res.Status != tb.AccountCreated && res.Status != tb.AccountExists {
			return fmt.Errorf("TigerBeetle rechazó la cuenta del operador: %s", res.Status)
		}
	}

	l.log.Info("cuenta del operador creada", "id", domain.OperatorAccountID)
	return nil
}

// CreateAccount crea una cuenta de cliente en el libro contable.
func (l *Ledger) CreateAccount(ctx context.Context, tigerBeetleID *big.Int, accountType domain.AccountType) error {
	account := tb.Account{
		ID:     tb.BigIntToUint128(tigerBeetleID),
		Ledger: domain.LedgerUSD,
		Code:   accountType.Code(),
		Flags: tb.AccountFlags{
			// La base rechaza cualquier débito que deje la cuenta en negativo.
			//
			// Es la validación real de fondos: no la hacemos con un `if saldo <
			// monto` en Go porque entre esa lectura y la escritura hay una
			// ventana de carrera. Sólo la base puede garantizarlo.
			DebitsMustNotExceedCredits: true,

			// Necesario para consultar saldos históricos. Irreversible.
			History: true,
		}.ToUint16(),
	}

	results, err := l.client.CreateAccounts([]tb.Account{account})
	if err != nil {
		return fmt.Errorf("no se pudo crear la cuenta: %w", err)
	}

	for _, res := range results {
		switch res.Status {
		case tb.AccountCreated:
			// Creada correctamente.
		case tb.AccountExists:
			return domain.ErrAccountAlreadyExists
		default:
			return fmt.Errorf("TigerBeetle rechazó la cuenta: %s", res.Status)
		}
	}

	return nil
}

// GetBalance devuelve el saldo actual de una cuenta.
func (l *Ledger) GetBalance(ctx context.Context, tigerBeetleID *big.Int) (domain.Balance, error) {
	accounts, err := l.client.LookupAccounts([]tb.Uint128{
		tb.BigIntToUint128(tigerBeetleID),
	})
	if err != nil {
		return domain.Balance{}, fmt.Errorf("no se pudo consultar el saldo: %w", err)
	}
	if len(accounts) == 0 {
		return domain.Balance{}, domain.ErrAccountNotFound
	}

	return balanceFromAccount(accounts[0])
}

// GetBalances consulta varias cuentas en una sola llamada.
//
// Evita el problema N+1 al listar las cuentas de un usuario: una petición a la
// base en lugar de una por cuenta.
//
// La clave del mapa es el id de TigerBeetle en decimal, para poder cruzarlo con
// los metadatos que vienen de Postgres.
func (l *Ledger) GetBalances(ctx context.Context, tigerBeetleIDs []*big.Int) (map[string]domain.Balance, error) {
	if len(tigerBeetleIDs) == 0 {
		return map[string]domain.Balance{}, nil
	}

	ids := make([]tb.Uint128, 0, len(tigerBeetleIDs))
	for _, id := range tigerBeetleIDs {
		ids = append(ids, tb.BigIntToUint128(id))
	}

	accounts, err := l.client.LookupAccounts(ids)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron consultar los saldos: %w", err)
	}

	balances := make(map[string]domain.Balance, len(accounts))
	for _, acc := range accounts {
		balance, err := balanceFromAccount(acc)
		if err != nil {
			return nil, err
		}
		balances[acc.ID.BigInt().String()] = balance
	}

	return balances, nil
}

// Transfer ejecuta una transferencia y devuelve su identificador.
//
// Si req.Pending es true, los fondos quedan reservados sin moverse hasta que
// se confirme con PostPending o se libere con VoidPending.
func (l *Ledger) Transfer(ctx context.Context, req domain.TransferRequest) (*big.Int, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	// ID() genera identificadores monotónicamente crecientes en el tiempo.
	// Eso mejora la localidad de escritura en el árbol LSM de TigerBeetle
	// frente a un UUID aleatorio.
	transferID := tb.ID()

	transfer := tb.Transfer{
		ID:              transferID,
		DebitAccountID:  tb.BigIntToUint128(req.FromTigerBeetleID),
		CreditAccountID: tb.BigIntToUint128(req.ToTigerBeetleID),
		Amount:          tb.BytesToUint128(req.Amount.ToUint128Bytes()),
		Ledger:          domain.LedgerUSD,
		Code:            req.Type.Code(),
	}

	if req.Pending {
		transfer.Flags = tb.TransferFlags{Pending: true}.ToUint16()

		// El timeout libera la reserva sola si nadie la resuelve. Sin esto, una
		// propuesta de la IA que el usuario abandona dejaría fondos bloqueados
		// para siempre.
		transfer.Timeout = uint32(req.PendingTimeout.Seconds())
	}

	results, err := l.client.CreateTransfers([]tb.Transfer{transfer})
	if err != nil {
		return nil, fmt.Errorf("no se pudo ejecutar la transferencia: %w", err)
	}

	for _, res := range results {
		if err := translateTransferStatus(res.Status); err != nil {
			return nil, err
		}
	}

	return transferID.BigInt(), nil
}

// PostPending confirma una transferencia pendiente: el dinero se mueve.
func (l *Ledger) PostPending(ctx context.Context, pendingID *big.Int) error {
	transfer := tb.Transfer{
		ID:        tb.ID(),
		PendingID: tb.BigIntToUint128(pendingID),

		// AmountMax significa "confirmar el monto completo de la reserva".
		//
		// Ojo con esto: dejar Amount en cero NO hereda el monto pendiente,
		// confirma CERO y libera la reserva. El resultado se parece a un void
		// y es un error silencioso: nadie recibe el dinero pero la operación
		// aparenta haber salido bien.
		Amount: tb.AmountMax,

		Flags: tb.TransferFlags{PostPendingTransfer: true}.ToUint16(),
	}

	results, err := l.client.CreateTransfers([]tb.Transfer{transfer})
	if err != nil {
		return fmt.Errorf("no se pudo confirmar la transferencia: %w", err)
	}

	for _, res := range results {
		if err := translateTransferStatus(res.Status); err != nil {
			return err
		}
	}

	return nil
}

// VoidPending cancela una transferencia pendiente y libera los fondos.
func (l *Ledger) VoidPending(ctx context.Context, pendingID *big.Int) error {
	transfer := tb.Transfer{
		ID:        tb.ID(),
		PendingID: tb.BigIntToUint128(pendingID),
		Flags:     tb.TransferFlags{VoidPendingTransfer: true}.ToUint16(),
	}

	results, err := l.client.CreateTransfers([]tb.Transfer{transfer})
	if err != nil {
		return fmt.Errorf("no se pudo cancelar la transferencia: %w", err)
	}

	for _, res := range results {
		if err := translateTransferStatus(res.Status); err != nil {
			return err
		}
	}

	return nil
}

// ListTransfers devuelve el historial de una cuenta, más reciente primero.
func (l *Ledger) ListTransfers(ctx context.Context, tigerBeetleID *big.Int, filter ports.TransferFilter) ([]domain.Transaction, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultTransferLimit
	}
	if limit > maxTransferLimit {
		limit = maxTransferLimit
	}

	tbFilter := tb.AccountFilter{
		AccountID: tb.BigIntToUint128(tigerBeetleID),
		// Pedimos uno más que el límite para saber si hay página siguiente
		// sin necesidad de una consulta extra.
		Limit: uint32(limit + 1),
		Flags: tb.AccountFilterFlags{
			// Queremos ambos lados: lo que salió y lo que entró.
			Debits:  true,
			Credits: true,
			// Más reciente primero, que es como se lee un estado de cuenta.
			Reversed: true,
		}.ToUint32(),
	}

	if filter.Cursor > 0 {
		tbFilter.TimestampMax = filter.Cursor
	}
	if !filter.From.IsZero() {
		tbFilter.TimestampMin = uint64(filter.From.UnixNano())
	}
	if !filter.To.IsZero() {
		tbFilter.TimestampMax = uint64(filter.To.UnixNano())
	}

	transfers, err := l.client.GetAccountTransfers(tbFilter)
	if err != nil {
		return nil, fmt.Errorf("no se pudo consultar el historial: %w", err)
	}

	accountID := tb.BigIntToUint128(tigerBeetleID)
	out := make([]domain.Transaction, 0, len(transfers))
	for _, t := range transfers {
		tx, err := transactionFromTransfer(t, accountID)
		if err != nil {
			return nil, err
		}
		out = append(out, tx)
	}

	return out, nil
}

// LookupTransfer busca una transferencia por su identificador.
func (l *Ledger) LookupTransfer(ctx context.Context, transferID *big.Int) (domain.Transaction, error) {
	transfers, err := l.client.LookupTransfers([]tb.Uint128{
		tb.BigIntToUint128(transferID),
	})
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("no se pudo consultar la transferencia: %w", err)
	}
	if len(transfers) == 0 {
		return domain.Transaction{}, domain.ErrTransferNotFound
	}

	// Sin punto de vista de una cuenta concreta, la dirección no aplica.
	return transactionFromTransfer(transfers[0], tb.Uint128{})
}

// ------------------------------------------------------------------------------
// Traducción entre el modelo de TigerBeetle y el del dominio
// ------------------------------------------------------------------------------

// balanceFromAccount calcula el saldo a partir de los contadores de la cuenta.
func balanceFromAccount(acc tb.Account) (domain.Balance, error) {
	creditsPosted, err := domain.MoneyFromBigInt(acc.CreditsPosted.BigInt())
	if err != nil {
		return domain.Balance{}, fmt.Errorf("créditos liquidados fuera de rango: %w", err)
	}
	debitsPosted, err := domain.MoneyFromBigInt(acc.DebitsPosted.BigInt())
	if err != nil {
		return domain.Balance{}, fmt.Errorf("débitos liquidados fuera de rango: %w", err)
	}
	debitsPending, err := domain.MoneyFromBigInt(acc.DebitsPending.BigInt())
	if err != nil {
		return domain.Balance{}, fmt.Errorf("débitos pendientes fuera de rango: %w", err)
	}

	return domain.Balance{
		Available: domain.CalculateAvailable(creditsPosted, debitsPosted, debitsPending),
		Posted:    creditsPosted - debitsPosted,
		Pending:   debitsPending,
		Currency:  "USD",
	}, nil
}

// transactionFromTransfer convierte una transferencia de TigerBeetle al dominio.
//
// viewpoint es la cuenta desde la que se mira, para determinar si el dinero
// entra o sale. Una misma transferencia es salida para quien envía y entrada
// para quien recibe.
func transactionFromTransfer(t tb.Transfer, viewpoint tb.Uint128) (domain.Transaction, error) {
	amount, err := domain.MoneyFromBigInt(t.Amount.BigInt())
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("monto fuera de rango: %w", err)
	}

	flags := t.TransferFlags()

	status := domain.TransactionStatusCompleted
	switch {
	case flags.Pending:
		status = domain.TransactionStatusPending
	case flags.VoidPendingTransfer:
		status = domain.TransactionStatusVoided
	}

	// La contraparte es el lado opuesto al de la cuenta desde la que se mira.
	direction := domain.DirectionIn
	counterparty := t.DebitAccountID
	if t.DebitAccountID == viewpoint {
		direction = domain.DirectionOut
		counterparty = t.CreditAccountID
	}

	return domain.Transaction{
		ID:             t.ID.BigInt(),
		Type:           domain.TransactionTypeFromCode(t.Code),
		Status:         status,
		Amount:         amount,
		Currency:       "USD",
		Direction:      direction,
		CounterpartyID: counterparty.BigInt(),
		// TigerBeetle guarda el timestamp en nanosegundos desde el epoch.
		Timestamp: time.Unix(0, int64(t.Timestamp)),
	}, nil
}

// translateTransferStatus convierte un código de TigerBeetle en un error del
// dominio.
//
// Traducirlos acá mantiene los detalles del motor encerrados en el adaptador:
// las capas superiores sólo ven errores del dominio.
func translateTransferStatus(status tb.CreateTransferStatus) error {
	switch status {
	case tb.TransferCreated:
		return nil

	// Fondos insuficientes: es la validación real, hecha por la base.
	case tb.TransferExceedsCredits:
		return domain.ErrInsufficientFunds

	case tb.TransferDebitAccountNotFound,
		tb.TransferCreditAccountNotFound:
		return domain.ErrAccountNotFound

	case tb.TransferAccountsMustBeDifferent:
		return domain.ErrSameAccount

	case tb.TransferPendingTransferNotFound:
		return domain.ErrTransferNotFound

	case tb.TransferPendingTransferNotPending:
		return domain.ErrTransferNotPending

	case tb.TransferPendingTransferExpired:
		return domain.ErrTransferExpired

	case tb.TransferPendingTransferAlreadyPosted,
		tb.TransferPendingTransferAlreadyVoided:
		return domain.ErrTransferResolved

	case tb.TransferExists:
		// Idempotencia: reintentar la misma transferencia no la duplica.
		return nil

	default:
		return fmt.Errorf("TigerBeetle rechazó la operación: %s", status)
	}
}

// errUnsupported se devuelve si el clúster no soporta una operación.
var errUnsupported = errors.New("operación no soportada por el clúster")

var _ = errUnsupported
