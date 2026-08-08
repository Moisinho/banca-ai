package auth

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
)

// Hasher aplica bcrypt a las contraseñas.
//
// El costo es configurable porque conviven dos niveles:
//   - Usuarios registrados por la aplicación: costo 12 (contraseñas reales).
//   - Usuarios sembrados de los datos de prueba: costo menor, porque esas
//     contraseñas ya vienen en texto plano en el JSON del repositorio y un
//     costo alto sólo haría lento el primer arranque sin aportar seguridad.
//
// Conviven sin problema: bcrypt guarda el costo dentro del propio hash y lo
// lee al verificar.
type Hasher struct {
	cost int
}

func NewHasher(cost int) *Hasher {
	return &Hasher{cost: cost}
}

// Hash cifra una contraseña.
func (h *Hasher) Hash(password string) (string, error) {
	// bcrypt trunca en 72 bytes. Validamos antes para no dar la falsa
	// sensación de que una contraseña más larga aporta seguridad extra.
	if len(password) > domain.MaxPasswordLength {
		return "", domain.ErrPasswordTooLong
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	if err != nil {
		return "", fmt.Errorf("no se pudo cifrar la contraseña: %w", err)
	}

	return string(hashed), nil
}

// Verify compara una contraseña con su hash.
//
// Devuelve siempre ErrInvalidCredentials ante cualquier fallo, sin distinguir
// si el hash está mal formado o si la contraseña no coincide: informar la
// diferencia le daría pistas a un atacante.
func (h *Hasher) Verify(hashedPassword, password string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password)); err != nil {
		return domain.ErrInvalidCredentials
	}
	return nil
}
