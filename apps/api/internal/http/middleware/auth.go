package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/Moisinho/banca-ai/apps/api/internal/auth"
	"github.com/Moisinho/banca-ai/apps/api/internal/http/response"
)

// contextKey es un tipo propio para las claves de contexto, para no colisionar
// con otros paquetes que escriban en el mismo contexto.
type contextKey struct{ name string }

var (
	userIDKey = &contextKey{"user_id"}
	emailKey  = &contextKey{"email"}
)

// Authenticate exige un access token válido.
//
// El token viaja en la cabecera Authorization como "Bearer <token>", no en una
// cookie. Eso lo hace inmune a CSRF: un formulario malicioso en otro sitio
// puede provocar que el navegador envíe cookies, pero no puede fijar cabeceras.
func Authenticate(issuer *auth.TokenIssuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				response.Error(w, r, http.StatusUnauthorized,
					"UNAUTHORIZED", "Necesita iniciar sesión para acceder a este recurso")
				return
			}

			claims, err := issuer.ParseAccessToken(token)
			if err != nil {
				response.Error(w, r, http.StatusUnauthorized,
					"UNAUTHORIZED", "Su sesión expiró. Inicie sesión de nuevo.")
				return
			}

			ctx := context.WithValue(r.Context(), userIDKey, claims.UserID)
			ctx = context.WithValue(ctx, emailKey, claims.Email)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserIDFrom devuelve el id del usuario autenticado.
//
// El segundo valor es false si la petición no pasó por Authenticate. Los
// handlers protegidos deberían tratar ese caso como error de programación,
// no como una petición anónima.
func UserIDFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDKey).(string)
	return id, ok
}

// EmailFrom devuelve el correo del usuario autenticado.
func EmailFrom(ctx context.Context) (string, bool) {
	email, ok := ctx.Value(emailKey).(string)
	return email, ok
}

// bearerToken extrae el token de la cabecera Authorization.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}

	// El prefijo se compara sin distinguir mayúsculas: la especificación dice
	// que el esquema es insensible a mayúsculas.
	const prefix = "bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}

	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}

	return token, true
}
