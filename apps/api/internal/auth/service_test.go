package auth

import (
	"context"
	"io"
	"log/slog"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// Dobles en memoria de los repositorios.
//
// Testear el servicio con implementaciones falsas es exactamente lo que habilita
// la arquitectura hexagonal: la lógica de rotación se verifica sin levantar
// Postgres ni TigerBeetle, y la suite corre en milisegundos.

type fakeUserRepo struct {
	mu     sync.Mutex
	byID   map[string]domain.User
	byMail map[string]domain.User
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{
		byID:   make(map[string]domain.User),
		byMail: make(map[string]domain.User),
	}
}

func (f *fakeUserRepo) Create(_ context.Context, user domain.User) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	email := domain.NormalizeEmail(user.Email)
	if _, exists := f.byMail[email]; exists {
		return domain.User{}, domain.ErrEmailAlreadyUsed
	}

	user.ID = uuid.NewString()
	user.CreatedAt = time.Now()
	user.UpdatedAt = user.CreatedAt

	f.byID[user.ID] = user
	f.byMail[email] = user
	return user, nil
}

func (f *fakeUserRepo) FindByID(_ context.Context, id string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	user, ok := f.byID[id]
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return user, nil
}

func (f *fakeUserRepo) FindByEmail(_ context.Context, email string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	user, ok := f.byMail[domain.NormalizeEmail(email)]
	if !ok {
		return domain.User{}, domain.ErrUserNotFound
	}
	return user, nil
}

func (f *fakeUserRepo) EmailExists(_ context.Context, email string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	_, ok := f.byMail[domain.NormalizeEmail(email)]
	return ok, nil
}

type fakeAccountRepo struct {
	mu       sync.Mutex
	accounts map[string]domain.Account
	counter  int
}

func newFakeAccountRepo() *fakeAccountRepo {
	return &fakeAccountRepo{accounts: make(map[string]domain.Account)}
}

func (f *fakeAccountRepo) Create(_ context.Context, account domain.Account) (domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	account.ID = uuid.NewString()
	account.CreatedAt = time.Now()
	f.accounts[account.ID] = account
	return account, nil
}

func (f *fakeAccountRepo) FindByID(_ context.Context, id string) (domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	account, ok := f.accounts[id]
	if !ok {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	return account, nil
}

func (f *fakeAccountRepo) FindByNumber(_ context.Context, number string) (domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, account := range f.accounts {
		if account.AccountNumber == number {
			return account, nil
		}
	}
	return domain.Account{}, domain.ErrAccountNotFound
}

func (f *fakeAccountRepo) ListByUser(_ context.Context, userID string) ([]domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []domain.Account
	for _, account := range f.accounts {
		if account.UserID == userID {
			out = append(out, account)
		}
	}
	return out, nil
}

func (f *fakeAccountRepo) NextAccountNumber(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.counter++
	return "4001-0000-0000-" + padLeft(f.counter), nil
}

func padLeft(n int) string {
	s := ""
	for _, c := range []byte{byte('0' + (n/1000)%10), byte('0' + (n/100)%10), byte('0' + (n/10)%10), byte('0' + n%10)} {
		s += string(c)
	}
	return s
}

type fakeTokenRepo struct {
	mu     sync.Mutex
	tokens map[string]domain.RefreshToken
}

func newFakeTokenRepo() *fakeTokenRepo {
	return &fakeTokenRepo{tokens: make(map[string]domain.RefreshToken)}
}

func (f *fakeTokenRepo) Store(_ context.Context, token domain.RefreshToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	token.ID = uuid.NewString()
	token.CreatedAt = time.Now()
	f.tokens[token.TokenHash] = token
	return nil
}

func (f *fakeTokenRepo) FindByHash(_ context.Context, hash string) (domain.RefreshToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	token, ok := f.tokens[hash]
	if !ok {
		return domain.RefreshToken{}, domain.ErrTokenNotFound
	}
	return token, nil
}

func (f *fakeTokenRepo) MarkUsed(_ context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	token, ok := f.tokens[hash]
	if !ok {
		return domain.ErrTokenNotFound
	}
	// Reproduce la atomicidad del UPDATE ... WHERE used_at IS NULL.
	if token.UsedAt != nil {
		return domain.ErrTokenAlreadyUsed
	}

	now := time.Now()
	token.UsedAt = &now
	f.tokens[hash] = token
	return nil
}

func (f *fakeTokenRepo) RevokeFamily(_ context.Context, familyID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now()
	for hash, token := range f.tokens {
		if token.FamilyID == familyID && token.RevokedAt == nil {
			token.RevokedAt = &now
			f.tokens[hash] = token
		}
	}
	return nil
}

func (f *fakeTokenRepo) RevokeAllForUser(_ context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now()
	for hash, token := range f.tokens {
		if token.UserID == userID && token.RevokedAt == nil {
			token.RevokedAt = &now
			f.tokens[hash] = token
		}
	}
	return nil
}

// fakeLedger sustituye a TigerBeetle. Sólo registra las cuentas creadas: lo que
// se está probando acá es la autenticación, no la contabilidad.
type fakeLedger struct {
	mu       sync.Mutex
	accounts map[string]bool
}

func newFakeLedger() *fakeLedger {
	return &fakeLedger{accounts: make(map[string]bool)}
}

func (f *fakeLedger) CreateAccount(_ context.Context, id *big.Int, _ domain.AccountType) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.accounts[id.String()] = true
	return nil
}

