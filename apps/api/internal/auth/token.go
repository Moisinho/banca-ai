// Package auth implementa autenticación con JWT y refresh tokens rotativos.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
)

// Claims son los datos que viajan dentro del access token.
//
// Guardamos lo mínimo: el id del usuario y su correo. Nada sensible, porque un
// JWT va firmado pero NO cifrado: cualquiera que lo intercepte puede leer su
// contenido. La firma garantiza que no fue alterado, no que sea privado.
type Claims struct {
	UserID string `json:"uid"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

// TokenIssuer emite y valida tokens.
type TokenIssuer struct {
	secret          []byte
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
}

func NewTokenIssuer(secret string, accessTTL, refreshTTL time.Duration) *TokenIssuer {
	return &TokenIssuer{
		secret:          []byte(secret),
		accessTokenTTL:  accessTTL,
		refreshTokenTTL: refreshTTL,
	}
}

// AccessTokenTTL expone la duración configurada, para informarla al cliente.
func (t *TokenIssuer) AccessTokenTTL() time.Duration { return t.accessTokenTTL }

// RefreshTokenTTL expone la duración del refresh token.
func (t *TokenIssuer) RefreshTokenTTL() time.Duration { return t.refreshTokenTTL }

// IssueAccessToken firma un access token para un usuario.
//
// Dura poco (15 minutos por defecto) a propósito: si alguien lo roba, la
// ventana de abuso es corta. La sesión larga la sostiene el refresh token,
// que vive en una cookie httpOnly y rota en cada uso.
func (t *TokenIssuer) IssueAccessToken(user domain.User) (string, error) {
	now := time.Now()

	claims := Claims{
		UserID: user.ID,
		Email:  user.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(t.accessTokenTTL)),
			// NotBefore evita que un token con reloj adelantado se acepte antes
			// de tiempo si los servidores están desincronizados.
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "banca-ai",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(t.secret)
	if err != nil {
		return "", fmt.Errorf("no se pudo firmar el access token: %w", err)
	}

	return signed, nil
}

// ParseAccessToken valida la firma y devuelve los claims.
func (t *TokenIssuer) ParseAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(token *jwt.Token) (any, error) {
			// Verificar el algoritmo es obligatorio. Sin esta comprobación, un
			// atacante puede cambiar el header a "none" o a RS256 y hacer que
			// la librería valide con una clave que él controla.
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("algoritmo de firma inesperado: %v", token.Header["alg"])
			}
			return t.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer("banca-ai"),
	)
	if err != nil {
		return nil, domain.ErrUnauthorized
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, domain.ErrUnauthorized
	}

	return claims, nil
}

// refreshTokenBytes es el tamaño del refresh token en bytes.
// 32 bytes (256 bits) hacen inviable adivinarlo por fuerza bruta.
const refreshTokenBytes = 32

// GenerateRefreshToken crea un token aleatorio y devuelve el valor en claro
// junto con su hash.
//
// El valor en claro se le entrega al cliente una única vez; la base guarda sólo
// el hash. Si alguien roba la base de datos, no obtiene tokens utilizables.
func GenerateRefreshToken() (plain string, hashed string, err error) {
	buf := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("no se pudo generar el refresh token: %w", err)
	}

	plain = base64.RawURLEncoding.EncodeToString(buf)
	return plain, HashRefreshToken(plain), nil
}

// HashRefreshToken calcula el hash que se guarda en la base.
//
// SHA-256 es suficiente acá, a diferencia de las contraseñas: el token ya es
// aleatorio de 256 bits, así que no hay nada que un atacante pueda adivinar
// por diccionario. Un hash lento como bcrypt sólo agregaría latencia.
func HashRefreshToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
