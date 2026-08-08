package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
)

const testSecret = "un_secreto_de_prueba_suficientemente_largo_para_hs256"

func newTestIssuer() *TokenIssuer {
	return NewTokenIssuer(testSecret, 15*time.Minute, 168*time.Hour)
}

func TestAccessTokenIdaYVuelta(t *testing.T) {
	issuer := newTestIssuer()
	user := domain.User{ID: "user-123", Email: "ana@example.com"}

	token, err := issuer.IssueAccessToken(user)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := issuer.ParseAccessToken(token)
	require.NoError(t, err)

	assert.Equal(t, user.ID, claims.UserID)
	assert.Equal(t, user.Email, claims.Email)
	assert.Equal(t, user.ID, claims.Subject)
}

func TestAccessTokenConFirmaInvalidaSeRechaza(t *testing.T) {
	issuer := newTestIssuer()
	user := domain.User{ID: "user-123", Email: "ana@example.com"}

	token, err := issuer.IssueAccessToken(user)
	require.NoError(t, err)

	// Un emisor con otro secreto no puede validar este token.
	otroEmisor := NewTokenIssuer("otro_secreto_completamente_distinto", 15*time.Minute, time.Hour)

	_, err = otroEmisor.ParseAccessToken(token)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestAccessTokenExpiradoSeRechaza(t *testing.T) {
	// TTL negativo: nace vencido.
	issuer := NewTokenIssuer(testSecret, -time.Minute, time.Hour)

	token, err := issuer.IssueAccessToken(domain.User{ID: "user-123", Email: "ana@example.com"})
	require.NoError(t, err)

	_, err = issuer.ParseAccessToken(token)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

// Verifica la defensa contra el ataque de algoritmo "none".
//
// Si la validación no comprueba el método de firma, un atacante puede armar un
// token sin firma, declarar alg=none y hacerse pasar por cualquier usuario.
func TestTokenConAlgoritmoNoneSeRechaza(t *testing.T) {
	issuer := newTestIssuer()

	claims := Claims{
		UserID: "atacante",
		Email:  "atacante@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "atacante",
			Issuer:    "banca-ai",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	unsigned, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = issuer.ParseAccessToken(unsigned)
	assert.ErrorIs(t, err, domain.ErrUnauthorized, "un token sin firma nunca puede aceptarse")
}

func TestTokenMalformadoSeRechaza(t *testing.T) {
	issuer := newTestIssuer()

	entradas := []string{
		"",
		"no-es-un-token",
		"a.b.c",
		"Bearer algo",
	}

	for _, entrada := range entradas {
		t.Run(entrada, func(t *testing.T) {
			_, err := issuer.ParseAccessToken(entrada)
			assert.ErrorIs(t, err, domain.ErrUnauthorized)
		})
	}
}

func TestRefreshTokenEsAleatorio(t *testing.T) {
	vistos := make(map[string]bool)

	// Si dos tokens coincidieran, el generador no sería seguro.
	for i := 0; i < 100; i++ {
		plain, hashed, err := GenerateRefreshToken()
		require.NoError(t, err)
		require.NotEmpty(t, plain)
		require.NotEmpty(t, hashed)

		assert.False(t, vistos[plain], "se generó un refresh token repetido")
		vistos[plain] = true
	}
}

func TestHashRefreshTokenEsDeterminista(t *testing.T) {
	plain, hashed, err := GenerateRefreshToken()
	require.NoError(t, err)

	// El mismo valor siempre da el mismo hash: así se puede buscar en la base.
	assert.Equal(t, hashed, HashRefreshToken(plain))

	// Y el hash no revela el token original.
	assert.NotEqual(t, plain, hashed)
}

func TestHashRefreshTokenDistinguevalores(t *testing.T) {
	assert.NotEqual(t, HashRefreshToken("token-a"), HashRefreshToken("token-b"))
}
