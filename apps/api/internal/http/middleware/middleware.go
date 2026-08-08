// Package middleware contiene los middlewares HTTP de la aplicación.
package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/Moisinho/banca-ai/apps/api/internal/http/response"
	"github.com/Moisinho/banca-ai/apps/api/internal/logger"
)

// RequestID asigna un identificador único a cada petición.
//
// Si el cliente ya envió uno en la cabecera X-Request-ID lo respetamos, para
// poder seguir una traza que empieza fuera de este servicio.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}

		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(logger.WithRequestID(r.Context(), id)))
	})
}

// statusRecorder envuelve el ResponseWriter para poder leer el código de estado
// después de que el handler respondió. El ResponseWriter estándar no lo expone.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	// Si el handler escribe sin llamar a WriteHeader, Go asume 200.
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += n
	return n, err
}

// RequestLogger registra cada petición con su resultado y duración.
func RequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			log := logger.FromContext(r.Context(), log).With(
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"bytes", rec.bytes,
				"ip", clientIP(r),
			)

			// El nivel del log depende del resultado: un 500 merece más atención
			// que un 200, y así se puede filtrar por severidad en producción.
			switch {
			case rec.status >= 500:
				log.Error("petición fallida por error del servidor")
			case rec.status >= 400:
				log.Warn("petición rechazada")
			default:
				log.Info("petición completada")
			}
		})
	}
}

// Recovery atrapa los panics y los convierte en un 500.
//
// Sin esto, un panic en cualquier handler tira el proceso entero y con él
// todas las peticiones en curso. En una aplicación bancaria eso es inaceptable.
func Recovery(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.FromContext(r.Context(), log).Error("pánico recuperado en el handler",
						"panic", rec,
						"stack", string(debug.Stack()),
						"method", r.Method,
						"path", r.URL.Path,
					)

					// Al cliente no le decimos nada del error interno: filtrar
					// detalles de implementación es un riesgo de seguridad.
					response.Error(w, r, http.StatusInternalServerError,
						"INTERNAL_ERROR", "Ocurrió un error inesperado. Intentá de nuevo.")
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// visitor guarda el limitador de una IP y cuándo se la vio por última vez.
type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// rateLimiter limita peticiones por IP usando token bucket.
type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rpm      int
	burst    int
}

// visitorTTL es cuánto tiempo guardamos el limitador de una IP inactiva.
// Sin esta limpieza el mapa crece sin control y termina siendo una fuga de memoria.
const visitorTTL = 3 * time.Minute

func newRateLimiter(rpm int) *rateLimiter {
	rl := &rateLimiter{
		visitors: make(map[string]*visitor),
		rpm:      rpm,
		// El burst permite ráfagas cortas: cargar un dashboard dispara varias
		// peticiones casi simultáneas y no queremos penalizar eso.
		burst: rpm / 4,
	}
	if rl.burst < 1 {
		rl.burst = 1
	}

	go rl.cleanupLoop()
	return rl
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		v = &visitor{
			limiter: rate.NewLimiter(rate.Limit(float64(rl.rpm)/60.0), rl.burst),
		}
		rl.visitors[ip] = v
	}
	v.lastSeen = time.Now()

	return v.limiter.Allow()
}

// cleanupLoop descarta periódicamente los visitantes inactivos.
func (rl *rateLimiter) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > visitorTTL {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// RateLimit limita las peticiones por IP.
//
// Se aplica un límite general a toda la API y uno más estricto a los endpoints
// de autenticación, que son el blanco natural de un ataque de fuerza bruta.
func RateLimit(rpm int, log *slog.Logger) func(http.Handler) http.Handler {
	limiter := newRateLimiter(rpm)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)

			if !limiter.allow(ip) {
				logger.FromContext(r.Context(), log).Warn("límite de peticiones excedido",
					"ip", ip,
					"path", r.URL.Path,
				)

				w.Header().Set("Retry-After", "60")
				response.Error(w, r, http.StatusTooManyRequests,
					"RATE_LIMIT_EXCEEDED", "Demasiadas peticiones. Esperá un momento e intentá de nuevo.")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// clientIP obtiene la IP real del cliente.
//
// Detrás de un proxy (Railway, nginx) RemoteAddr es la del proxy, no la del
// cliente. X-Forwarded-For trae la cadena completa y el primer valor es el
// origen real.
//
// Nota de seguridad: esta cabecera la puede falsificar el cliente, así que
// sirve para rate limiting y logs, nunca para decisiones de autorización.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// El formato es "cliente, proxy1, proxy2": nos quedamos con el primero.
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return trimSpace(xff[:i])
			}
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

// trimSpace quita espacios al inicio y al final sin importar strings.
func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
