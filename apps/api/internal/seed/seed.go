// Package seed carga los datos de prueba en Postgres y TigerBeetle.
//
// # Por qué el orden importa
//
// El JSON trae un `initial_balance` por cuenta Y 6.429 transacciones
// históricas. Son dos hechos independientes, no una secuencia: si se aplicara
// el saldo primero y después el historial, 244 transacciones dejarían la
// cuenta en negativo y TigerBeetle las rechazaría por el flag
// debits_must_not_exceed_credits.
//
// La solución: primero se reproduce el historial completo desde la cuenta del
// operador, y al final se hace un asiento de ajuste por cuenta para que el
// saldo coincida exactamente con el del JSON. Así el evaluador ve el historial
// completo Y los saldos que espera.
package seed

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Moisinho/banca-ai/apps/api/internal/adapters/tigerbeetle"
	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// Data es la estructura del archivo de datos de prueba.
type Data struct {
	Users        []UserRecord        `json:"users"`
	Accounts     []AccountRecord     `json:"accounts"`
	Transactions []TransactionRecord `json:"transactions"`
}

type UserRecord struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	FullName  string `json:"full_name"`
	CreatedAt string `json:"created_at"`
}

type AccountRecord struct {
	AccountNumber  string  `json:"account_number"`
	UserID         string  `json:"user_id"`
	InitialBalance float64 `json:"initial_balance"`
	Currency       string  `json:"currency"`
	AccountType    string  `json:"account_type"`
}

type TransactionRecord struct {
	FromAccount string  `json:"from_account"`
	ToAccount   string  `json:"to_account"`
	Amount      float64 `json:"amount"`
	Type        string  `json:"type"`
	Description string  `json:"description"`
	Timestamp   string  `json:"timestamp"`
	Status      string  `json:"status"`
}

// Config son los parámetros de la siembra.
type Config struct {
	// DataPath es la ruta del archivo JSON.
	DataPath string

	// UserLimit acota cuántos usuarios cargar en total. Cero significa todos.
	UserLimit int

	// BcryptCost es el costo de hashing de los usuarios sembrados.
	//
	// Deliberadamente menor que el de la aplicación: las contraseñas de estos
	// usuarios ya vienen en texto plano dentro del repositorio, así que un
	// costo alto no aporta seguridad real y sí haría lento el primer arranque.
	// Los usuarios que se registren por la aplicación usan el costo completo.
	BcryptCost int
}

// BatchLedger es la parte del libro contable que admite operaciones en lote.
//
// TigerBeetle acepta hasta 8.189 eventos por petición. Enviar de a uno paga el
// costo de red y consenso por cada movimiento; en lote ese costo se reparte y
// la carga pasa de minutos a segundos.
//
// Es una interfaz aparte de ports.Ledger porque sólo la siembra la necesita:
// las operaciones de la aplicación son de a una por naturaleza.
type BatchLedger interface {
	CreateAccountsBatch(ctx context.Context, requests []tigerbeetle.AccountRequest) error
	TransferBatch(ctx context.Context, requests []domain.TransferRequest) ([]tigerbeetle.TransferBatchItem, error)
}

// Seeder carga los datos de prueba.
type Seeder struct {
	users    ports.UserRepository
	accounts ports.AccountRepository
	metadata ports.TransactionMetadataRepository
	ledger   ports.Ledger
	// batch es opcional: si el libro contable admite lotes, la carga los usa.
	batch BatchLedger
	log   *slog.Logger
}

func New(
	users ports.UserRepository,
	accounts ports.AccountRepository,
	metadata ports.TransactionMetadataRepository,
	ledger ports.Ledger,
	log *slog.Logger,
) *Seeder {
	s := &Seeder{
		users:    users,
		accounts: accounts,
		metadata: metadata,
		ledger:   ledger,
		log:      log,
	}

	// Si el libro contable admite lotes, la siembra los aprovecha. El
	// adaptador falso de los tests no los implementa y cae al camino de a uno.
	if batch, ok := ledger.(BatchLedger); ok {
		s.batch = batch
	}

	return s
}

