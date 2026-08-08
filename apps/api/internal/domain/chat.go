package domain

import (
	"math/big"
	"time"
)

// ChatRole es quién emitió un mensaje.
type ChatRole string

const (
	ChatRoleUser      ChatRole = "user"
	ChatRoleAssistant ChatRole = "assistant"
	ChatRoleTool      ChatRole = "tool"
)

// ConfirmationStatus es el estado de una operación propuesta por la IA.
type ConfirmationStatus string

const (
	// ConfirmationPending: los fondos están reservados esperando decisión.
	ConfirmationPending ConfirmationStatus = "pending"

	// ConfirmationConfirmed: el usuario aprobó y el dinero se movió.
	ConfirmationConfirmed ConfirmationStatus = "confirmed"

	// ConfirmationRejected: el usuario rechazó y los fondos se liberaron.
	ConfirmationRejected ConfirmationStatus = "rejected"

	// ConfirmationExpired: nadie decidió a tiempo y la reserva venció.
	ConfirmationExpired ConfirmationStatus = "expired"
)

// ChatMessage es un mensaje de la conversación con el asistente.
type ChatMessage struct {
	ID      string
	UserID  string
	Role    ChatRole
	Content string

	// PendingTransferID apunta a la transferencia reservada en TigerBeetle
	// cuando el asistente propuso una operación que mueve dinero.
	PendingTransferID  *big.Int
	ConfirmationStatus ConfirmationStatus

	CreatedAt time.Time
}

// PendingOperation es una operación propuesta por la IA, con los fondos ya
// reservados, a la espera de que el usuario confirme o rechace.
//
// La reserva existe en TigerBeetle como transferencia en dos fases: el dinero
// está comprometido pero no movido. Si el usuario no decide, el timeout la
// libera sola.
type PendingOperation struct {
	TransferID  *big.Int
	Operation   TransactionType
	Amount      Money
	Currency    string
	FromAccount string
	ToAccount   string
	Description string
	Status      ConfirmationStatus
	ExpiresAt   time.Time
}

// Expired indica si la reserva ya venció.
func (p PendingOperation) Expired() bool {
	return time.Now().After(p.ExpiresAt)
}
