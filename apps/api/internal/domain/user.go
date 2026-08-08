package domain

import (
	"net/mail"
	"strings"
	"time"
	"unicode"
)

// User es una persona registrada en el sistema.
//
// PasswordHash nunca se expone hacia afuera: las respuestas HTTP usan un DTO
// aparte que no lo incluye. Tener el campo separado del serializado evita
// filtrarlo por descuido al agregar un endpoint nuevo.
type User struct {
	ID           string
	Email        string
	PasswordHash string
	FullName     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Longitudes mínimas y máximas de la contraseña.
//
// El mínimo de 8 sigue la recomendación de NIST. El máximo existe por bcrypt:
// trunca en 72 bytes, así que aceptar más da una falsa sensación de seguridad.
const (
	MinPasswordLength = 8
	MaxPasswordLength = 72
)

// NormalizeEmail deja el correo en la forma canónica para comparar.
//
// El correo se guarda tal como lo escribió el usuario, pero se compara en
// minúsculas: "Ana@Mail.com" y "ana@mail.com" son la misma persona.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidateEmail verifica que el correo tenga un formato utilizable.
func ValidateEmail(email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return ErrEmailRequired
	}
	if len(email) > 254 {
		return ErrEmailTooLong
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return ErrEmailInvalid
	}
	return nil
}

// ValidatePassword verifica que la contraseña cumpla la política mínima.
//
// Pedimos longitud y algo de variedad, sin exigir símbolos obligatorios:
// obligarlos lleva a contraseñas predecibles del tipo "Password1!", que son
// peores que una frase larga.
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if len(password) > MaxPasswordLength {
		return ErrPasswordTooLong
	}

	var hasLetter, hasDigit bool
	for _, r := range password {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}

	if !hasLetter || !hasDigit {
		return ErrPasswordTooWeak
	}
	return nil
}

// ValidateFullName verifica el nombre completo.
func ValidateFullName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrNameRequired
	}
	if len(name) > 200 {
		return ErrNameTooLong
	}
	return nil
}

// RefreshToken es un token de renovación de sesión.
//
// El sistema usa rotación con detección de reutilización:
//
//  1. Cada token pertenece a una familia (FamilyID).
//  2. Al usarlo, se marca como consumido y se emite uno nuevo de la familia.
//  3. Si alguien intenta usar un token ya consumido, fue robado: se revoca la
//     familia entera y se cierra la sesión.
//
// TokenHash guarda un SHA-256, nunca el token en claro. Si alguien accede a la
// base de datos, los tokens siguen siendo inservibles.
type RefreshToken struct {
	ID        string
	UserID    string
	FamilyID  string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
	UserAgent string
	IPAddress string
}

// Usable indica si el token se puede canjear por uno nuevo.
func (t RefreshToken) Usable() bool {
	return t.UsedAt == nil && t.RevokedAt == nil && time.Now().Before(t.ExpiresAt)
}

// Reused indica que se intentó usar un token ya consumido.
//
// Esto significa robo: el usuario legítimo ya lo canjeó, así que quien lo está
// presentando ahora obtuvo una copia. La respuesta correcta es revocar la
// familia completa y forzar un login nuevo.
func (t RefreshToken) Reused() bool {
	return t.UsedAt != nil && t.RevokedAt == nil
}