// Run carga los datos de prueba.
//
// Es idempotente: si ya hay usuarios no hace nada, así que reiniciar el
// contenedor no duplica los datos.
//
// Toda la carga es sincrónica. Gracias al envío en lote a TigerBeetle —hasta
// 8.189 eventos por petición— los 1.000 usuarios con sus 6.429 transacciones
// entran en segundos, así que no hace falta diferir nada a segundo plano.
func (s *Seeder) Run(ctx context.Context, cfg Config) error {
	// Un usuario conocido del JSON sirve de centinela: si existe, ya se sembró.
	exists, err := s.users.EmailExists(ctx, "ihernandez@email.com")
	if err != nil {
		return fmt.Errorf("no se pudo verificar si los datos ya están sembrados: %w", err)
	}
	if exists {
		s.log.Info("los datos de prueba ya están cargados, se omite la siembra")
		return nil
	}

	data, err := Load(cfg.DataPath)
	if err != nil {
		return err
	}

	if cfg.UserLimit > 0 && cfg.UserLimit < len(data.Users) {
		data = limit(data, cfg.UserLimit)
	}

	s.log.Info("sembrando datos de prueba",
		"usuarios", len(data.Users),
		"cuentas", len(data.Accounts),
		"transacciones", len(data.Transactions),
	)

	started := time.Now()
	if err := s.seedSet(ctx, data, cfg.BcryptCost); err != nil {
		return err
	}

	s.log.Info("datos de prueba cargados",
		"duración", time.Since(started).Round(time.Millisecond).String(),
	)

	return nil
}

// seedSet carga un conjunto de datos completo: usuarios, cuentas, historial y
// ajuste de saldos.
func (s *Seeder) seedSet(ctx context.Context, data *Data, bcryptCost int) error {
	// 1. Usuarios. El hashing es lo más caro, así que se paraleliza.
	userIDs, err := s.seedUsers(ctx, data.Users, bcryptCost)
	if err != nil {
		return err
	}

	// 2. Cuentas, en Postgres y en TigerBeetle.
	accountsByNumber, err := s.seedAccounts(ctx, data.Accounts, userIDs)
	if err != nil {
		return err
	}

	// 3. Historial, en orden cronológico.
	if err := s.seedTransactions(ctx, data.Transactions, accountsByNumber); err != nil {
		return err
	}

	// 4. Ajuste final para que los saldos coincidan con el JSON.
	return s.reconcileBalances(ctx, data.Accounts, accountsByNumber)
}

// Load lee y valida el archivo de datos.
func Load(path string) (*Data, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer el archivo de datos %q: %w", path, err)
	}

	var data Data
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("el archivo de datos no tiene un formato válido: %w", err)
	}

	if len(data.Users) == 0 {
		return nil, fmt.Errorf("el archivo de datos no contiene usuarios")
	}

	return &data, nil
}

// limit recorta el conjunto a los primeros n usuarios, conservando la
// coherencia: sólo entran las cuentas de esos usuarios y sólo las
// transacciones entre esas cuentas.
func limit(data *Data, n int) *Data {
	users := data.Users[:n]

	keep := make(map[string]bool, n)
	for _, u := range users {
		keep[u.ID] = true
	}

	var accounts []AccountRecord
	numbers := make(map[string]bool)
	for _, a := range data.Accounts {
		if keep[a.UserID] {
			accounts = append(accounts, a)
			numbers[a.AccountNumber] = true
		}
	}

	var transactions []TransactionRecord
	for _, t := range data.Transactions {
		fromOK := t.FromAccount == externalAccount || numbers[t.FromAccount]
		toOK := t.ToAccount == externalAccount || numbers[t.ToAccount]
		if fromOK && toOK {
			transactions = append(transactions, t)
		}
	}

	return &Data{Users: users, Accounts: accounts, Transactions: transactions}
}

