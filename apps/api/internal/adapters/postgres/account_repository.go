package postgres

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// accountNumberPrefix es el prefijo de todos los números de cuenta.
// Coincide con el formato de los datos de prueba: 4001-XXXX-XXXX-XXXX.
const accountNumberPrefix = "4001"

// AccountRepository implementa ports.AccountRepository sobre PostgreSQL.
//
// Guarda sólo metadatos: el número legible, el dueño y el tipo de producto.
// El saldo vive en TigerBeetle.
type AccountRepository struct {
	pool *pgxpool.Pool
}

var _ ports.AccountRepository = (*AccountRepository)(nil)

func NewAccountRepository(pool *pgxpool.Pool) *AccountRepository {
	return &AccountRepository{pool: pool}
}

func (r *AccountRepository) Create(ctx context.Context, account domain.Account) (domain.Account, error) {
	const query = `
		INSERT INTO accounts (user_id, account_number, tigerbeetle_id, account_type, currency)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, account_number, tigerbeetle_id, account_type, currency, created_at
	`

	tigerBeetleID := numericFromBigInt(account.TigerBeetleID)

	var out domain.Account
	var scannedID pgtype.Numeric

	err := r.pool.QueryRow(ctx, query,
		account.UserID,
		account.AccountNumber,
		tigerBeetleID,
		string(account.Type),
		account.Currency,
	).Scan(
		&out.ID,
		&out.UserID,
		&out.AccountNumber,
		&scannedID,
		&out.Type,
		&out.Currency,
		&out.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			return domain.Account{}, domain.ErrAccountAlreadyExists
		}
		return domain.Account{}, fmt.Errorf("no se pudo crear la cuenta: %w", err)
	}

	out.TigerBeetleID = bigIntFromNumeric(scannedID)
	return out, nil
}

func (r *AccountRepository) FindByID(ctx context.Context, id string) (domain.Account, error) {
	const query = `
		SELECT id, user_id, account_number, tigerbeetle_id, account_type, currency, created_at
		FROM accounts
		WHERE id = $1
	`
	return r.queryOne(ctx, query, id)
}

func (r *AccountRepository) FindByNumber(ctx context.Context, accountNumber string) (domain.Account, error) {
	const query = `
		SELECT id, user_id, account_number, tigerbeetle_id, account_type, currency, created_at
		FROM accounts
		WHERE account_number = $1
	`
	return r.queryOne(ctx, query, accountNumber)
}

func (r *AccountRepository) ListByUser(ctx context.Context, userID string) ([]domain.Account, error) {
	const query = `
		SELECT id, user_id, account_number, tigerbeetle_id, account_type, currency, created_at
		FROM accounts
		WHERE user_id = $1
		ORDER BY created_at ASC
	`

	rows, err := r.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron listar las cuentas: %w", err)
	}
	defer rows.Close()

	var accounts []domain.Account
	for rows.Next() {
		var account domain.Account
		var tigerBeetleID pgtype.Numeric

		err := rows.Scan(
			&account.ID,
			&account.UserID,
			&account.AccountNumber,
			&tigerBeetleID,
			&account.Type,
			&account.Currency,
			&account.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("no se pudo leer una cuenta: %w", err)
		}

		account.TigerBeetleID = bigIntFromNumeric(tigerBeetleID)
		accounts = append(accounts, account)
	}

	return accounts, rows.Err()
}

