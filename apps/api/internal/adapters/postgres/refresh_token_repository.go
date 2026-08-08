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

// RefreshTokenRepository implementa la rotación de refresh tokens.
//
// Guarda el hash SHA-256, nunca el token en claro: si alguien accede a la base
// de datos, lo que encuentra no sirve para autenticarse.
type RefreshTokenRepository struct {
	pool *pgxpool.Pool
}

var _ ports.RefreshTokenRepository = (*RefreshTokenRepository)(nil)

func NewRefreshTokenRepository(pool *pgxpool.Pool) *RefreshTokenRepository {
	return &RefreshTokenRepository{pool: pool}
}

func (r *RefreshTokenRepository) Store(ctx context.Context, token domain.RefreshToken) error {
	const query = `
		INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at, user_agent, ip_address)
		VALUES ($1, $2, $3, $4, $5, $6)
	`

	// La IP se guarda en una columna INET, que rechaza cadenas mal formadas.
	// Si no la tenemos, va NULL en vez de una cadena vacía.
	var ip *string
	if token.IPAddress != "" {
		ip = &token.IPAddress
	}

	_, err := r.pool.Exec(ctx, query,
		token.UserID,
		token.FamilyID,
		token.TokenHash,
		token.ExpiresAt,
		token.UserAgent,
		ip,
	)
	if err != nil {
		return fmt.Errorf("no se pudo guardar el refresh token: %w", err)
	}

	return nil
}

func (r *RefreshTokenRepository) FindByHash(ctx context.Context, tokenHash string) (domain.RefreshToken, error) {
	const query = `
		SELECT id, user_id, family_id, token_hash, expires_at, used_at, revoked_at, created_at,
		       COALESCE(user_agent, ''), COALESCE(host(ip_address), '')
		FROM refresh_tokens
		WHERE token_hash = $1
	`

	var token domain.RefreshToken
	var usedAt, revokedAt pgtype.Timestamptz

	err := r.pool.QueryRow(ctx, query, tokenHash).Scan(
		&token.ID,
		&token.UserID,
		&token.FamilyID,
		&token.TokenHash,
		&token.ExpiresAt,
		&usedAt,
		&revokedAt,
		&token.CreatedAt,
		&token.UserAgent,
		&token.IPAddress,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RefreshToken{}, domain.ErrTokenNotFound
	}
	if err != nil {
		return domain.RefreshToken{}, fmt.Errorf("no se pudo buscar el refresh token: %w", err)
	}

	if usedAt.Valid {
		token.UsedAt = &usedAt.Time
	}
	if revokedAt.Valid {
		token.RevokedAt = &revokedAt.Time
	}

	return token, nil
}

// MarkUsed marca un token como consumido.
//
// La condición `used_at IS NULL` hace la operación atómica: si dos peticiones
// concurrentes intentan canjear el mismo token, sólo una actualiza la fila. La
// otra recibe cero filas afectadas y se trata como reutilización.
func (r *RefreshTokenRepository) MarkUsed(ctx context.Context, tokenHash string) error {
	const query = `
		UPDATE refresh_tokens
		SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL
	`

	tag, err := r.pool.Exec(ctx, query, tokenHash)
	if err != nil {
		return fmt.Errorf("no se pudo marcar el refresh token como usado: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return domain.ErrTokenAlreadyUsed
	}

	return nil
}

// RevokeFamily revoca todos los tokens de una familia.
//
// Se invoca al detectar la reutilización de un token ya consumido: significa
// que alguien tiene una copia, así que se cierra la cadena entera y el usuario
// debe iniciar sesión de nuevo.
func (r *RefreshTokenRepository) RevokeFamily(ctx context.Context, familyID string) error {
	const query = `
		UPDATE refresh_tokens
		SET revoked_at = now()
		WHERE family_id = $1 AND revoked_at IS NULL
	`

	if _, err := r.pool.Exec(ctx, query, familyID); err != nil {
		return fmt.Errorf("no se pudo revocar la familia de tokens: %w", err)
	}

	return nil
}

// RevokeAllForUser cierra todas las sesiones activas de un usuario.
func (r *RefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID string) error {
	const query = `
		UPDATE refresh_tokens
		SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL
	`

	if _, err := r.pool.Exec(ctx, query, userID); err != nil {
		return fmt.Errorf("no se pudieron revocar los tokens del usuario: %w", err)
	}

	return nil
}

// ------------------------------------------------------------------------------
// Metadatos de transacciones
// ------------------------------------------------------------------------------

// TransactionMetadataRepository guarda las descripciones de las transferencias.
//
// TigerBeetle no admite texto libre: sus campos user_data son numéricos. La
// descripción que escribe la persona ("Pago de gimnasio") vive acá, enlazada
// por el id de la transferencia.
type TransactionMetadataRepository struct {
	pool *pgxpool.Pool
}

var _ ports.TransactionMetadataRepository = (*TransactionMetadataRepository)(nil)

func NewTransactionMetadataRepository(pool *pgxpool.Pool) *TransactionMetadataRepository {
	return &TransactionMetadataRepository{pool: pool}
}

func (r *TransactionMetadataRepository) Store(ctx context.Context, transferID *big.Int, description string) error {
	const query = `
		INSERT INTO transaction_metadata (transfer_id, description)
		VALUES ($1, $2)
		ON CONFLICT (transfer_id) DO UPDATE SET description = EXCLUDED.description
	`

	_, err := r.pool.Exec(ctx, query, numericFromBigInt(transferID), description)
	if err != nil {
		return fmt.Errorf("no se pudo guardar la descripción de la transacción: %w", err)
	}

	return nil
}

// GetMany recupera las descripciones de varias transferencias de una vez.
//
// Una sola consulta en lugar de una por transacción: al listar el historial,
// consultar de a una sería el problema N+1.
func (r *TransactionMetadataRepository) GetMany(ctx context.Context, transferIDs []*big.Int) (map[string]string, error) {
	if len(transferIDs) == 0 {
		return map[string]string{}, nil
	}

	ids := make([]pgtype.Numeric, 0, len(transferIDs))
	for _, id := range transferIDs {
		ids = append(ids, numericFromBigInt(id))
	}

	const query = `
		SELECT transfer_id, COALESCE(description, '')
		FROM transaction_metadata
		WHERE transfer_id = ANY($1)
	`

	rows, err := r.pool.Query(ctx, query, ids)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron leer las descripciones: %w", err)
	}
	defer rows.Close()

	descriptions := make(map[string]string, len(transferIDs))
	for rows.Next() {
		var transferID pgtype.Numeric
		var description string

		if err := rows.Scan(&transferID, &description); err != nil {
			return nil, fmt.Errorf("no se pudo leer una descripción: %w", err)
		}

		if id := bigIntFromNumeric(transferID); id != nil {
			descriptions[id.String()] = description
		}
	}

	return descriptions, rows.Err()
}