func (f *fakeLedger) GetBalance(context.Context, *big.Int) (domain.Balance, error) {
	return domain.Balance{}, nil
}

func (f *fakeLedger) GetBalances(context.Context, []*big.Int) (map[string]domain.Balance, error) {
	return map[string]domain.Balance{}, nil
}

func (f *fakeLedger) Transfer(context.Context, domain.TransferRequest) (*big.Int, error) {
	return big.NewInt(1), nil
}

func (f *fakeLedger) PostPending(context.Context, *big.Int) error { return nil }
func (f *fakeLedger) VoidPending(context.Context, *big.Int) error { return nil }

func (f *fakeLedger) ListTransfers(context.Context, *big.Int, ports.TransferFilter) ([]domain.Transaction, error) {
	return nil, nil
}

func (f *fakeLedger) LookupTransfer(context.Context, *big.Int) (domain.Transaction, error) {
	return domain.Transaction{}, domain.ErrTransferNotFound
}

func (f *fakeLedger) Close() error { return nil }

// newTestService arma el servicio con todos los dobles.
func newTestService(t *testing.T) (*Service, *fakeTokenRepo, *fakeLedger) {
	t.Helper()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokenRepo := newFakeTokenRepo()
	ledger := newFakeLedger()

	service := NewService(
		newFakeUserRepo(),
		newFakeAccountRepo(),
		tokenRepo,
		ledger,
		NewHasher(testBcryptCost),
		NewTokenIssuer(testSecret, 15*time.Minute, 168*time.Hour),
		log,
	)

	return service, tokenRepo, ledger
}

func TestRegisterCreaUsuarioYCuentaBancaria(t *testing.T) {
	service, _, ledger := newTestService(t)
	ctx := context.Background()

	session, err := service.Register(ctx, RegisterInput{
		Email:    "isabel@example.com",
		Password: "Isabel2024!",
		FullName: "Isabel Hernández Álvarez",
	})
	require.NoError(t, err)

	assert.NotEmpty(t, session.User.ID)
	assert.Equal(t, "isabel@example.com", session.User.Email)
	assert.NotEmpty(t, session.AccessToken)
	assert.NotEmpty(t, session.RefreshToken)

	// La prueba pide que registrarse cree la cuenta bancaria.
	assert.Len(t, ledger.accounts, 1, "el registro debe crear una cuenta en el libro contable")

	// La contraseña nunca puede viajar en claro dentro de la sesión.
	assert.NotEqual(t, "Isabel2024!", session.User.PasswordHash)
}

