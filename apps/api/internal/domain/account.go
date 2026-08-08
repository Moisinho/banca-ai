package domain

import (
	"math/big"
	"regexp"
	"time"
)

// AccountType es el tipo de producto bancario.
type AccountType string

const (
	AccountTypeSavings    AccountType = "savings"
	AccountTypeChecking   AccountType = "checking"
	AccountTypeInvestment AccountType = "investment"
)

// Valid indica si el tipo de cuenta es uno de los soportados.
func (t AccountType) Valid() bool {
	switch t {
	case AccountTypeSavings, AccountTypeChecking, AccountTypeInvestment:
		return true
	default:
		return false
	}
}

// Code devuelve el código numérico que se guarda en TigerBeetle.
//
// TigerBeetle no admite texto: el campo Code es un uint16. Este mapeo permite
// saber a qué producto pertenece una cuenta consultando sólo TigerBeetle.
func (t AccountType) Code() uint16 {
	switch t {
	case AccountTypeSavings:
		return 1
	case AccountTypeChecking:
		return 2
	case AccountTypeInvestment:
		return 3
	default:
		return 0
	}
}

// LedgerUSD identifica el libro contable en dólares.
//
// TigerBeetle usa el ledger para particionar: no permite transferencias entre
// cuentas de ledgers distintos. Eso hace imposible, por construcción, mover
// dinero entre monedas sin una conversión explícita.
const LedgerUSD uint32 = 1

// OperatorAccountID es la cuenta que representa al mundo exterior.
//
// Es el "EXTERNAL" de los datos de prueba. Todo depósito viene de acá y todo
// retiro va para acá, porque en contabilidad de doble entrada el dinero no
// aparece de la nada: siempre sale de algún lado.
//
// Esta cuenta queda con saldo negativo, y eso es correcto: representa el
// pasivo del banco frente a sus clientes.
const OperatorAccountID uint64 = 1

// ExternalAccountNumber es como aparece la cuenta del operador en los datos
// de prueba y en la interfaz de usuario.
const ExternalAccountNumber = "EXTERNAL"

// accountNumberPattern valida el formato 4001-XXXX-XXXX-XXXX.
var accountNumberPattern = regexp.MustCompile(`^\d{4}(-\d{4}){3}$`)

// ValidAccountNumber indica si la cadena tiene el formato de número de cuenta.
func ValidAccountNumber(s string) bool {
	return accountNumberPattern.MatchString(s)
}

// Account es una cuenta bancaria.
//
// Los datos identificatorios viven en Postgres; el saldo vive en TigerBeetle.
// Esta estructura los une para las capas superiores.
type Account struct {
	ID            string
	UserID        string
	AccountNumber string
	TigerBeetleID *big.Int
	Type          AccountType
	Currency      string
	CreatedAt     time.Time
}

// Balance es el saldo de una cuenta en un momento dado.
//
// Los tres valores son distintos y la diferencia importa:
//   - Posted: lo que ya se liquidó.
//   - Pending: lo reservado por operaciones sin confirmar.
//   - Available: lo que el usuario puede gastar realmente.
type Balance struct {
	AccountID     string
	AccountNumber string
	Available     Money
	Posted        Money
	Pending       Money
	Currency      string
}

// CalculateAvailable obtiene el saldo disponible de una cuenta de activo.
//
// Fórmula: credits_posted - debits_posted - debits_pending
//
// Los débitos pendientes se restan porque ya están comprometidos. Si la IA
// propone transferir $100 y esperamos confirmación, esos $100 no pueden
// gastarse en otra operación: sin esta resta habría doble gasto.
func CalculateAvailable(creditsPosted, debitsPosted, debitsPending Money) Money {
	return creditsPosted - debitsPosted - debitsPending
}