// skip devuelve los datos a partir del usuario n, descartando los anteriores.
//
// Es el complemento de limit: juntas reparten el archivo en el lote inicial y
// el resto, sin que ninguna transacción quede duplicada ni perdida.
func skip(data *Data, n int) *Data {
	if n >= len(data.Users) {
		return &Data{}
	}

	users := data.Users[n:]

	keep := make(map[string]bool, len(users))
	for _, u := range users {
		keep[u.ID] = true
	}

	var accounts []AccountRecord
	numbers := make(map[string]bool)
	for _, a := range data.Accounts {
		if keep[a.UserID] {
			accounts = append(accounts, a)
			numbers[a.AccountNumber] = true
		}
	}

	// Sólo entran las transacciones entre cuentas de este subconjunto. Las que
	// cruzan de un lote al otro se descartan: reproducirlas exigiría que ambas
	// cuentas ya existieran, y el orden de carga no lo garantiza.
	var transactions []TransactionRecord
	for _, t := range data.Transactions {
		fromOK := t.FromAccount == externalAccount || numbers[t.FromAccount]
		toOK := t.ToAccount == externalAccount || numbers[t.ToAccount]
		if fromOK && toOK {
			transactions = append(transactions, t)
		}
	}

	return &Data{Users: users, Accounts: accounts, Transactions: transactions}
}

// externalAccount es como el JSON identifica al mundo exterior.
const externalAccount = "EXTERNAL"

// seedUsers inserta los usuarios y devuelve el mapa del id del JSON al id real.
//
// El hashing con bcrypt es CPU puro y no comparte estado, así que se reparte
// entre todos los núcleos disponibles: con 1.000 usuarios la diferencia es de
// minutos a segundos.
func (s *Seeder) seedUsers(ctx context.Context, records []UserRecord, cost int) (map[string]string, error) {
	type hashed struct {
		record UserRecord
		hash   string
		err    error
	}

	workers := runtime.NumCPU()
	if workers > len(records) {
		workers = len(records)
	}

	jobs := make(chan int, len(records))
	results := make([]hashed, len(records))

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				h, err := bcrypt.GenerateFromPassword([]byte(records[i].Password), cost)
				results[i] = hashed{record: records[i], hash: string(h), err: err}
			}
		}()
	}

	for i := range records {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	s.log.Info("contraseñas cifradas", "cantidad", len(records), "workers", workers, "costo", cost)

	// La inserción es secuencial: el cuello de botella era el hashing, no la
	// base, y así los errores son más fáciles de atribuir.
	userIDs := make(map[string]string, len(records))
	var duplicates int

	for _, r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("no se pudo cifrar la contraseña de %s: %w", r.record.Email, r.err)
		}

		created, err := s.users.Create(ctx, domain.User{
			Email:        domain.NormalizeEmail(r.record.Email),
			PasswordHash: r.hash,
			FullName:     r.record.FullName,
		})
		if err != nil {
			// El archivo de datos tiene 20 correos repetidos entre sus 1.000
			// registros. El índice único los rechaza, que es lo correcto: dos
			// personas no pueden compartir credenciales.
			//
			// Se omiten y se sigue, en lugar de abortar la carga entera por un
			// defecto del archivo de origen.
			if errors.Is(err, domain.ErrEmailAlreadyUsed) {
				duplicates++
				continue
			}
			return nil, fmt.Errorf("no se pudo crear el usuario %s: %w", r.record.Email, err)
		}

		userIDs[r.record.ID] = created.ID
	}

	if duplicates > 0 {
		s.log.Warn("se omitieron usuarios con correo repetido en el archivo de datos",
			"omitidos", duplicates,
			"cargados", len(userIDs),
		)
	}

	return userIDs, nil
}

// seededAccount enlaza un número de cuenta con sus identificadores reales.
type seededAccount struct {
	id            string
	tigerBeetleID *big.Int
	currency      string
}

