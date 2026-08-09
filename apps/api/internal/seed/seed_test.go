package seed

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildData arma un conjunto de datos de prueba pequeño y controlado.
func buildData() *Data {
	return &Data{
		Users: []UserRecord{
			{ID: "u1", Email: "ana@example.com", Password: "Ana2024!", FullName: "Ana Gil"},
			{ID: "u2", Email: "beto@example.com", Password: "Beto2024!", FullName: "Beto Ruiz"},
			{ID: "u3", Email: "caro@example.com", Password: "Caro2024!", FullName: "Caro Paz"},
		},
		Accounts: []AccountRecord{
			{AccountNumber: "4001-0000-0000-0001", UserID: "u1", InitialBalance: 100.50, Currency: "USD", AccountType: "checking"},
			{AccountNumber: "4001-0000-0000-0002", UserID: "u2", InitialBalance: 200.25, Currency: "USD", AccountType: "savings"},
			{AccountNumber: "4001-0000-0000-0003", UserID: "u3", InitialBalance: 300.00, Currency: "USD", AccountType: "investment"},
		},
		Transactions: []TransactionRecord{
			// Dentro del primer usuario.
			{FromAccount: "EXTERNAL", ToAccount: "4001-0000-0000-0001", Amount: 50, Type: "deposit", Timestamp: "2024-01-01T00:00:00Z"},
			// Entre el primer y el segundo usuario: cruza el corte del lote.
			{FromAccount: "4001-0000-0000-0001", ToAccount: "4001-0000-0000-0002", Amount: 25, Type: "transfer", Timestamp: "2024-01-02T00:00:00Z"},
			// Dentro del tercero.
			{FromAccount: "4001-0000-0000-0003", ToAccount: "EXTERNAL", Amount: 10, Type: "withdrawal", Timestamp: "2024-01-03T00:00:00Z"},
		},
	}
}

func TestLimitRecortaDeFormaCoherente(t *testing.T) {
	data := limit(buildData(), 1)

	require.Len(t, data.Users, 1)
	assert.Equal(t, "u1", data.Users[0].ID)

	// Sólo entran las cuentas de ese usuario.
	require.Len(t, data.Accounts, 1)
	assert.Equal(t, "4001-0000-0000-0001", data.Accounts[0].AccountNumber)

	// Y sólo las transacciones cuyas dos puntas están dentro del subconjunto.
	// La transferencia hacia el segundo usuario queda fuera porque su cuenta
	// destino no existe en este lote.
	require.Len(t, data.Transactions, 1)
	assert.Equal(t, "deposit", data.Transactions[0].Type)
}

func TestSkipEsElComplementoDeLimit(t *testing.T) {
	data := buildData()
	rest := skip(data, 1)

	require.Len(t, rest.Users, 2)
	assert.Equal(t, "u2", rest.Users[0].ID)
	assert.Equal(t, "u3", rest.Users[1].ID)

	require.Len(t, rest.Accounts, 2)

	// Sólo el retiro del tercer usuario: la transferencia entre el primero y el
	// segundo cruza el corte y se descarta en ambos lados.
	require.Len(t, rest.Transactions, 1)
	assert.Equal(t, "withdrawal", rest.Transactions[0].Type)
}

// Ningún usuario puede quedar duplicado ni perdido entre los dos lotes.
func TestLimitYSkipCubrenTodosLosUsuariosSinRepetir(t *testing.T) {
	data := buildData()

	for corte := 0; corte <= len(data.Users); corte++ {
		primero := limit(data, corte)
		resto := skip(data, corte)

		vistos := make(map[string]int)
		for _, u := range primero.Users {
			vistos[u.ID]++
		}
		for _, u := range resto.Users {
			vistos[u.ID]++
		}

		assert.Len(t, vistos, len(data.Users), "corte en %d: faltan usuarios", corte)
		for id, veces := range vistos {
			assert.Equal(t, 1, veces, "corte en %d: el usuario %s aparece repetido", corte, id)
		}
	}
}

func TestSkipMasAlláDelFinalDevuelveVacio(t *testing.T) {
	rest := skip(buildData(), 10)

	assert.Empty(t, rest.Users)
	assert.Empty(t, rest.Accounts)
	assert.Empty(t, rest.Transactions)
}

// Los identificadores de TigerBeetle se derivan del número de cuenta, no del
// índice del bucle.
//
// Es lo que permite sembrar en dos etapas: con un índice, el segundo lote
// empezaría de cero y colisionaría con las cuentas del primero.
func TestElIdentificadorDeCuentaEsEstable(t *testing.T) {
	primero := accountIDFromNumber("4001-6588-5247-0001")
	segundo := accountIDFromNumber("4001-6588-5247-0001")

	assert.Equal(t, 0, primero.Cmp(segundo), "el mismo número debe dar siempre el mismo id")
}

func TestElIdentificadorDeCuentaEsUnico(t *testing.T) {
	numeros := []string{
		"4001-6588-5247-0001",
		"4001-6588-5247-0002",
		"4001-2559-1172-0416",
		"4001-6629-5214-0685",
	}

	vistos := make(map[string]bool)
	for _, n := range numeros {
		id := accountIDFromNumber(n).String()
		assert.False(t, vistos[id], "el número %s produjo un id repetido", n)
		vistos[id] = true
	}
}

// Los identificadores del sistema quedan reservados.
func TestElIdentificadorDeCuentaNoPisaLosReservados(t *testing.T) {
	id := accountIDFromNumber("4001-0000-0000-0001")

	assert.Positive(t, id.Cmp(big.NewInt(seedIDOffset-1)),
		"el id debe estar por encima del rango reservado para cuentas del sistema")
}

func TestElIdentificadorToleraNumerosMalFormados(t *testing.T) {
	// Un número fuera de formato no puede provocar un pánico: cae a un hash.
	id := accountIDFromNumber("no-es-un-numero-de-cuenta")

	require.NotNil(t, id)
	assert.Positive(t, id.Sign())
}

// La conversión de montos redondea en lugar de truncar.
//
// El JSON trae los montos como número, que se decodifica a float64. Truncar
// daría 3235452 para 32354.53, porque en binario es 32354.529999...
func TestToCentsRedondeaEnLugarDeTruncar(t *testing.T) {
	casos := []struct {
		monto    float64
		esperado int64
	}{
		{32354.53, 3235453},
		{4999.65, 499965},
		{995.76, 99576},
		{1633.55, 163355},
		{0.01, 1},
		{100, 10000},
		{49982.36, 4998236},
	}

	for _, c := range casos {
		assert.Equal(t, c.esperado, toCents(c.monto), "monto %v", c.monto)
	}
}

func TestToCentsManejaNegativos(t *testing.T) {
	assert.Equal(t, int64(-2550), toCents(-25.50))
}

func TestLoadRechazaUnArchivoInexistente(t *testing.T) {
	_, err := Load("/ruta/que/no/existe.json")
	assert.Error(t, err)
}
