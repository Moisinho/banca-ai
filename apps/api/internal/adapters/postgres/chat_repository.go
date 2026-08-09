package postgres

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// ChatMessageRepository persiste la conversación con el asistente.
type ChatMessageRepository struct {
	pool *pgxpool.Pool
}

var _ ports.ChatMessageRepository = (*ChatMessageRepository)(nil)

func NewChatMessageRepository(pool *pgxpool.Pool) *ChatMessageRepository {
	return &ChatMessageRepository{pool: pool}
}

func (r *ChatMessageRepository) Store(ctx context.Context, message domain.ChatMessage) (domain.ChatMessage, error) {
	const query = `
		INSERT INTO chat_messages (user_id, role, content, pending_transfer_id, confirmation_status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, role, content, pending_transfer_id, confirmation_status, created_at
	`

	var pendingID pgtype.Numeric
	if message.PendingTransferID != nil {
		pendingID = numericFromBigInt(message.PendingTransferID)
	}

	// La columna tiene un CHECK que rechaza cadenas vacías: sin operación
	// pendiente va NULL.
	var status *string
	if message.ConfirmationStatus != "" {
		s := string(message.ConfirmationStatus)
		status = &s
	}

	var out domain.ChatMessage
	var scannedPending pgtype.Numeric
	var scannedStatus *string

	err := r.pool.QueryRow(ctx, query,
		message.UserID,
		string(message.Role),
		message.Content,
		pendingID,
		status,
	).Scan(
		&out.ID,
		&out.UserID,
		&out.Role,
		&out.Content,
		&scannedPending,
		&scannedStatus,
		&out.CreatedAt,
	)
	if err != nil {
		return domain.ChatMessage{}, fmt.Errorf("no se pudo guardar el mensaje: %w", err)
	}

	out.PendingTransferID = bigIntFromNumeric(scannedPending)
	if scannedStatus != nil {
		out.ConfirmationStatus = domain.ConfirmationStatus(*scannedStatus)
	}

	return out, nil
}

// ListRecent devuelve los últimos mensajes en orden cronológico.
//
// La consulta ordena descendente para quedarse con los más recientes, y
// después se invierte: el modelo necesita leer la conversación de principio a
// fin, no al revés.
func (r *ChatMessageRepository) ListRecent(ctx context.Context, userID string, limit int) ([]domain.ChatMessage, error) {
	const query = `
		SELECT id, user_id, role, content, pending_transfer_id, confirmation_status, created_at
		FROM chat_messages
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := r.pool.Query(ctx, query, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron leer los mensajes: %w", err)
	}
	defer rows.Close()

	var messages []domain.ChatMessage
	for rows.Next() {
		message, err := scanChatMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Invierte para dejarlos del más antiguo al más reciente.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

// ListBefore devuelve los mensajes anteriores a uno dado, en orden cronológico.
//
// El cursor compara la pareja (created_at, id) y no sólo la fecha: un turno
// guarda el mensaje del usuario y el del asistente casi a la vez, y si dos
// comparten timestamp un cursor por fecha se saltaría uno o lo repetiría. La
// comparación de tuplas que ofrece Postgres desempata por id de forma estable.
//
// El segundo valor de retorno indica si todavía quedan mensajes más antiguos.
// Se resuelve pidiendo uno de más en lugar de con un COUNT aparte.
func (r *ChatMessageRepository) ListBefore(ctx context.Context, userID, beforeID string, limit int) ([]domain.ChatMessage, bool, error) {
	const query = `
		SELECT id, user_id, role, content, pending_transfer_id, confirmation_status, created_at
		FROM chat_messages
		WHERE user_id = $1
		  AND (created_at, id) < (
			SELECT created_at, id FROM chat_messages WHERE id = $2 AND user_id = $1
		  )
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`

	rows, err := r.pool.Query(ctx, query, userID, beforeID, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("no se pudieron leer los mensajes anteriores: %w", err)
	}
	defer rows.Close()

	var messages []domain.ChatMessage
	for rows.Next() {
		message, err := scanChatMessage(rows)
		if err != nil {
			return nil, false, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}

	// Invierte para dejarlos del más antiguo al más reciente.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, hasMore, nil
}

func (r *ChatMessageRepository) FindByPendingTransfer(ctx context.Context, userID string, transferID *big.Int) (domain.ChatMessage, error) {
	const query = `
		SELECT id, user_id, role, content, pending_transfer_id, confirmation_status, created_at
		FROM chat_messages
		WHERE user_id = $1 AND pending_transfer_id = $2
	`

	row := r.pool.QueryRow(ctx, query, userID, numericFromBigInt(transferID))

	message, err := scanChatMessageRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ChatMessage{}, domain.ErrTransferNotFound
	}
	if err != nil {
		return domain.ChatMessage{}, fmt.Errorf("no se pudo buscar la operación pendiente: %w", err)
	}

	return message, nil
}

// UpdateConfirmation marca una operación como confirmada o rechazada.
//
// La condición sobre confirmation_status hace la actualización atómica: dos
// peticiones concurrentes no pueden resolver la misma operación dos veces.
func (r *ChatMessageRepository) UpdateConfirmation(ctx context.Context, transferID *big.Int, status domain.ConfirmationStatus) error {
	const query = `
		UPDATE chat_messages
		SET confirmation_status = $2
		WHERE pending_transfer_id = $1 AND confirmation_status = 'pending'
	`

	tag, err := r.pool.Exec(ctx, query, numericFromBigInt(transferID), string(status))
	if err != nil {
		return fmt.Errorf("no se pudo actualizar la confirmación: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return domain.ErrTransferResolved
	}

	return nil
}

// ------------------------------------------------------------------------------
// Lectura de filas
// ------------------------------------------------------------------------------

type scannable interface {
	Scan(dest ...any) error
}

func scanChatMessage(rows pgx.Rows) (domain.ChatMessage, error) {
	return scanChatMessageRow(rows)
}

func scanChatMessageRow(row scannable) (domain.ChatMessage, error) {
	var message domain.ChatMessage
	var pendingID pgtype.Numeric
	var status *string

	err := row.Scan(
		&message.ID,
		&message.UserID,
		&message.Role,
		&message.Content,
		&pendingID,
		&status,
		&message.CreatedAt,
	)
	if err != nil {
		return domain.ChatMessage{}, err
	}

	message.PendingTransferID = bigIntFromNumeric(pendingID)
	if status != nil {
		message.ConfirmationStatus = domain.ConfirmationStatus(*status)
	}

	return message, nil
}