func TestRegisterRechazaCorreoDuplicado(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := service.Register(ctx, RegisterInput{
		Email:    "isabel@example.com",
		Password: "Isabel2024!",
		FullName: "Isabel Hernández",
	})
	require.NoError(t, err)

	// Mismo correo con otra capitalización: sigue siendo la misma persona.
	_, err = service.Register(ctx, RegisterInput{
		Email:    "ISABEL@Example.com",
		Password: "Otra2024!",
		FullName: "Impostor",
	})
	assert.ErrorIs(t, err, domain.ErrEmailAlreadyUsed)
}

func TestRegisterValidaLosDatos(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	casos := []struct {
		nombre   string
		input    RegisterInput
		esperado error
	}{
		{
			"correo inválido",
			RegisterInput{Email: "no-es-correo", Password: "Valida123", FullName: "Test"},
			domain.ErrEmailInvalid,
		},
		{
			"contraseña débil",
			RegisterInput{Email: "a@b.com", Password: "solotexto", FullName: "Test"},
			domain.ErrPasswordTooWeak,
		},
		{
			"nombre vacío",
			RegisterInput{Email: "a@b.com", Password: "Valida123", FullName: ""},
			domain.ErrNameRequired,
		},
	}

	for _, tc := range casos {
		t.Run(tc.nombre, func(t *testing.T) {
			_, err := service.Register(ctx, tc.input)
			assert.ErrorIs(t, err, tc.esperado)
		})
	}
}

