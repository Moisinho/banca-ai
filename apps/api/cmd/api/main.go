// Comando api: punto de entrada del backend de Banca AI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Moisinho/banca-ai/apps/api/internal/adapters/postgres"
	"github.com/Moisinho/banca-ai/apps/api/internal/adapters/tigerbeetle"
	"github.com/Moisinho/banca-ai/apps/api/internal/auth"
	"github.com/Moisinho/banca-ai/apps/api/internal/banking"
	"github.com/Moisinho/banca-ai/apps/api/internal/config"
	httpapi "github.com/Moisinho/banca-ai/apps/api/internal/http"
	"github.com/Moisinho/banca-ai/apps/api/internal/logger"
)

// shutdownTimeout es el tiempo que damos a las peticiones en curso para
// terminar antes de cerrar el servidor a la fuerza.
const shutdownTimeout = 15 * time.Second

func main() {
	// Modo healthcheck: lo usa Docker para saber si el contenedor está sano.
	healthcheck := flag.Bool("healthcheck", false, "verifica que la API responda y termina")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	if err := run(); err != nil {
		// Si la configuración falla todavía no hay logger, así que escribimos
		// directo a stderr.
		fmt.Fprintf(os.Stderr, "error fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("no se pudo cargar la configuración: %w", err)
	}

	log := logger.New(cfg.Env)
	log.Info("iniciando Banca AI",
		"env", cfg.Env,
		"port", cfg.Port,
		"ai_enabled", cfg.AI.Enabled(),
	)

	if !cfg.AI.Enabled() {
		log.Warn("el chat con IA está deshabilitado: falta OPENROUTER_API_KEY")
	}

	// El contexto de arranque acota cuánto esperamos a las dependencias.
	// Sin límite, un Postgres que nunca levanta dejaría el proceso colgado.
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelStartup()

	// ---------------------------------------------------------------------------
	// PostgreSQL — usuarios y autenticación
	// ---------------------------------------------------------------------------
	pool, err := postgres.Connect(startupCtx, postgres.DefaultConfig(cfg.Postgres.DSN()), log)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := postgres.Migrate(startupCtx, pool, log); err != nil {
		return err
	}

	// ---------------------------------------------------------------------------
	// TigerBeetle — operaciones financieras
	// ---------------------------------------------------------------------------
	ledger, err := tigerbeetle.New(cfg.TigerBeetle.ClusterID, cfg.TigerBeetle.Addresses, log)
	if err != nil {
		return err
	}
	defer func() { _ = ledger.Close() }()

	// La cuenta del operador representa al mundo exterior: es la contraparte de
	// todo depósito y retiro. Sin ella no se puede mover dinero, así que se
	// crea al arrancar.
	if err := ledger.EnsureOperatorAccount(startupCtx); err != nil {
		return err
	}

	// ---------------------------------------------------------------------------
	// Composición de dependencias
	// ---------------------------------------------------------------------------
	userRepo := postgres.NewUserRepository(pool)
	accountRepo := postgres.NewAccountRepository(pool)
	refreshTokenRepo := postgres.NewRefreshTokenRepository(pool)
	metadataRepo := postgres.NewTransactionMetadataRepository(pool)

	hasher := auth.NewHasher(cfg.Auth.BcryptCost)
	issuer := auth.NewTokenIssuer(cfg.Auth.JWTSecret, cfg.Auth.AccessTokenTTL, cfg.Auth.RefreshTokenTTL)

	authService := auth.NewService(userRepo, accountRepo, refreshTokenRepo, ledger, hasher, issuer, log)
	accountService := banking.NewAccountService(accountRepo, ledger, log)
	transactionService := banking.NewTransactionService(accountRepo, ledger, metadataRepo, log)

	router := httpapi.NewRouter(cfg, log, httpapi.Dependencies{
		AuthService:        authService,
		TokenIssuer:        issuer,
		AccountService:     accountService,
		TransactionService: transactionService,
	})

	// ---------------------------------------------------------------------------
	// Servidor HTTP
	// ---------------------------------------------------------------------------
	srv := &http.Server{
		Addr:    net.JoinHostPort("", cfg.Port),
		Handler: router,

		// Timeouts explícitos. Sin ellos, una conexión lenta o maliciosa puede
		// mantener recursos del servidor ocupados indefinidamente.
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Info("servidor HTTP escuchando", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	// Apagado ordenado: esperamos SIGINT (Ctrl+C) o SIGTERM (docker stop).
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return fmt.Errorf("el servidor falló: %w", err)

	case sig := <-shutdown:
		log.Info("señal de apagado recibida, cerrando ordenadamente", "signal", sig.String())

		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		// Shutdown deja terminar las peticiones en curso antes de cerrar. En una
		// aplicación bancaria esto importa: cortar una transferencia a la mitad
		// no es aceptable.
		if err := srv.Shutdown(ctx); err != nil {
			log.Error("el apagado ordenado falló, cerrando a la fuerza", "error", err)
			_ = srv.Close()
			return fmt.Errorf("apagado forzado: %w", err)
		}

		log.Info("servidor cerrado correctamente")
		return nil
	}
}

// runHealthcheck consulta el endpoint de salud local.
// Devuelve el código de salida que espera Docker: 0 sano, 1 enfermo.
func runHealthcheck() int {
	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8080"
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://localhost:" + port + "/health")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck falló: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck devolvió estado %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
