// Package postgres implementa los repositorios sobre PostgreSQL.
//
// Acá viven usuarios, credenciales y metadatos de cuentas. Los saldos y las
// transacciones NO: de eso se encarga TigerBeetle. Si ves una columna con un
// saldo en este paquete, algo se hizo mal.
package postgres

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed all:migrations
var migrationsFS embed.FS

// Config son los parámetros de conexión al pool.
type Config struct {
	DSN string

	// MaxConns acota las conexiones simultáneas. Postgres reserva memoria por
	// conexión, así que un pool sin límite puede tumbar la base bajo carga.
	MaxConns int32

	// MaxConnLifetime recicla conexiones periódicamente. Evita que una conexión
	// vieja quede colgada tras un reinicio de la base o un corte de red.
	MaxConnLifetime time.Duration
}

// DefaultConfig devuelve valores razonables para el tamaño de este proyecto.
func DefaultConfig(dsn string) Config {
	return Config{
		DSN:             dsn,
		MaxConns:        20,
		MaxConnLifetime: time.Hour,
	}
}

// Connect abre el pool y verifica que la base responda.
//
// Reintenta con espera creciente: en docker-compose la API suele arrancar antes
// de que Postgres termine de inicializar, y fallar en el primer intento haría
// que el contenedor muriera innecesariamente.
func Connect(ctx context.Context, cfg Config, log *slog.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("la cadena de conexión de Postgres no es válida: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("no se pudo crear el pool de conexiones: %w", err)
	}

	const maxAttempts = 10
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := pool.Ping(pingCtx)
		cancel()

		if err == nil {
			log.Info("conectado a PostgreSQL")
			return pool, nil
		}

		lastErr = err
		wait := time.Duration(attempt) * time.Second
		log.Warn("PostgreSQL todavía no responde, reintentando",
			"intento", attempt,
			"espera", wait.String(),
			"error", err,
		)

		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}

	pool.Close()
	return nil, fmt.Errorf("no se pudo conectar con PostgreSQL tras %d intentos: %w", maxAttempts, lastErr)
}

// Migrate aplica las migraciones pendientes.
//
// Es un runner mínimo a propósito: registra qué migraciones ya corrieron y
// aplica las que faltan, en orden alfabético. Para un proyecto de este tamaño
// una dependencia externa de migraciones no se justifica.
//
// Cada migración corre dentro de una transacción: si falla a la mitad, no deja
// el esquema en un estado intermedio.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	// Tabla de control. IF NOT EXISTS la hace idempotente.
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		return fmt.Errorf("no se pudo crear la tabla de migraciones: %w", err)
	}

	applied, err := appliedMigrations(ctx, pool)
	if err != nil {
		return err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("no se pudieron leer las migraciones: %w", err)
	}

	// Sólo las de subida, ordenadas por nombre para respetar la secuencia.
	var pending []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		version := strings.TrimSuffix(name, ".up.sql")
		if !applied[version] {
			pending = append(pending, version)
		}
	}
	sort.Strings(pending)

	if len(pending) == 0 {
		log.Info("el esquema está al día, no hay migraciones pendientes")
		return nil
	}

	for _, version := range pending {
		if err := applyMigration(ctx, pool, version, log); err != nil {
			return err
		}
	}

	log.Info("migraciones aplicadas", "cantidad", len(pending))
	return nil
}

func appliedMigrations(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("no se pudieron leer las migraciones aplicadas: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("no se pudo leer una migración aplicada: %w", err)
		}
		applied[version] = true
	}

	return applied, rows.Err()
}

func applyMigration(ctx context.Context, pool *pgxpool.Pool, version string, log *slog.Logger) error {
	content, err := migrationsFS.ReadFile("migrations/" + version + ".up.sql")
	if err != nil {
		return fmt.Errorf("no se pudo leer la migración %s: %w", version, err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("no se pudo iniciar la transacción de %s: %w", version, err)
	}
	// Si el commit tuvo éxito este rollback no hace nada; si algo falló antes,
	// deshace los cambios parciales.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, string(content)); err != nil {
		return fmt.Errorf("falló la migración %s: %w", version, err)
	}

	_, err = tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version)
	if err != nil {
		return fmt.Errorf("no se pudo registrar la migración %s: %w", version, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("no se pudo confirmar la migración %s: %w", version, err)
	}

	log.Info("migración aplicada", "version", version)
	return nil
}