// NextAccountNumber genera un número de cuenta libre.
//
// El formato sigue el de los datos de prueba: 4001-XXXX-XXXX-XXXX. Los tres
// grupos aleatorios se generan en la base para evitar una ida y vuelta extra,
// y se reintenta si el número ya existe.
func (r *AccountRepository) NextAccountNumber(ctx context.Context) (string, error) {
	const query = `
		SELECT $1 || '-' ||
		       LPAD((random() * 9999)::int::text, 4, '0') || '-' ||
		       LPAD((random() * 9999)::int::text, 4, '0') || '-' ||
		       LPAD((random() * 9999)::int::text, 4, '0')
	`

	// Con ~1.6k cuentas sobre 10^12 combinaciones, una colisión es
	// improbabilísima. Los reintentos existen igual porque "improbable" no
	// es "imposible" y el costo de manejarlo es mínimo.
	const maxAttempts = 5

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var candidate string
		if err := r.pool.QueryRow(ctx, query, accountNumberPrefix).Scan(&candidate); err != nil {
			return "", fmt.Errorf("no se pudo generar el número de cuenta: %w", err)
		}

		var exists bool
		err := r.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM accounts WHERE account_number = $1)`,
			candidate,
		).Scan(&exists)
		if err != nil {
			return "", fmt.Errorf("no se pudo verificar el número de cuenta: %w", err)
		}

		if !exists {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no se pudo generar un número de cuenta libre tras %d intentos", maxAttempts)
}

// NumbersByTigerBeetleIDs traduce ids de TigerBeetle a números de cuenta.
//
// Una sola consulta para todos los ids: el historial necesita resolver la
// contraparte de cada movimiento, y consultarlas de a una sería N+1.
func (r *AccountRepository) NumbersByTigerBeetleIDs(ctx context.Context, ids []*big.Int) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}

	numerics := make([]pgtype.Numeric, 0, len(ids))
	for _, id := range ids {
		numerics = append(numerics, numericFromBigInt(id))
	}

	const query = `
		SELECT tigerbeetle_id, account_number
		FROM accounts
		WHERE tigerbeetle_id = ANY($1)
	`

	rows, err := r.pool.Query(ctx, query, numerics)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron resolver los números de cuenta: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string, len(ids))
	for rows.Next() {
		var tigerBeetleID pgtype.Numeric
		var accountNumber string

		if err := rows.Scan(&tigerBeetleID, &accountNumber); err != nil {
			return nil, fmt.Errorf("no se pudo leer un número de cuenta: %w", err)
		}

		if id := bigIntFromNumeric(tigerBeetleID); id != nil {
			out[id.String()] = accountNumber
		}
	}

	return out, rows.Err()
}

func (r *AccountRepository) queryOne(ctx context.Context, query string, arg any) (domain.Account, error) {
	var account domain.Account
	var tigerBeetleID pgtype.Numeric

	err := r.pool.QueryRow(ctx, query, arg).Scan(
		&account.ID,
		&account.UserID,
		&account.AccountNumber,
		&tigerBeetleID,
		&account.Type,
		&account.Currency,
		&account.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	if err != nil {
		return domain.Account{}, fmt.Errorf("no se pudo buscar la cuenta: %w", err)
	}

	account.TigerBeetleID = bigIntFromNumeric(tigerBeetleID)
	return account, nil
}

// ------------------------------------------------------------------------------
// Conversión entre el u128 de TigerBeetle y el NUMERIC de Postgres
//
// Postgres no tiene un entero de 128 bits, así que usamos NUMERIC(39,0):
// 2^128-1 tiene 39 dígitos y NUMERIC es exacto, no aproximado como un float.
// ------------------------------------------------------------------------------

func numericFromBigInt(value *big.Int) pgtype.Numeric {
	if value == nil {
		return pgtype.Numeric{Valid: false}
	}
	return pgtype.Numeric{Int: value, Exp: 0, Valid: true}
}

func bigIntFromNumeric(value pgtype.Numeric) *big.Int {
	if !value.Valid || value.Int == nil {
		return nil
	}

	// Exp siempre debería ser 0 por la definición de la columna (escala cero),
	// pero si no lo fuera hay que escalar el valor para no perder magnitud.
	if value.Exp == 0 {
		return new(big.Int).Set(value.Int)
	}

	result := new(big.Int).Set(value.Int)
	if value.Exp > 0 {
		multiplier := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(value.Exp)), nil)
		return result.Mul(result, multiplier)
	}

	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-value.Exp)), nil)
	return result.Div(result, divisor)
}