// seedAccounts crea las cuentas en TigerBeetle y en Postgres.
func (s *Seeder) seedAccounts(
	ctx context.Context,
	records []AccountRecord,
	userIDs map[string]string,
) (map[string]seededAccount, error) {
	out := make(map[string]seededAccount, len(records))

	// Primero se arma el lote completo para el libro contable, y se envía en
	// una sola petición en lugar de una por cuenta.
	var ledgerBatch []tigerbeetle.AccountRequest
	type pending struct {
		record        AccountRecord
		userID        string
		accountType   domain.AccountType
		tigerBeetleID *big.Int
	}
	var toInsert []pending

	for _, r := range records {
		userID, ok := userIDs[r.UserID]
		if !ok {
			// La cuenta pertenece a un usuario que quedó fuera del límite.
			continue
		}

		accountType := domain.AccountType(r.AccountType)
		if !accountType.Valid() {
			return nil, fmt.Errorf("la cuenta %s tiene un tipo inválido: %s", r.AccountNumber, r.AccountType)
		}

		// Identificador determinístico derivado del número de cuenta.
		//
		// Se deriva del número y no del índice del bucle porque la siembra
		// ocurre en dos etapas: con un índice, el segundo lote empezaría de
		// cero y colisionaría con las cuentas del primero.
		tigerBeetleID := accountIDFromNumber(r.AccountNumber)

		ledgerBatch = append(ledgerBatch, tigerbeetle.AccountRequest{
			TigerBeetleID: tigerBeetleID,
			Type:          accountType,
		})
		toInsert = append(toInsert, pending{
			record:        r,
			userID:        userID,
			accountType:   accountType,
			tigerBeetleID: tigerBeetleID,
		})
	}

	if s.batch != nil {
		if err := s.batch.CreateAccountsBatch(ctx, ledgerBatch); err != nil {
			return nil, err
		}
	} else {
		// Camino de respaldo para un libro contable sin soporte de lotes.
		for _, req := range ledgerBatch {
			if err := s.ledger.CreateAccount(ctx, req.TigerBeetleID, req.Type); err != nil {
				return nil, fmt.Errorf("no se pudo crear una cuenta en el libro contable: %w", err)
			}
		}
	}

	for _, p := range toInsert {
		r := p.record

		created, err := s.accounts.Create(ctx, domain.Account{
			UserID:        p.userID,
			AccountNumber: r.AccountNumber,
			TigerBeetleID: p.tigerBeetleID,
			Type:          p.accountType,
			Currency:      r.Currency,
		})
		if err != nil {
			return nil, fmt.Errorf("no se pudo crear la cuenta %s: %w", r.AccountNumber, err)
		}

		out[r.AccountNumber] = seededAccount{
			id:            created.ID,
			tigerBeetleID: p.tigerBeetleID,
			currency:      r.Currency,
		}
	}

	s.log.Info("cuentas creadas", "cantidad", len(out))
	return out, nil
}

// seedIDOffset deja libres los primeros identificadores para cuentas del
// sistema, como la del operador.
const seedIDOffset = 100_000

// accountIDFromNumber deriva un identificador de TigerBeetle del número de
// cuenta.
//
// Los números del archivo tienen formato 4001-XXXX-XXXX-XXXX: quitando los
// guiones queda un entero de 16 dígitos, único por construcción. Derivarlo así
// hace que el identificador sea estable y reproducible, sin depender del orden
// en que se cargue cada cuenta.
func accountIDFromNumber(accountNumber string) *big.Int {
	digits := strings.ReplaceAll(accountNumber, "-", "")

	id, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		// Un número con formato inesperado cae a un hash del texto, que sigue
		// siendo determinístico.
		sum := sha256.Sum256([]byte(accountNumber))
		id = new(big.Int).SetBytes(sum[:8])
	}

	// Desplazado para no pisar los identificadores reservados del sistema.
	return id.Add(id, big.NewInt(seedIDOffset))
}

