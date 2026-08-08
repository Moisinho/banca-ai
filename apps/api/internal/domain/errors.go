package domain

import "errors"

// Errores del dominio.
//
// Se comparan con errors.Is en la capa HTTP para traducirlos a un código de
// estado y un mensaje para el usuario. El dominio no sabe nada de HTTP: no
// conoce códigos de estado ni formatos de respuesta.
var (
	// Cuentas
	ErrAccountNotFound      = errors.New("la cuenta no existe")
	ErrAccountAlreadyExists = errors.New("la cuenta ya existe")
	ErrAccountClosed        = errors.New("la cuenta está cerrada")

	// Fondos
	ErrInsufficientFunds = errors.New("fondos insuficientes")

	// Transferencias
	ErrSameAccount            = errors.New("la cuenta origen y destino son la misma")
	ErrTransferNotFound       = errors.New("la transferencia no existe")
	ErrTransferNotPending     = errors.New("la transferencia no está pendiente")
	ErrTransferExpired        = errors.New("la transferencia expiró")
	ErrTransferResolved       = errors.New("la transferencia ya fue confirmada o rechazada")
	ErrInvalidTransactionType = errors.New("el tipo de transacción no es válido")

	// Usuarios
	ErrUserNotFound       = errors.New("el usuario no existe")
	ErrEmailAlreadyUsed   = errors.New("el correo ya está registrado")
	ErrInvalidCredentials = errors.New("credenciales inválidas")

	// Autorización
	ErrForbidden    = errors.New("no tenés permiso sobre este recurso")
	ErrUnauthorized = errors.New("necesitás iniciar sesión")

	// Tokens de sesión
	ErrTokenNotFound    = errors.New("el token no existe")
	ErrTokenExpired     = errors.New("el token expiró")
	ErrTokenRevoked     = errors.New("el token fue revocado")
	ErrTokenAlreadyUsed = errors.New("el token ya fue utilizado")

	// ErrTokenReuseDetected indica que se presentó un token ya consumido.
	// Significa robo: el usuario legítimo ya lo canjeó, así que quien lo
	// presenta ahora tiene una copia. Se revoca la familia completa.
	ErrTokenReuseDetected = errors.New("se detectó reutilización de un token")

	// Validación de datos de registro
	ErrEmailRequired    = errors.New("el correo es obligatorio")
	ErrEmailInvalid     = errors.New("el correo no tiene un formato válido")
	ErrEmailTooLong     = errors.New("el correo es demasiado largo")
	ErrPasswordTooShort = errors.New("la contraseña debe tener al menos 8 caracteres")
	ErrPasswordTooLong  = errors.New("la contraseña no puede superar los 72 caracteres")
	ErrPasswordTooWeak  = errors.New("la contraseña debe incluir letras y números")
	ErrNameRequired     = errors.New("el nombre es obligatorio")
	ErrNameTooLong      = errors.New("el nombre es demasiado largo")
)

// ErrorCode traduce un error del dominio al código estable que consume el
// frontend.
//
// El código va en inglés porque es un identificador que el cliente compara;
// el mensaje en español lo arma la capa HTTP. Separarlos permite reescribir
// el texto sin romper al frontend.
func ErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrAccountNotFound):
		return "ACCOUNT_NOT_FOUND"
	case errors.Is(err, ErrAccountAlreadyExists):
		return "ACCOUNT_ALREADY_EXISTS"
	case errors.Is(err, ErrAccountClosed):
		return "ACCOUNT_CLOSED"
	case errors.Is(err, ErrInsufficientFunds):
		return "INSUFFICIENT_FUNDS"
	case errors.Is(err, ErrSameAccount):
		return "SAME_ACCOUNT"
	case errors.Is(err, ErrTransferNotFound):
		return "TRANSFER_NOT_FOUND"
	case errors.Is(err, ErrTransferNotPending):
		return "TRANSFER_NOT_PENDING"
	case errors.Is(err, ErrTransferExpired):
		return "TRANSFER_EXPIRED"
	case errors.Is(err, ErrTransferResolved):
		return "TRANSFER_ALREADY_RESOLVED"
	case errors.Is(err, ErrUserNotFound):
		return "USER_NOT_FOUND"
	case errors.Is(err, ErrEmailAlreadyUsed):
		return "EMAIL_ALREADY_USED"
	case errors.Is(err, ErrInvalidCredentials):
		return "INVALID_CREDENTIALS"
	case errors.Is(err, ErrForbidden):
		return "FORBIDDEN"
	case errors.Is(err, ErrAmountNotPositive):
		return "AMOUNT_NOT_POSITIVE"
	case errors.Is(err, ErrAmountTooLarge):
		return "AMOUNT_TOO_LARGE"
	case errors.Is(err, ErrAmountFormat), errors.Is(err, ErrAmountPrecision):
		return "INVALID_AMOUNT"
	default:
		return "INTERNAL_ERROR"
	}
}
