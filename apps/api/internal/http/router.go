// Package http arma el router HTTP y sus middlewares.
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/Moisinho/banca-ai/apps/api/internal/config"
	"github.com/Moisinho/banca-ai/apps/api/internal/http/middleware"
	"github.com/Moisinho/banca-ai/apps/api/internal/http/response"
)

// NewRouter construye el router con todos los middlewares y rutas.
func NewRouter(cfg *config.Config, log *slog.Logger) http.Handler {
	r := chi.NewRouter()

	// ---------------------------------------------------------------------------
	// Middlewares globales. El orden importa: se ejecutan de arriba hacia abajo
	// en la petición, y en sentido inverso en la respuesta.
	// ---------------------------------------------------------------------------

	// Recovery va primero para que atrape los panics de todo lo que viene después.
	r.Use(middleware.Recovery(log))

	// Asigna un identificador único a cada petición, para poder correlacionar logs.
	r.Use(middleware.RequestID)

	// Registra cada petición con su método, ruta, estado y duración.
	r.Use(middleware.RequestLogger(log))

	// Corta las peticiones que superen el tiempo límite.
	r.Use(chimw.Timeout(30 * time.Second))

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSAllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID"},
		// Necesario para que el navegador envíe la cookie del refresh token.
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Límite general de peticiones por IP.
	r.Use(middleware.RateLimit(cfg.RateLimit.GeneralRPM, log))

	// ---------------------------------------------------------------------------
	// Rutas
	// ---------------------------------------------------------------------------

	// Health check. Queda fuera de /api/v1 porque lo consumen Docker y Railway,
	// no el frontend.
	r.Get("/health", handleHealth)

	r.Route("/api/v1", func(r chi.Router) {
		// Las rutas de autenticación, cuentas, transacciones y chat se montan
		// en las fases siguientes.
		r.Get("/ping", handlePing)
	})

	// Respuestas consistentes para rutas y métodos inexistentes: JSON, no HTML.
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		response.Error(w, r, http.StatusNotFound, "NOT_FOUND", "El recurso solicitado no existe")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		response.Error(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Método no permitido para este recurso")
	})

	return r
}

// handleHealth informa si el servicio está operativo.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, r, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// handlePing sirve para verificar rápidamente que la API responde.
func handlePing(w http.ResponseWriter, r *http.Request) {
	response.JSON(w, r, http.StatusOK, map[string]string{
		"message": "pong",
	})
}