func TestLoginConCredencialesCorrectas(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := service.Register(ctx, RegisterInput{
		Email:    "isabel@example.com",
		Password: "Isabel2024!",
		FullName: "Isabel Hernández",
	})
	require.NoError(t, err)

	session, err := service.Login(ctx, LoginInput{
		Email:    "isabel@example.com",
		Password: "Isabel2024!",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, session.AccessToken)
	assert.NotEmpty(t, session.RefreshToken)
}

func TestLoginRechazaCredencialesIncorrectas(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := service.Register(ctx, RegisterInput{
		Email:    "isabel@example.com",
		Password: "Isabel2024!",
		FullName: "Isabel Hernández",
	})
	require.NoError(t, err)

	t.Run("contraseña incorrecta", func(t *testing.T) {
		_, err := service.Login(ctx, LoginInput{
			Email:    "isabel@example.com",
			Password: "no-es-la-contraseña",
		})
		assert.ErrorIs(t, err, domain.ErrInvalidCredentials)
	})

	// El mismo error para un correo inexistente: distinguirlos permitiría
	// enumerar qué correos están registrados.
	t.Run("correo inexistente", func(t *testing.T) {
		_, err := service.Login(ctx, LoginInput{
			Email:    "nadie@example.com",
			Password: "Isabel2024!",
		})
		assert.ErrorIs(t, err, domain.ErrInvalidCredentials)
	})
}

func TestRefreshRotaElToken(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	original, err := service.Register(ctx, RegisterInput{
		Email:    "isabel@example.com",
		Password: "Isabel2024!",
		FullName: "Isabel Hernández",
	})
	require.NoError(t, err)

	renovada, err := service.Refresh(ctx, original.RefreshToken, "", "")
	require.NoError(t, err)

	// El token nuevo tiene que ser distinto: en eso consiste la rotación.
	assert.NotEqual(t, original.RefreshToken, renovada.RefreshToken)
	assert.NotEmpty(t, renovada.AccessToken)
	assert.Equal(t, original.User.ID, renovada.User.ID)
}

// Este es el test central del bonus de autenticación adicional.
//
// Si alguien roba un refresh token y el usuario legítimo ya lo canjeó, el
// intento del atacante debe cerrar la sesión entera en lugar de darle acceso.
func TestRefreshDetectaReutilizacionYRevocaLaFamilia(t *testing.T) {
	service, tokenRepo, _ := newTestService(t)
	ctx := context.Background()

	session, err := service.Register(ctx, RegisterInput{
		Email:    "isabel@example.com",
		Password: "Isabel2024!",
		FullName: "Isabel Hernández",
	})
	require.NoError(t, err)

	tokenRobado := session.RefreshToken

	// El usuario legítimo renueva: el token robado queda consumido.
	renovada, err := service.Refresh(ctx, tokenRobado, "", "")
	require.NoError(t, err)

	// El atacante intenta usar su copia.
	_, err = service.Refresh(ctx, tokenRobado, "", "")
	assert.ErrorIs(t, err, domain.ErrUnauthorized, "un token reutilizado debe rechazarse")

	// Y la sesión legítima también queda revocada: ante un robo, cortamos todo
	// y obligamos a iniciar sesión de nuevo.
	_, err = service.Refresh(ctx, renovada.RefreshToken, "", "")
	assert.ErrorIs(t, err, domain.ErrUnauthorized,
		"detectar el robo debe revocar la familia completa, no sólo el token robado")

	// Verificación directa sobre el repositorio.
	stored, err := tokenRepo.FindByHash(ctx, HashRefreshToken(renovada.RefreshToken))
	require.NoError(t, err)
	assert.NotNil(t, stored.RevokedAt, "todos los tokens de la familia deben quedar revocados")
}

func TestRefreshRechazaTokenInexistente(t *testing.T) {
	service, _, _ := newTestService(t)

	_, err := service.Refresh(context.Background(), "token-que-nunca-existio", "", "")
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestRefreshRechazaTokenExpirado(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokenRepo := newFakeTokenRepo()

	// TTL negativo: el refresh nace vencido.
	service := NewService(
		newFakeUserRepo(),
		newFakeAccountRepo(),
		tokenRepo,
		newFakeLedger(),
		NewHasher(testBcryptCost),
		NewTokenIssuer(testSecret, 15*time.Minute, -time.Hour),
		log,
	)

	ctx := context.Background()
	session, err := service.Register(ctx, RegisterInput{
		Email:    "isabel@example.com",
		Password: "Isabel2024!",
		FullName: "Isabel Hernández",
	})
	require.NoError(t, err)

	_, err = service.Refresh(ctx, session.RefreshToken, "", "")
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestLogoutRevocaLaSesion(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	session, err := service.Register(ctx, RegisterInput{
		Email:    "isabel@example.com",
		Password: "Isabel2024!",
		FullName: "Isabel Hernández",
	})
	require.NoError(t, err)

	require.NoError(t, service.Logout(ctx, session.RefreshToken))

	// Tras cerrar sesión el token ya no sirve.
	_, err = service.Refresh(ctx, session.RefreshToken, "", "")
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestLogoutConTokenVacioNoFalla(t *testing.T) {
	service, _, _ := newTestService(t)

	// Cerrar sesión sin cookie no es un error: el objetivo ya está cumplido.
	assert.NoError(t, service.Logout(context.Background(), ""))
}

// Verifica que dos sesiones del mismo usuario son independientes.
//
// Cerrar sesión en el teléfono no debería desconectar la computadora.
func TestSesionesEnDistintosDispositivosSonIndependientes(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()

	_, err := service.Register(ctx, RegisterInput{
		Email:    "isabel@example.com",
		Password: "Isabel2024!",
		FullName: "Isabel Hernández",
	})
	require.NoError(t, err)

	telefono, err := service.Login(ctx, LoginInput{
		Email: "isabel@example.com", Password: "Isabel2024!", UserAgent: "teléfono",
	})
	require.NoError(t, err)

	computadora, err := service.Login(ctx, LoginInput{
		Email: "isabel@example.com", Password: "Isabel2024!", UserAgent: "computadora",
	})
	require.NoError(t, err)

	// Cierra sesión sólo en el teléfono.
	require.NoError(t, service.Logout(ctx, telefono.RefreshToken))

	_, err = service.Refresh(ctx, telefono.RefreshToken, "", "")
	assert.ErrorIs(t, err, domain.ErrUnauthorized, "la sesión del teléfono debe quedar cerrada")

	_, err = service.Refresh(ctx, computadora.RefreshToken, "", "")
	assert.NoError(t, err, "la sesión de la computadora debe seguir activa")
}
