package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// Service orquesta el registro, el inicio de sesión y la renovación de tokens.
type Service struct {
	users     ports.UserRepository
	accounts  ports.AccountRepository
	tokens    ports.RefreshTokenRepository
	ledger    ports.Ledger
	hasher    *Hasher
	issuer    *TokenIssuer
	log       *slog.Logger
}

func NewService(
	users ports.UserRepository,
	accounts ports.AccountRepository,
	tokens ports.RefreshTokenRepository,
	ledger ports.Ledger,
	hasher *Hasher,
	issuer *TokenIssuer,
	log *slog.Logger,
) *Service {
	return &Service{
		users:    users,
		accounts: accounts,
		tokens:   tokens,
		ledger:   ledger,
		hasher:   hasher,
		issuer:   issuer,
		log:      log,
	}
}

// Session es el resultado de un login o una renovación.
type Session struct {
	User         domain.User
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

// RegisterInput son los datos de registro.
type RegisterInput struct {
	Email     string
	Password  string
	FullName  string
	UserAgent string
	IPAddress string
}

// Register crea un usuario y su primera cuenta bancaria.
//
// La prueba pide explícitamente "Crear cuenta bancaria al registrar usuario",
// así que el registro también da de alta la cuenta en TigerBeetle.
func (s *Service) Register(ctx context.Context, in RegisterInput) (Session, error) {
	if err := domain.ValidateEmail(in.Email); err != nil {
		return Session{}, err
	}
	if err := domain.ValidatePassword(in.Password); err != nil {
		return Session{}, err
	}
	if err := domain.ValidateFullName(in.FullName); err != nil {
		return Session{}, err
	}

	passwordHash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return Session{}, err
	}

	user, err := s.users.Create(ctx, domain.User{
		Email:        domain.NormalizeEmail(in.Email),
		PasswordHash: passwordHash,
		FullName:     in.FullName,
	})
	if err != nil {
		return Session{}, err
	}

	if _, err := s.createAccountFor(ctx, user.ID, domain.AccountTypeChecking); err != nil {
		// El usuario ya existe pero se quedó sin cuenta. No es un estado
		// irrecuperable (se le puede crear después), pero sí digno de alerta.
		s.log.Error("el usuario se creó pero falló la creación de su cuenta",
			"user_id", user.ID,
			"error", err,
		)
		return Session{}, fmt.Errorf("no se pudo crear la cuenta bancaria: %w", err)
	}

	s.log.Info("usuario registrado", "user_id", user.ID)

	return s.issueSession(ctx, user, uuid.NewString(), in.UserAgent, in.IPAddress)
}

// LoginInput son las credenciales de inicio de sesión.
type LoginInput struct {
	Email     string
	Password  string
	UserAgent string
	IPAddress string
}

// Login valida las credenciales y abre una sesión.
func (s *Service) Login(ctx context.Context, in LoginInput) (Session, error) {
	user, err := s.users.FindByEmail(ctx, in.Email)
	if err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			// Ejecutamos igual una verificación de contraseña contra un hash
			// ficticio. Sin esto, responder al instante cuando el correo no
			// existe revela qué correos están registrados: el atacante mide el
			// tiempo de respuesta y enumera usuarios.
			s.dummyPasswordCheck(in.Password)
			return Session{}, domain.ErrInvalidCredentials
		}
		return Session{}, err
	}

	if err := s.hasher.Verify(user.PasswordHash, in.Password); err != nil {
		s.log.Warn("intento de inicio de sesión fallido",
			"user_id", user.ID,
			"ip", in.IPAddress,
		)
		return Session{}, domain.ErrInvalidCredentials
	}

	s.log.Info("inicio de sesión exitoso", "user_id", user.ID)

	// Cada login abre una familia de tokens nueva: las sesiones de distintos
	// dispositivos son independientes y revocar una no afecta a las otras.
	return s.issueSession(ctx, user, uuid.NewString(), in.UserAgent, in.IPAddress)
}

