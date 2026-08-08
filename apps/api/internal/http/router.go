// Package http arma el router HTTP y sus middlewares.
package http

import (
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/Moisinho/banca-ai/apps/api/internal/auth"
	"github.com/Moisinho/banca-ai/apps/api/internal/banking"
	"github.com/Moisinho/banca-ai/apps/api/internal/config"
	"github.com/Moisinho/banca-ai/apps/api/internal/http/middleware"
	"github.com/Moisinho/banca-ai/apps/api/internal/http/response"
)

// Dependencies son los componentes que el router necesita para armar las rutas.
type Dependencies struct {
	AuthService        *auth.Service
	TokenIssuer        *auth.TokenIssuer
	AccountService     *banking.AccountService
	TransactionService *banking.TransactionService
}

// NewRouter construye el router con todos los middlewares y rutas.
func NewRouter(cfg *config.Config, log *slog.Logger, deps Dependencies) http.Handler {
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
		AllowedOrigins: cfg.CORSAllowedOrigins,
		AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type", "X-Request-ID"},
		ExposedHeaders: []string{"X-Request-ID"},
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

	authHandler := NewAuthHandler(deps.AuthService, log, cfg.IsProduction())

	r.Route("/api/v1", func(r chi.Router) {
		// Autenticación con un límite más estricto que el resto de la API:
		// son los endpoints que un atacante golpea para probar contraseñas.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RateLimit(cfg.RateLimit.AuthRPM, log))
			r.Route("/auth", authHandler.Routes)
		})

		// Rutas protegidas: exigen un access token válido.
		r.Group(func(r chi.Router) {
			r.Use(middleware.Authenticate(deps.TokenIssuer))

			r.Get("/me", handleMe)

			bankingHandler := NewBankingHandler(deps.AccountService, deps.TransactionService, log)
			r.Route("/accounts", bankingHandler.AccountRoutes)
			r.Route("/transactions", bankingHandler.TransactionRoutes)

			// El chat con IA se monta en la fase siguiente.
		})
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

// handleMe devuelve la identidad del usuario autenticado.
// Sirve al frontend para verificar que su token sigue siendo válido.
func handleMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFrom(r.Context())
	if !ok {
		response.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Necesitás iniciar sesión")
		return
	}

	email, _ := middleware.EmailFrom(r.Context())

	response.JSON(w, r, http.StatusOK, map[string]string{
		"id":    userID,
		"email": email,
	})
}

// clientIP obtiene la IP real del cliente, considerando proxies.
//
// Nota de seguridad: X-Forwarded-For la puede falsificar el cliente, así que
// sirve para rate limiting y auditoría, nunca para decisiones de autorización.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// El formato es "cliente, proxy1, proxy2": el primero es el origen.
		if idx := indexByte(xff, ','); idx >= 0 {
			return trimSpace(xff[:idx])
		}
		return trimSpace(xff)
	}

	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return trimSpace(xri)
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
