package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config agrupa toda la configuración de la aplicación.
//
// Se carga una sola vez al arrancar y se pasa explícitamente a quien la
// necesite. Nada de leer variables de entorno desde el medio del código:
// si falta algo, queremos enterarnos al inicio y no cuando un usuario
// dispara la operación.
type Config struct {
	Env                string
	Port               string
	Postgres           PostgresConfig
	TigerBeetle        TigerBeetleConfig
	Auth               AuthConfig
	AI                 AIConfig
	RateLimit          RateLimitConfig
	Seed               SeedConfig
	CORSAllowedOrigins []string
}

type PostgresConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
	SSLMode  string
}

// DSN arma la cadena de conexión para pgx.
func (p PostgresConfig) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=%s",
		p.User, p.Password, p.Host, p.Port, p.Database, p.SSLMode,
	)
}

type TigerBeetleConfig struct {
	ClusterID uint64
	Addresses []string
}

type AuthConfig struct {
	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	BcryptCost      int
}

type AIConfig struct {
	OpenRouterAPIKey  string
	OpenRouterModel   string
	OpenRouterBaseURL string
}

// Enabled indica si el chat con IA puede funcionar.
// Sin API key el resto de la aplicación sigue operando con normalidad.
func (a AIConfig) Enabled() bool {
	return a.OpenRouterAPIKey != ""
}

type RateLimitConfig struct {
	GeneralRPM int
	AuthRPM    int
}

type SeedConfig struct {
	Enabled    bool
	UserLimit  int
	BcryptCost int
	DataPath   string
}

// Load lee la configuración del entorno y la valida.
//
// Devuelve error en lugar de terminar el proceso: quien llama decide qué hacer,
// y así la función se puede testear.
func Load() (*Config, error) {
	cfg := &Config{
		Env:  getEnv("ENV", "development"),
		Port: getEnv("API_PORT", "8080"),

		Postgres: PostgresConfig{
			Host:     getEnv("POSTGRES_HOST", "localhost"),
			Port:     getEnv("POSTGRES_PORT", "5432"),
			User:     getEnv("POSTGRES_USER", "banca"),
			Password: getEnv("POSTGRES_PASSWORD", ""),
			Database: getEnv("POSTGRES_DB", "banca"),
			SSLMode:  getEnv("POSTGRES_SSLMODE", "disable"),
		},

		TigerBeetle: TigerBeetleConfig{
			ClusterID: getEnvUint64("TIGERBEETLE_CLUSTER_ID", 0),
			Addresses: splitAndTrim(getEnv("TIGERBEETLE_ADDRESSES", "localhost:3000")),
		},

		Auth: AuthConfig{
			JWTSecret:       getEnv("JWT_SECRET", ""),
			AccessTokenTTL:  getEnvDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
			RefreshTokenTTL: getEnvDuration("REFRESH_TOKEN_TTL", 168*time.Hour),
			BcryptCost:      getEnvInt("BCRYPT_COST", 12),
		},

		AI: AIConfig{
			OpenRouterAPIKey:  getEnv("OPENROUTER_API_KEY", ""),
			OpenRouterModel:   getEnv("OPENROUTER_MODEL", "anthropic/claude-sonnet-4.5"),
			OpenRouterBaseURL: getEnv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
		},

		RateLimit: RateLimitConfig{
			GeneralRPM: getEnvInt("RATE_LIMIT_GENERAL_RPM", 120),
			AuthRPM:    getEnvInt("RATE_LIMIT_AUTH_RPM", 10),
		},

		Seed: SeedConfig{
			Enabled:    getEnvBool("SEED_ENABLED", true),
			UserLimit:  getEnvInt("SEED_USER_LIMIT", 0),
			BcryptCost: getEnvInt("SEED_BCRYPT_COST", 10),
			DataPath:   getEnv("SEED_DATA_PATH", "/app/seed-data.json"),
		},

		CORSAllowedOrigins: splitAndTrim(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:5173")),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// IsProduction indica si corremos en producción, para endurecer cookies y CORS.
func (c *Config) IsProduction() bool {
	return c.Env == "production"
}

func (c *Config) validate() error {
	if c.Auth.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET es obligatorio")
	}

	// En producción un secreto corto es inaceptable: se puede romper por fuerza
	// bruta. En desarrollo lo dejamos pasar para no estorbar.
	if c.IsProduction() && len(c.Auth.JWTSecret) < 32 {
		return fmt.Errorf("JWT_SECRET debe tener al menos 32 caracteres en producción")
	}

	if c.Postgres.Password == "" {
		return fmt.Errorf("POSTGRES_PASSWORD es obligatorio")
	}

	// El rango válido de bcrypt es 4-31. Por debajo de 10 el hash es débil.
	if c.Auth.BcryptCost < 10 || c.Auth.BcryptCost > 31 {
		return fmt.Errorf("BCRYPT_COST debe estar entre 10 y 31, se recibió %d", c.Auth.BcryptCost)
	}

	if c.Seed.BcryptCost < 4 || c.Seed.BcryptCost > 31 {
		return fmt.Errorf("SEED_BCRYPT_COST debe estar entre 4 y 31, se recibió %d", c.Seed.BcryptCost)
	}

	if len(c.TigerBeetle.Addresses) == 0 {
		return fmt.Errorf("TIGERBEETLE_ADDRESSES es obligatorio")
	}

	return nil
}

// ------------------------------------------------------------------------------
// Utilidades de lectura del entorno.
//
// Todas aplican un valor por defecto cuando la variable no está definida o
// tiene un formato inválido, salvo las que validate() marca como obligatorias.
// ------------------------------------------------------------------------------

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v, err := strconv.Atoi(getEnv(key, ""))
	if err != nil {
		return fallback
	}
	return v
}

func getEnvUint64(key string, fallback uint64) uint64 {
	v, err := strconv.ParseUint(getEnv(key, ""), 10, 64)
	if err != nil {
		return fallback
	}
	return v
}

func getEnvBool(key string, fallback bool) bool {
	v, err := strconv.ParseBool(getEnv(key, ""))
	if err != nil {
		return fallback
	}
	return v
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v, err := time.ParseDuration(getEnv(key, ""))
	if err != nil {
		return fallback
	}
	return v
}

// splitAndTrim divide una lista separada por comas y descarta los vacíos.
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