// Refresh canjea un refresh token por un par nuevo.
//
// Acá vive la detección de robo. Si el token presentado ya fue consumido,
// significa que alguien tiene una copia: se revoca la familia completa y se
// obliga a iniciar sesión de nuevo.
func (s *Service) Refresh(ctx context.Context, plainToken, userAgent, ipAddress string) (Session, error) {
	tokenHash := HashRefreshToken(plainToken)

	stored, err := s.tokens.FindByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, domain.ErrTokenNotFound) {
			return Session{}, domain.ErrUnauthorized
		}
		return Session{}, err
	}

	// Token ya consumido: es robo.
	//
	// El usuario legítimo lo canjeó y recibió uno nuevo. Quien lo presenta
	// ahora obtuvo una copia, así que cerramos la cadena entera. Es preferible
	// forzar un login a permitir que un atacante siga renovando la sesión.
	if stored.Reused() {
		s.log.Warn("reutilización de refresh token detectada, se revoca la familia",
			"user_id", stored.UserID,
			"family_id", stored.FamilyID,
			"ip", ipAddress,
		)

		if err := s.tokens.RevokeFamily(ctx, stored.FamilyID); err != nil {
			s.log.Error("no se pudo revocar la familia de tokens",
				"family_id", stored.FamilyID,
				"error", err,
			)
		}

		return Session{}, domain.ErrUnauthorized
	}

	if stored.RevokedAt != nil {
		return Session{}, domain.ErrUnauthorized
	}
	if time.Now().After(stored.ExpiresAt) {
		return Session{}, domain.ErrUnauthorized
	}

	// Marcar como usado es atómico: si dos peticiones concurrentes intentan
	// canjear el mismo token, sólo una lo consigue.
	if err := s.tokens.MarkUsed(ctx, tokenHash); err != nil {
		if errors.Is(err, domain.ErrTokenAlreadyUsed) {
			// Otra petición ganó la carrera. Tratado como reutilización.
			_ = s.tokens.RevokeFamily(ctx, stored.FamilyID)
			return Session{}, domain.ErrUnauthorized
		}
		return Session{}, err
	}

	user, err := s.users.FindByID(ctx, stored.UserID)
	if err != nil {
		return Session{}, err
	}

	// El token nuevo hereda la familia: es la continuación de esta sesión.
	return s.issueSession(ctx, user, stored.FamilyID, userAgent, ipAddress)
}

// Logout revoca el refresh token presentado.
//
// Sólo se revoca esa familia, no todas: cerrar sesión en un dispositivo no
// debería desconectar los demás.
func (s *Service) Logout(ctx context.Context, plainToken string) error {
	if plainToken == "" {
		return nil
	}

	stored, err := s.tokens.FindByHash(ctx, HashRefreshToken(plainToken))
	if err != nil {
		// Un token inexistente no es un error para el usuario: el objetivo de
		// cerrar sesión ya está cumplido.
		if errors.Is(err, domain.ErrTokenNotFound) {
			return nil
		}
		return err
	}

	return s.tokens.RevokeFamily(ctx, stored.FamilyID)
}

// issueSession emite el par de tokens y persiste el refresh.
func (s *Service) issueSession(ctx context.Context, user domain.User, familyID, userAgent, ipAddress string) (Session, error) {
	accessToken, err := s.issuer.IssueAccessToken(user)
	if err != nil {
		return Session{}, err
	}

	plainRefresh, hashedRefresh, err := GenerateRefreshToken()
	if err != nil {
		return Session{}, err
	}

	err = s.tokens.Store(ctx, domain.RefreshToken{
		UserID:    user.ID,
		FamilyID:  familyID,
		TokenHash: hashedRefresh,
		ExpiresAt: time.Now().Add(s.issuer.RefreshTokenTTL()),
		UserAgent: userAgent,
		IPAddress: ipAddress,
	})
	if err != nil {
		return Session{}, err
	}

	return Session{
		User:         user,
		AccessToken:  accessToken,
		RefreshToken: plainRefresh,
		ExpiresIn:    int(s.issuer.AccessTokenTTL().Seconds()),
	}, nil
}

// createAccountFor da de alta una cuenta en Postgres y en TigerBeetle.
func (s *Service) createAccountFor(ctx context.Context, userID string, accountType domain.AccountType) (domain.Account, error) {
	accountNumber, err := s.accounts.NextAccountNumber(ctx)
	if err != nil {
		return domain.Account{}, err
	}

	tigerBeetleID, err := newTigerBeetleID()
	if err != nil {
		return domain.Account{}, err
	}

	// Primero TigerBeetle: si falla, no queda una fila en Postgres apuntando a
	// una cuenta contable inexistente. Al revés el estado sería inconsistente.
	if err := s.ledger.CreateAccount(ctx, tigerBeetleID, accountType); err != nil {
		return domain.Account{}, err
	}

	return s.accounts.Create(ctx, domain.Account{
		UserID:        userID,
		AccountNumber: accountNumber,
		TigerBeetleID: tigerBeetleID,
		Type:          accountType,
		Currency:      "USD",
	})
}

// newTigerBeetleID genera un identificador de 128 bits para una cuenta.
//
// Se generan aleatorios en lugar de secuenciales para que un id no revele
// cuántas cuentas hay ni permita adivinar el de otra persona.
//
// Se reservan los valores bajos (menores a 1000) para cuentas del sistema,
// como la del operador.
func newTigerBeetleID() (*big.Int, error) {
	// 2^128 - 1000, para dejar libres los ids reservados.
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	max.Sub(max, big.NewInt(1000))

	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return nil, fmt.Errorf("no se pudo generar el identificador de cuenta: %w", err)
	}

	return n.Add(n, big.NewInt(1000)), nil
}

// dummyHash es un hash bcrypt válido de una contraseña arbitraria.
//
// Se usa para que un login con correo inexistente tarde lo mismo que uno con
// correo real. Sin esto, la diferencia de tiempo permite enumerar usuarios.
const dummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

func (s *Service) dummyPasswordCheck(password string) {
	_ = s.hasher.Verify(dummyHash, password)
}
