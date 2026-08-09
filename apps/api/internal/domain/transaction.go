package domain

import (
	"math/big"
	"time"
)

// TransactionType es la naturaleza de un movimiento de dinero.
type TransactionType string

const (
	// TransactionTypeDeposit: entra dinero desde afuera del banco.
	TransactionTypeDeposit TransactionType = "deposit"

	// TransactionTypeWithdrawal: sale dinero hacia afuera del banco.
	TransactionTypeWithdrawal TransactionType = "withdrawal"

	// TransactionTypeTransfer: entre cuentas de distintos usuarios.
	TransactionTypeTransfer TransactionType = "transfer"

	// TransactionTypeInternalTransfer: entre cuentas del mismo usuario.
	TransactionTypeInternalTransfer TransactionType = "internal_transfer"
)

// Valid indica si el tipo es uno de los soportados.
func (t TransactionType) Valid() bool {
	switch t {
	case TransactionTypeDeposit, TransactionTypeWithdrawal,
		TransactionTypeTransfer, TransactionTypeInternalTransfer:
		return true
	default:
		return false
	}
}

// Code devuelve el código numérico que se guarda en TigerBeetle.
//
// Igual que con AccountType: TigerBeetle sólo admite números, así que el tipo
// de operación se codifica para poder reconstruirlo al leer.
func (t TransactionType) Code() uint16 {
	switch t {
	case TransactionTypeDeposit:
		return 10
	case TransactionTypeWithdrawal:
		return 20
	case TransactionTypeTransfer:
		return 30
	case TransactionTypeInternalTransfer:
		return 40
	default:
		return 0
	}
}

// SeedAdjustmentCode marca los asientos técnicos de la siembra de datos.
//
// Al cargar los datos de prueba hay que financiar las cuentas antes de
// reproducir su historial, y ajustar el saldo final para que coincida con el
// archivo. Esos movimientos son un artefacto de la carga, no operaciones que
// la persona haya hecho, así que se marcan con un código propio para poder
// excluirlos del historial que ve el usuario.
const SeedAdjustmentCode uint16 = 900

// IsSeedAdjustment indica si una transferencia es un asiento técnico de la
// siembra y por lo tanto no debe mostrarse.
func IsSeedAdjustment(code uint16) bool {
	return code == SeedAdjustmentCode
}

// TransactionTypeFromCode reconstruye el tipo desde el código de TigerBeetle.
func TransactionTypeFromCode(code uint16) TransactionType {
	switch code {
	case 10:
		return TransactionTypeDeposit
	case 20:
		return TransactionTypeWithdrawal
	case 30:
		return TransactionTypeTransfer
	case 40:
		return TransactionTypeInternalTransfer
	default:
		return ""
	}
}

// TransactionStatus es el estado de un movimiento.
type TransactionStatus string

const (
	// TransactionStatusPending: fondos reservados, esperando confirmación.
	// Es el estado en que queda una operación propuesta por la IA.
	TransactionStatusPending TransactionStatus = "pending"

	// TransactionStatusCompleted: el dinero se movió definitivamente.
	TransactionStatusCompleted TransactionStatus = "completed"

	// TransactionStatusVoided: la reserva se liberó sin mover dinero.
	TransactionStatusVoided TransactionStatus = "voided"
)

// Direction indica si el dinero entra o sale, visto desde una cuenta concreta.
//
// Una misma transferencia es salida para quien envía y entrada para quien
// recibe: la dirección no es propiedad del movimiento sino del punto de vista.
type Direction string

const (
	DirectionIn  Direction = "in"
	DirectionOut Direction = "out"
)

// Transaction es un movimiento de dinero.
type Transaction struct {
	ID          *big.Int
	Type        TransactionType
	Status      TransactionStatus
	Amount      Money
	Currency    string
	FromAccount string
	ToAccount   string
	Description string
	Direction   Direction
	Timestamp   time.Time

	// CounterpartyID es el id en TigerBeetle de la otra cuenta involucrada.
	//
	// TigerBeetle sólo guarda ids numéricos, así que para mostrar el número de
	// cuenta legible hay que buscarlo en Postgres. Este campo es interno: no
	// se expone en la API.
	CounterpartyID *big.Int
}

// TransferRequest describe una transferencia por ejecutar.
type TransferRequest struct {
	FromTigerBeetleID *big.Int
	ToTigerBeetleID   *big.Int
	Amount            Money
	Type              TransactionType
	Description       string

	// Pending crea la transferencia en dos fases: reserva los fondos sin
	// moverlos, a la espera de confirmación explícita.
	//
	// Es lo que usa el chat con IA: el modelo nunca mueve dinero por su cuenta,
	// sólo propone una reserva que el usuario confirma o rechaza.
	Pending bool

	// PendingTimeout es cuánto vive la reserva antes de liberarse sola.
	// Sólo aplica cuando Pending es true.
	PendingTimeout time.Duration

	// CodeOverride reemplaza el código derivado del tipo de transacción.
	//
	// Lo usa la siembra de datos para marcar sus asientos técnicos y que el
	// historial pueda excluirlos. Cero significa usar el código del tipo.
	CodeOverride uint16
}

// Validate verifica las reglas de negocio de una transferencia.
//
// Estas comprobaciones no reemplazan a las de TigerBeetle: la base sigue
// siendo la autoridad sobre los fondos. Acá atajamos lo que podemos rechazar
// antes de llegar a la red, con un mensaje más claro.
func (r TransferRequest) Validate() error {
	if err := r.Amount.Validate(); err != nil {
		return err
	}

	if !r.Type.Valid() {
		return ErrInvalidTransactionType
	}

	if r.FromTigerBeetleID == nil || r.ToTigerBeetleID == nil {
		return ErrAccountNotFound
	}

	// TigerBeetle rechaza una transferencia entre una cuenta y sí misma, pero
	// preferimos devolver un mensaje propio antes que un error de la base.
	if r.FromTigerBeetleID.Cmp(r.ToTigerBeetleID) == 0 {
		return ErrSameAccount
	}

	return nil
}

// Las operaciones pendientes de confirmación viven en chat.go, junto al resto
// del modelo de conversación: es el chat con IA quien las genera.
