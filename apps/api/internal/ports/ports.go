// Package ports define las interfaces que el dominio necesita del exterior.
//
// El dominio declara QUÉ necesita; los adaptadores deciden CÓMO. Gracias a
// esto el núcleo de negocio se testea con implementaciones falsas, sin
// levantar TigerBeetle ni Postgres.
//
// La flecha de dependencia apunta siempre hacia adentro: los adaptadores
// conocen al dominio, el dominio no conoce a los adaptadores.
package ports

import (
	"context"
	"math/big"
	"time"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
)

// Ledger son las operaciones financieras.
//
// Lo implementa el adaptador de TigerBeetle. La interfaz habla de cuentas,
// saldos y transferencias: nada de Uint128, flags ni detalles del cliente.
type Ledger interface {
	// CreateAccount crea una cuenta en el libro contable.
	CreateAccount(ctx context.Context, tigerBeetleID *big.Int, accountType domain.AccountType) error

	// GetBalance devuelve el saldo actual de una cuenta.
	GetBalance(ctx context.Context, tigerBeetleID *big.Int) (domain.Balance, error)

	// GetBalances devuelve los saldos de varias cuentas en una sola llamada.
	// Evita el problema N+1 al listar las cuentas de un usuario.
	GetBalances(ctx context.Context, tigerBeetleIDs []*big.Int) (map[string]domain.Balance, error)

	// Transfer ejecuta una transferencia y devuelve su identificador.
	//
	// Si la petición tiene Pending en true, los fondos quedan reservados y hay
	// que confirmarla con PostPending o liberarla con VoidPending.
	Transfer(ctx context.Context, req domain.TransferRequest) (*big.Int, error)

	// PostPending confirma una transferencia pendiente: mueve el dinero.
	PostPending(ctx context.Context, pendingID *big.Int) error

	// VoidPending cancela una transferencia pendiente y libera los fondos.
	VoidPending(ctx context.Context, pendingID *big.Int) error

	// ListTransfers devuelve el historial de una cuenta, más reciente primero.
	ListTransfers(ctx context.Context, tigerBeetleID *big.Int, filter TransferFilter) ([]domain.Transaction, error)

	// LookupTransfer busca una transferencia por su identificador.
	LookupTransfer(ctx context.Context, transferID *big.Int) (domain.Transaction, error)

	// Close libera la conexión con el libro contable.
	Close() error
}

// TransferFilter acota la consulta del historial.
type TransferFilter struct {
	// Limit es cuántos resultados devolver como máximo.
	Limit int

	// Cursor es el timestamp desde el cual seguir paginando.
	// Vacío significa empezar por el principio.
	Cursor uint64

	// From y To acotan por fecha. Cero significa sin límite por ese lado.
	From time.Time
	To   time.Time
}

// UserRepository es la persistencia de usuarios y autenticación.
// Lo implementa el adaptador de Postgres.
type UserRepository interface {
	Create(ctx context.Context, user domain.User) (domain.User, error)
	FindByID(ctx context.Context, id string) (domain.User, error)
	FindByEmail(ctx context.Context, email string) (domain.User, error)
	EmailExists(ctx context.Context, email string) (bool, error)
}

// AccountRepository son los metadatos de cuentas.
//
// El saldo NO se guarda acá: vive en TigerBeetle. Esta interfaz sólo maneja
// lo que TigerBeetle no puede guardar, como el número de cuenta legible.
type AccountRepository interface {
	Create(ctx context.Context, account domain.Account) (domain.Account, error)
	FindByID(ctx context.Context, id string) (domain.Account, error)
	FindByNumber(ctx context.Context, accountNumber string) (domain.Account, error)
	ListByUser(ctx context.Context, userID string) ([]domain.Account, error)

	// NextAccountNumber genera un número de cuenta libre.
	NextAccountNumber(ctx context.Context) (string, error)

	// NumbersByTigerBeetleIDs traduce ids de TigerBeetle a números de cuenta
	// legibles, en una sola consulta.
	//
	// Lo necesita el historial: TigerBeetle guarda ids numéricos, pero la
	// persona quiere ver "4001-8143-0798-6257". La clave del mapa es el id en
	// decimal; los ids que no estén en Postgres se omiten.
	NumbersByTigerBeetleIDs(ctx context.Context, ids []*big.Int) (map[string]string, error)
}

// RefreshTokenRepository maneja los refresh tokens con rotación.
//
// El diseño detecta el robo de tokens: cada token pertenece a una familia y se
// usa una sola vez. Si alguien reutiliza uno ya consumido, es señal de que fue
// robado y se revoca la familia completa.
type RefreshTokenRepository interface {
	// Store guarda el hash de un token nuevo.
	Store(ctx context.Context, token domain.RefreshToken) error

	// FindByHash busca un token por su hash.
	FindByHash(ctx context.Context, tokenHash string) (domain.RefreshToken, error)

	// MarkUsed marca un token como consumido.
	MarkUsed(ctx context.Context, tokenHash string) error

	// RevokeFamily revoca todos los tokens de una familia.
	// Se invoca al detectar la reutilización de un token ya consumido.
	RevokeFamily(ctx context.Context, familyID string) error

	// RevokeAllForUser cierra todas las sesiones de un usuario.
	RevokeAllForUser(ctx context.Context, userID string) error
}

// TransactionMetadataRepository guarda las descripciones de las transferencias.
//
// TigerBeetle no admite texto libre, así que la descripción que escribe el
// usuario se guarda en Postgres enlazada por el id de la transferencia.
type TransactionMetadataRepository interface {
	Store(ctx context.Context, transferID *big.Int, description string) error
	GetMany(ctx context.Context, transferIDs []*big.Int) (map[string]string, error)
}

// AIProvider es el modelo de lenguaje que atiende el chat.
//
// Está detrás de una interfaz para poder cambiar de proveedor sin tocar la
// lógica del chat, y para testear sin gastar llamadas reales a la API.
type AIProvider interface {
	// Complete envía la conversación y devuelve la respuesta del modelo.
	// Si el modelo decide usar una herramienta, viene en ToolCalls.
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}

// CompletionRequest es lo que se le manda al modelo.
type CompletionRequest struct {
	Messages []ChatMessage
	Tools    []ToolDefinition
}

// ChatMessage es un mensaje de la conversación.
type ChatMessage struct {
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  []ToolCall
}

// ToolDefinition describe una herramienta disponible para el modelo.
type ToolDefinition struct {
	Name        string
	Description string
	// InputSchema es un JSON Schema que describe los parámetros.
	InputSchema map[string]any
}

// ToolCall es la invocación de una herramienta decidida por el modelo.
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// CompletionResponse es la respuesta del modelo.
type CompletionResponse struct {
	Content   string
	ToolCalls []ToolCall
}
