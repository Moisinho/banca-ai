package domain

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMoney(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Money
	}{
		{"entero sin decimales", "100", 10000},
		{"con dos decimales", "1234.56", 123456},
		{"con un decimal se normaliza", "10.5", 1050},
		{"monto de los datos de prueba", "32354.53", 3235453},
		{"monto máximo de los datos de prueba", "4999.65", 499965},
		{"con separador de miles", "1,234.56", 123456},
		{"un centavo", "0.01", 1},
		{"signo positivo explícito", "+50.00", 5000},
		{"con espacios alrededor", "  25.50  ", 2550},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMoney(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseMoneyRechazaEntradasInvalidas(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{"cadena vacía", "", ErrAmountFormat},
		{"texto no numérico", "abc", ErrAmountFormat},
		{"cero no es un monto válido", "0", ErrAmountNotPositive},
		{"monto negativo", "-50.00", ErrAmountNotPositive},
		{"más de dos decimales", "10.999", ErrAmountPrecision},
		{"excede el máximo permitido", "9999999.00", ErrAmountTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseMoney(tt.input)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// Este test es la razón por la que Money existe.
//
// Sumar 0.10 diez veces en float64 NO da 1.00 exacto, porque 0.1 no tiene
// representación finita en binario. Con enteros el resultado es exacto siempre.
func TestMoneyNoAcumulaErrorDeRedondeo(t *testing.T) {
	diezCentavos, err := ParseMoney("0.10")
	require.NoError(t, err)

	var total Money
	for i := 0; i < 10; i++ {
		total += diezCentavos
	}

	assert.Equal(t, Money(100), total)
	assert.Equal(t, "1.00", total.String())

	// La demostración del problema que estamos evitando.
	var conFloat float64
	for i := 0; i < 10; i++ {
		conFloat += 0.10
	}
	assert.NotEqual(t, 1.0, conFloat, "float64 acumula error: por eso usamos enteros")
}

func TestMoneyString(t *testing.T) {
	tests := []struct {
		name  string
		input Money
		want  string
	}{
		{"monto con centavos", 123456, "1234.56"},
		{"monto redondo", 10000, "100.00"},
		{"un centavo", 1, "0.01"},
		{"centavos menores a diez llevan cero", 105, "1.05"},
		{"cero", 0, "0.00"},
		{"negativo mantiene el signo", -2550, "-25.50"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.input.String())
		})
	}
}

// Verifica que parsear y volver a formatear no altera el valor, usando montos
// reales de los datos de prueba.
func TestMoneyIdaYVuelta(t *testing.T) {
	montos := []string{"32354.53", "4999.65", "995.76", "1633.55", "49982.36", "0.01"}

	for _, original := range montos {
		t.Run(original, func(t *testing.T) {
			m, err := ParseMoney(original)
			require.NoError(t, err)
			assert.Equal(t, original, m.String())
		})
	}
}

func TestMoneyToUint128Bytes(t *testing.T) {
	m := Money(123456)
	got := m.ToUint128Bytes()

	// TigerBeetle espera little-endian: el byte menos significativo va primero.
	assert.Equal(t, byte(0x40), got[0])
	assert.Equal(t, byte(0xE2), got[1])
	assert.Equal(t, byte(0x01), got[2])

	// Los bytes altos quedan en cero porque el monto entra en 64 bits.
	for i := 8; i < 16; i++ {
		assert.Equal(t, byte(0), got[i], "el byte %d debería ser cero", i)
	}
}

func TestMoneyFromBigInt(t *testing.T) {
	t.Run("valor dentro de rango", func(t *testing.T) {
		got, err := MoneyFromBigInt(big.NewInt(3235453))
		require.NoError(t, err)
		assert.Equal(t, Money(3235453), got)
	})

	t.Run("valor fuera de rango falla en vez de truncar", func(t *testing.T) {
		enorme := new(big.Int).Lsh(big.NewInt(1), 100)
		_, err := MoneyFromBigInt(enorme)
		assert.Error(t, err, "un valor que no entra en int64 debe fallar, no truncarse")
	})
}

func TestMoneyValidate(t *testing.T) {
	assert.NoError(t, Money(1).Validate())
	assert.NoError(t, MaxAmount.Validate())
	assert.ErrorIs(t, Money(0).Validate(), ErrAmountNotPositive)
	assert.ErrorIs(t, Money(-1).Validate(), ErrAmountNotPositive)
	assert.ErrorIs(t, (MaxAmount + 1).Validate(), ErrAmountTooLarge)
}
