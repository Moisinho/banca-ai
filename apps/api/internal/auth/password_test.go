package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
)

// Costo bajo a propósito: bcrypt está diseñado para ser lento, y con costo 12
// esta suite tardaría minutos. Lo que se verifica acá es la lógica, no la
// dureza del hash.
const testBcryptCost = 4

func TestHashYVerify(t *testing.T) {
	hasher := NewHasher(testBcryptCost)

	hashed, err := hasher.Hash("Isabel2024!")
	require.NoError(t, err)
	require.NotEmpty(t, hashed)

	// El hash nunca puede ser la contraseña en claro.
	assert.NotEqual(t, "Isabel2024!", hashed)

	assert.NoError(t, hasher.Verify(hashed, "Isabel2024!"))
	assert.ErrorIs(t, hasher.Verify(hashed, "contraseña-incorrecta"), domain.ErrInvalidCredentials)
}

// bcrypt incorpora una sal aleatoria, así que la misma contraseña produce
// hashes distintos. Eso impide que dos usuarios con igual contraseña sean
// identificables por su hash, y anula las tablas precomputadas.
func TestHashUsaSalAleatoria(t *testing.T) {
	hasher := NewHasher(testBcryptCost)

	primero, err := hasher.Hash("MismaContraseña123")
	require.NoError(t, err)

	segundo, err := hasher.Hash("MismaContraseña123")
	require.NoError(t, err)

	assert.NotEqual(t, primero, segundo, "cada hash debe usar una sal distinta")

	// Ambos siguen validando la misma contraseña.
	assert.NoError(t, hasher.Verify(primero, "MismaContraseña123"))
	assert.NoError(t, hasher.Verify(segundo, "MismaContraseña123"))
}

func TestHashRechazaContraseñasDemasiadoLargas(t *testing.T) {
	hasher := NewHasher(testBcryptCost)

	// bcrypt trunca en 72 bytes: aceptar más daría una falsa sensación de
	// seguridad, porque los caracteres extra se ignoran.
	demasiadoLarga := strings.Repeat("a", domain.MaxPasswordLength+1)

	_, err := hasher.Hash(demasiadoLarga)
	assert.ErrorIs(t, err, domain.ErrPasswordTooLong)
}

// Verifica que hashes creados con distinto costo conviven.
//
// Es lo que permite sembrar los datos de prueba con un costo menor mientras los
// usuarios registrados por la aplicación usan costo 12: bcrypt guarda el costo
// dentro del propio hash y lo lee al verificar.
func TestHashesDeDistintoCostoConviven(t *testing.T) {
	hasherBajo := NewHasher(4)
	hasherAlto := NewHasher(10)

	hashBajo, err := hasherBajo.Hash("Contraseña123")
	require.NoError(t, err)

	hashAlto, err := hasherAlto.Hash("Contraseña123")
	require.NoError(t, err)

	// Cualquier hasher valida ambos, sin importar con cuál se creó el hash.
	assert.NoError(t, hasherAlto.Verify(hashBajo, "Contraseña123"))
	assert.NoError(t, hasherBajo.Verify(hashAlto, "Contraseña123"))
}

func TestVerifyRechazaHashesMalFormados(t *testing.T) {
	hasher := NewHasher(testBcryptCost)

	// Un hash corrupto no puede provocar un pánico ni aceptar la contraseña.
	assert.ErrorIs(t, hasher.Verify("no-es-un-hash", "cualquier-cosa"), domain.ErrInvalidCredentials)
	assert.ErrorIs(t, hasher.Verify("", "cualquier-cosa"), domain.ErrInvalidCredentials)
}

func TestValidatePassword(t *testing.T) {
	validas := []string{
		"Isabel2024!",
		"contraseña1",
		"abcd1234",
	}

	for _, p := range validas {
		t.Run("válida: "+p, func(t *testing.T) {
			assert.NoError(t, domain.ValidatePassword(p))
		})
	}

	invalidas := []struct {
		nombre   string
		password string
		esperado error
	}{
		{"muy corta", "abc12", domain.ErrPasswordTooShort},
		{"sólo letras", "solamenteletras", domain.ErrPasswordTooWeak},
		{"sólo números", "12345678", domain.ErrPasswordTooWeak},
		{"vacía", "", domain.ErrPasswordTooShort},
		{"demasiado larga", strings.Repeat("a1", 40), domain.ErrPasswordTooLong},
	}

	for _, tc := range invalidas {
		t.Run(tc.nombre, func(t *testing.T) {
			assert.ErrorIs(t, domain.ValidatePassword(tc.password), tc.esperado)
		})
	}
}

func TestValidateEmail(t *testing.T) {
	validos := []string{
		"ihernandez@email.com",
		"antonio.lopez489@test.com",
		"paulamolina@mail.com",
	}

	for _, e := range validos {
		t.Run("válido: "+e, func(t *testing.T) {
			assert.NoError(t, domain.ValidateEmail(e))
		})
	}

	invalidos := []struct {
		nombre   string
		email    string
		esperado error
	}{
		{"vacío", "", domain.ErrEmailRequired},
		{"sin arroba", "noesuncorreo", domain.ErrEmailInvalid},
		{"sin dominio", "usuario@", domain.ErrEmailInvalid},
		{"sólo espacios", "   ", domain.ErrEmailRequired},
	}

	for _, tc := range invalidos {
		t.Run(tc.nombre, func(t *testing.T) {
			assert.ErrorIs(t, domain.ValidateEmail(tc.email), tc.esperado)
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	// El correo se compara en minúsculas: "Ana@Mail.com" y "ana@mail.com" son
	// la misma persona.
	assert.Equal(t, "ana@mail.com", domain.NormalizeEmail("Ana@Mail.com"))
	assert.Equal(t, "ana@mail.com", domain.NormalizeEmail("  ANA@MAIL.COM  "))
}