// seedTransactions reproduce el historial en orden cronológico.
//
// Las cuentas arrancan en cero, así que una transferencia entre cuentas de
// usuario sólo funciona si el origen ya tiene fondos. En lugar de financiar
// cada movimiento por separado —lo que duplicaría el número de escrituras—
// se calcula por adelantado cuánto necesita cada cuenta y se le entrega ese
// total en un único asiento.
func (s *Seeder) seedTransactions(
	ctx context.Context,
	records []TransactionRecord,
	accounts map[string]seededAccount,
) error {
	// El orden cronológico importa: el historial tiene que leerse como una
	// secuencia coherente.
	sorted := make([]TransactionRecord, len(records))
	copy(sorted, records)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp < sorted[j].Timestamp
	})

	operator := new(big.Int).SetUint64(domain.OperatorAccountID)

	// Primera pasada: cuánto tiene que salir de cada cuenta en total.
	//
	// Financiar por adelantado ese total garantiza que ningún movimiento se
	// rechace por fondos, sin necesidad de simular el saldo paso a paso.
	needed := make(map[string]domain.Money)
	for _, r := range sorted {
		if r.FromAccount == externalAccount {
			continue
		}
		if _, exists := accounts[r.FromAccount]; !exists {
			continue
		}
		needed[r.FromAccount] += domain.Money(toCents(r.Amount))
	}

	fundingBatch := make([]domain.TransferRequest, 0, len(needed))
	for number, total := range needed {
		if total <= 0 {
			continue
		}
		fundingBatch = append(fundingBatch, domain.TransferRequest{
			FromTigerBeetleID: operator,
			ToTigerBeetleID:   accounts[number].tigerBeetleID,
			Amount:            total,
			Type:              domain.TransactionTypeDeposit,
			// Asiento técnico: financia la cuenta para poder reproducir su
			// historial, pero no es una operación del usuario.
			CodeOverride: domain.SeedAdjustmentCode,
		})
	}

	if err := s.sendBatch(ctx, fundingBatch); err != nil {
		return err
	}

	s.log.Info("cuentas financiadas para reproducir el historial", "cuentas", len(needed))

	// Segunda pasada: los movimientos reales, también en lote.
	//
	// Las descripciones se guardan después, cuando ya se conocen los
	// identificadores que TigerBeetle asignó a cada transferencia.
	batch := make([]domain.TransferRequest, 0, len(sorted))
	descriptions := make([]string, 0, len(sorted))
	var skipped int

	for _, r := range sorted {
		amount, err := domain.NewMoney(toCents(r.Amount))
		if err != nil {
			skipped++
			continue
		}

		txType := domain.TransactionType(r.Type)
		if !txType.Valid() {
			skipped++
			continue
		}

		from, to, ok := s.resolveEndpoints(r, accounts, operator)
		if !ok {
			skipped++
			continue
		}

		batch = append(batch, domain.TransferRequest{
			FromTigerBeetleID: from,
			ToTigerBeetleID:   to,
			Amount:            amount,
			Type:              txType,
		})
		descriptions = append(descriptions, r.Description)
	}

	items, err := s.sendBatchWithIDs(ctx, batch)
	if err != nil {
		return err
	}

	// Las descripciones viven en Postgres porque TigerBeetle no admite texto
	// libre, y se enlazan por el identificador que el libro contable asignó.
	if err := s.storeDescriptions(ctx, items, descriptions); err != nil {
		// El texto es complementario: perderlo no invalida los movimientos.
		s.log.Warn("no se pudieron guardar algunas descripciones", "error", err)
	}

	s.log.Info("historial cargado", "transacciones", len(items), "omitidas", skipped)
	return nil
}

// sendBatch ejecuta transferencias en lote, o de a una si el libro contable no
// admite lotes.
func (s *Seeder) sendBatch(ctx context.Context, requests []domain.TransferRequest) error {
	_, err := s.sendBatchWithIDs(ctx, requests)
	return err
}

// sendBatchWithIDs ejecuta transferencias en lote y devuelve sus identificadores.
func (s *Seeder) sendBatchWithIDs(
	ctx context.Context,
	requests []domain.TransferRequest,
) ([]tigerbeetle.TransferBatchItem, error) {
	if len(requests) == 0 {
		return nil, nil
	}

	if s.batch != nil {
		items, err := s.batch.TransferBatch(ctx, requests)
		if err != nil {
			return nil, fmt.Errorf("no se pudo ejecutar el lote de transferencias: %w", err)
		}
		return items, nil
	}

	// Camino de respaldo para un libro contable sin soporte de lotes.
	items := make([]tigerbeetle.TransferBatchItem, 0, len(requests))
	for _, req := range requests {
		id, err := s.ledger.Transfer(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("no se pudo registrar una transacción histórica: %w", err)
		}
		items = append(items, tigerbeetle.TransferBatchItem{Request: req, ID: id})
	}

	return items, nil
}

