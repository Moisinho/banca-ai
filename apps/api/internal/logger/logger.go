// Package logger configura el logging estructurado de la aplicación.
//
// Usamos log/slog de la librería estándar en lugar de una dependencia externa:
// cubre todo lo que necesitamos y evita sumar una librería más al proyecto.
//
// Los mensajes van en español, siguiendo la convención del proyecto. Las claves
// de los campos van en inglés, porque son identificadores que se consultan
// desde herramientas de observabilidad.
package logger

import (
	"context"
	"log/slog"
	"os"
)

// contextKey es un tipo propio para las claves de contexto.
// Usar un tipo propio evita colisiones con otros paquetes que también
// escriban en el mismo contexto.
type contextKey struct{ name string }

var requestIDKey = &contextKey{"request_id"}

// New construye el logger según el entorno.
//
// En producción emite JSON, que es lo que consumen los agregadores de logs.
// En desarrollo emite texto legible por humanos, porque lo lee una persona.
func New(env string) *slog.Logger {
	level := slog.LevelInfo
	if env == "development" {
		level = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{
		Level: level,
		// AddSource en desarrollo: muestra archivo y línea de cada log.
		// En producción se omite porque encarece cada llamada.
		AddSource: env == "development",
	}

	var handler slog.Handler
	if env == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

// WithRequestID guarda el identificador de petición en el contexto.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// RequestIDFrom recupera el identificador de petición del contexto.
// Devuelve cadena vacía si no hay ninguno.
func RequestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// FromContext devuelve un logger que incluye automáticamente el request_id.
//
// Con esto todos los logs de una misma petición quedan correlacionados: cuando
// algo falla en producción, se puede seguir el rastro completo de esa petición.
func FromContext(ctx context.Context, base *slog.Logger) *slog.Logger {
	if id := RequestIDFrom(ctx); id != "" {
		return base.With("request_id", id)
	}
	return base
}
