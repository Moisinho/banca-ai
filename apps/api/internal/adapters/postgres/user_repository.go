package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// uniqueViolationCode es el SQLSTATE que Postgres devuelve al violar un índice
// único. Lo usamos para traducir el error de la base a uno del dominio.
const uniqueViolationCode = "23505"

// UserRepository implementa ports.UserRepository sobre PostgreSQL.
type UserRepository struct {
	pool *pgxpool.Pool
}

var _ ports.UserRepository = (*UserRepository)(nil)

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// Create inserta un usuario nuevo.
//
// La unicidad del correo la garantiza el índice único sobre LOWER(email), no
// una consulta previa: entre un SELECT y un INSERT hay una ventana en la que
// otro registro puede colarse. Dejamos que la base decida y traducimos el error.
func (r *UserRepository) Create(ctx context.Context, user domain.User) (domain.User, error) {
	const query = `
		INSERT INTO users (email, password_hash, full_name)
		VALUES ($1, $2, $3)
		RETURNING id, email, password_hash, full_name, created_at, updated_at
	`

	var out domain.User
	err := r.pool.QueryRow(ctx, query, user.Email, user.PasswordHash, user.FullName).Scan(
		&out.ID,
		&out.Email,
		&out.PasswordHash,
		&out.FullName,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			return domain.User{}, domain.ErrEmailAlreadyUsed
		}
		return domain.User{}, fmt.Errorf("no se pudo crear el usuario: %w", err)
	}

	return out, nil
}

func (r *UserRepository) FindByID(ctx context.Context, id string) (domain.User, error) {
	const query = `
		SELECT id, email, password_hash, full_name, created_at, updated_at
		FROM users
		WHERE id = $1
	`

	var user domain.User
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.FullName,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("no se pudo buscar el usuario: %w", err)
	}

	return user, nil
}

// FindByEmail busca por correo sin distinguir mayúsculas.
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (domain.User, error) {
	const query = `
		SELECT id, email, password_hash, full_name, created_at, updated_at
		FROM users
		WHERE LOWER(email) = LOWER($1)
	`

	var user domain.User
	err := r.pool.QueryRow(ctx, query, email).Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.FullName,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrUserNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("no se pudo buscar el usuario por correo: %w", err)
	}

	return user, nil
}

func (r *UserRepository) EmailExists(ctx context.Context, email string) (bool, error) {
	const query = `SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(email) = LOWER($1))`

	var exists bool
	if err := r.pool.QueryRow(ctx, query, email).Scan(&exists); err != nil {
		return false, fmt.Errorf("no se pudo verificar el correo: %w", err)
	}

	return exists, nil
}