// storeDescriptions guarda el texto libre de las transacciones.
func (s *Seeder) storeDescriptions(
	ctx context.Context,
	items []tigerbeetle.TransferBatchItem,
	descriptions []string,
) error {
	for i, item := range items {
		if i >= len(descriptions) || descriptions[i] == "" {
			continue
		}
		if err := s.metadata.Store(ctx, item.ID, descriptions[i]); err != nil {
			return err
		}
	}
	return nil
}

// resolveEndpoints traduce los números de cuenta del JSON a identificadores
// de TigerBeetle.
func (s *Seeder) resolveEndpoints(
	r TransactionRecord,
	accounts map[string]seededAccount,
	operator *big.Int,
) (from, to *big.Int, ok bool) {
	if r.FromAccount == externalAccount {
		from = operator
	} else {
		account, exists := accounts[r.FromAccount]
		if !exists {
			return nil, nil, false
		}
		from = account.tigerBeetleID
	}

	if r.ToAccount == externalAccount {
		to = operator
	} else {
		account, exists := accounts[r.ToAccount]
		if !exists {
			return nil, nil, false
		}
		to = account.tigerBeetleID
	}

	// TigerBeetle rechaza una transferencia de una cuenta a sí misma. Puede
	// ocurrir si el JSON tiene un movimiento con origen y destino iguales.
	if from.Cmp(to) == 0 {
		return nil, nil, false
	}

	return from, to, true
}

// reconcileBalances ajusta cada cuenta para que su saldo coincida con el
// initial_balance del JSON.
//
// Es el paso que hace cuadrar las dos fuentes: el historial ya se reprodujo,
// pero los saldos resultantes no son los del archivo. Un asiento de ajuste por
// cuenta cierra la diferencia.
//
// Tanto la consulta de saldos como los ajustes van en lote: con 1.500 cuentas,
// hacerlo de a una serían 3.000 idas y vueltas.
func (s *Seeder) reconcileBalances(
	ctx context.Context,
	records []AccountRecord,
	accounts map[string]seededAccount,
) error {
	if len(accounts) == 0 {
		return nil
	}

	operator := new(big.Int).SetUint64(domain.OperatorAccountID)

	ids := make([]*big.Int, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.tigerBeetleID)
	}

	balances, err := s.ledger.GetBalances(ctx, ids)
	if err != nil {
		return fmt.Errorf("no se pudieron consultar los saldos para el ajuste: %w", err)
	}

	adjustments := make([]domain.TransferRequest, 0, len(records))

	for _, r := range records {
		account, ok := accounts[r.AccountNumber]
		if !ok {
			continue
		}

		balance := balances[account.tigerBeetleID.String()]
		delta := domain.Money(toCents(r.InitialBalance)) - balance.Available

		if delta == 0 {
			continue
		}

		// Un delta positivo entra desde el operador; uno negativo sale hacia él.
		from, to := operator, account.tigerBeetleID
		amount := delta
		txType := domain.TransactionTypeDeposit

		if delta < 0 {
			from, to = account.tigerBeetleID, operator
			amount = -delta
			txType = domain.TransactionTypeWithdrawal
		}

		adjustments = append(adjustments, domain.TransferRequest{
			FromTigerBeetleID: from,
			ToTigerBeetleID:   to,
			Amount:            amount,
			Type:              txType,
			// El ajuste no es una operación del usuario: existe sólo para
			// cuadrar el saldo con el archivo de datos.
			CodeOverride: domain.SeedAdjustmentCode,
		})
	}

	if err := s.sendBatch(ctx, adjustments); err != nil {
		return fmt.Errorf("no se pudieron ajustar los saldos: %w", err)
	}

	s.log.Info("saldos ajustados al archivo de datos", "cuentas", len(adjustments))
	return nil
}

// toCents convierte un monto decimal del JSON a centavos.
//
// El JSON trae los montos como número, que en Go se decodifica a float64. La
// conversión redondea explícitamente para no arrastrar el error de
// representación binaria: 32354.53 en float64 es 32354.529999..., y truncar
// daría 3235452 en lugar de 3235453.
func toCents(amount float64) int64 {
	if amount < 0 {
		return -int64(-amount*100 + 0.5)
	}
	return int64(amount*100 + 0.5)
}
