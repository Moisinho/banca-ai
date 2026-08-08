package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Moisinho/banca-ai/apps/api/internal/auth"
	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/http/response"
	"github.com/Moisinho/banca-ai/apps/api/internal/logger"
)

// refreshCookieName es el nombre de la cookie que transporta el refresh token.
const refreshCookieName = "banca_refresh"

// AuthHandler expone los endpoints de autenticación.
type AuthHandler struct {
	service      *auth.Service
	log          *slog.Logger
	secureCookie bool
}

func NewAuthHandler(service *auth.Service, log *slog.Logger, secureCookie bool) *AuthHandler {
	return &AuthHandler{
		service:      service,
		log:          log,
		secureCookie: secureCookie,
	}
}

// Routes monta las rutas de autenticación.
func (h *AuthHandler) Routes(r chi.Router) {
	r.Post("/register", h.register)
	r.Post("/login", h.login)
	r.Post("/refresh", h.refresh)
	r.Post("/logout", h.logout)
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"fullName"`
}

type sessionResponse struct {
	User        userResponse `json:"user"`
	AccessToken string       `json:"accessToken"`
	ExpiresIn   int          `json:"expiresIn"`
}

// userResponse es el usuario tal como se expone hacia afuera.
//
// Es un tipo aparte de domain.User a propósito: así el hash de la contraseña
// no puede filtrarse por descuido al agregar un campo nuevo.
type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	FullName  string    `json:"fullName"`
	CreatedAt time.Time `json:"createdAt"`
}

func toUserResponse(u domain.User) userResponse {
	return userResponse{
		ID:        u.ID,
		Email:     u.Email,
		FullName:  u.FullName,
		CreatedAt: u.CreatedAt,
	}
}

func (h *AuthHandler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	session, err := h.service.Register(r.Context(), auth.RegisterInput{
		Email:     req.Email,
		Password:  req.Password,
		FullName:  req.FullName,
		UserAgent: r.UserAgent(),
		IPAddress: clientIP(r),
	})
	if err != nil {
		h.writeAuthError(w, r, err)
		return
	}

	h.setRefreshCookie(w, session.RefreshToken)
	response.JSON(w, r, http.StatusCreated, sessionResponse{
		User:        toUserResponse(session.User),
		AccessToken: session.AccessToken,
		ExpiresIn:   session.ExpiresIn,
	})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	session, err := h.service.Login(r.Context(), auth.LoginInput{
		Email:     req.Email,
		Password:  req.Password,
		UserAgent: r.UserAgent(),
		IPAddress: clientIP(r),
	})
	if err != nil {
		h.writeAuthError(w, r, err)
		return
	}

	h.setRefreshCookie(w, session.RefreshToken)
	response.JSON(w, r, http.StatusOK, sessionResponse{
		User:        toUserResponse(session.User),
		AccessToken: session.AccessToken,
		ExpiresIn:   session.ExpiresIn,
	})
}

func (h *AuthHandler) refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		response.Error(w, r, http.StatusUnauthorized,
			"UNAUTHORIZED", "Tu sesión expiró. Iniciá sesión de nuevo.")
		return
	}

	session, err := h.service.Refresh(r.Context(), cookie.Value, r.UserAgent(), clientIP(r))
	if err != nil {
		// El refresh falló: la cookie ya no sirve, así que la borramos para
		// que el navegador no siga reintentando con un token muerto.
		h.clearRefreshCookie(w)
		h.writeAuthError(w, r, err)
		return
	}

	h.setRefreshCookie(w, session.RefreshToken)
	response.JSON(w, r, http.StatusOK, sessionResponse{
		User:        toUserResponse(session.User),
		AccessToken: session.AccessToken,
		ExpiresIn:   session.ExpiresIn,
	})
}

func (h *AuthHandler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(refreshCookieName); err == nil {
		if err := h.service.Logout(r.Context(), cookie.Value); err != nil {
			logger.FromContext(r.Context(), h.log).Error("falló el cierre de sesión", "error", err)
		}
	}

	h.clearRefreshCookie(w)
	response.JSON(w, r, http.StatusOK, map[string]string{
		"message": "Sesión cerrada correctamente",
	})
}

// setRefreshCookie escribe la cookie del refresh token.
//
// HttpOnly la hace invisible para JavaScript: aunque haya un XSS, el script no
// puede leer el token. Por eso el refresh va en cookie y el access token en
// memoria, y no al revés.
func (h *AuthHandler) setRefreshCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    token,
		Path:     "/api/v1/auth",
		HttpOnly: true,
		// Secure exige HTTPS. En desarrollo se desactiva porque localhost va
		// por HTTP y el navegador descartaría la cookie.
		Secure:   h.secureCookie,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int((168 * time.Hour).Seconds()),
	})
}

func (h *AuthHandler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// writeAuthError traduce un error del dominio a una respuesta HTTP.
func (h *AuthHandler) writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	code := domain.ErrorCode(err)

	switch {
	case errors.Is(err, domain.ErrEmailAlreadyUsed):
		response.Error(w, r, http.StatusConflict, code, "Ese correo ya está registrado")

	case errors.Is(err, domain.ErrInvalidCredentials):
		// El mismo mensaje para correo inexistente y contraseña incorrecta:
		// distinguirlos revelaría qué correos están registrados.
		response.Error(w, r, http.StatusUnauthorized, code, "El correo o la contraseña no son correctos")

	case errors.Is(err, domain.ErrUnauthorized):
		response.Error(w, r, http.StatusUnauthorized, code, "Tu sesión expiró. Iniciá sesión de nuevo.")

	case errors.Is(err, domain.ErrEmailRequired),
		errors.Is(err, domain.ErrEmailInvalid),
		errors.Is(err, domain.ErrEmailTooLong),
		errors.Is(err, domain.ErrPasswordTooShort),
		errors.Is(err, domain.ErrPasswordTooLong),
		errors.Is(err, domain.ErrPasswordTooWeak),
		errors.Is(err, domain.ErrNameRequired),
		errors.Is(err, domain.ErrNameTooLong):
		response.Error(w, r, http.StatusUnprocessableEntity, code, err.Error())

	default:
		logger.FromContext(r.Context(), h.log).Error("error inesperado en autenticación", "error", err)
		response.Error(w, r, http.StatusInternalServerError,
			"INTERNAL_ERROR", "Ocurrió un error inesperado. Intentá de nuevo.")
	}
}

// maxRequestBody acota el tamaño del cuerpo de una petición.
// Sin este límite, un cuerpo enorme puede agotar la memoria del servidor.
const maxRequestBody = 1 << 20 // 1 MiB

// decodeJSON lee y valida el cuerpo JSON de una petición.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

	decoder := json.NewDecoder(r.Body)
	// Rechaza campos que no existen en la estructura destino: si el cliente
	// manda "passwrod", falla en vez de ignorarlo en silencio.
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		response.Error(w, r, http.StatusBadRequest,
			"INVALID_JSON", "El cuerpo de la petición no es un JSON válido")
		return err
	}

	return nil
}
