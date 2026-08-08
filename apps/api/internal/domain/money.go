package domain

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Money representa un monto en la unidad mínima de la moneda (centavos).
//
// Deliberadamente NO usamos float64. Un float no puede representar 0.10 de
// forma exacta en binario, y ese error se acumula: sumá 0.10 diez veces en
// float64 y no da 1.00. En contabilidad eso rompe el balance.
//
// Un monto de $32,354.53 se representa como Money(3235453).
type Money int64

// MaxAmount es el tope por operación individual, en centavos ($1,000,000).
// Existe como red de seguridad: un monto absurdo casi siempre es un error de
// entrada o un intento de abuso, no una operación legítima.
const MaxAmount Money = 100_000_000

// centsPerUnit es la cantidad de centavos que forman una unidad monetaria.
const centsPerUnit = 100

var (
	ErrAmountNotPositive = errors.New("el monto debe ser mayor a cero")
	ErrAmountTooLarge    = errors.New("el monto excede el máximo permitido")
	ErrAmountFormat      = errors.New("formato de monto inválido")
	ErrAmountPrecision   = errors.New("el monto no puede tener más de dos decimales")
)

// NewMoney construye un Money a partir de centavos, validando el rango.
func NewMoney(cents int64) (Money, error) {
	m := Money(cents)
	if err := m.Validate(); err != nil {
		return 0, err
	}
	return m, nil
}

// Validate verifica que el monto sea utilizable en una operación.
func (m Money) Validate() error {
	if m <= 0 {
		return ErrAmountNotPositive
	}
	if m > MaxAmount {
		return ErrAmountTooLarge
	}
	return nil
}

// ParseMoney convierte una representación decimal ("1234.56") a centavos.
//
// Trabaja sobre la cadena en lugar de convertir a float, justamente para no
// introducir el error de redondeo que Money existe para evitar.
func ParseMoney(s string) (Money, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ErrAmountFormat
	}

	// Acepta separadores de miles por comodidad del usuario ("1,234.56").
	s = strings.ReplaceAll(s, ",", "")

	negative := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimPrefix(s, "+")

	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	if intPart == "" {
		intPart = "0"
	}

	if hasFrac && len(fracPart) > 2 {
		return 0, ErrAmountPrecision
	}
	// Normaliza a exactamente dos dígitos: "5" → "50", "" → "00".
	fracPart = (fracPart + "00")[:2]

	units, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, ErrAmountFormat
	}
	frac, err := strconv.ParseInt(fracPart, 10, 64)
	if err != nil {
		return 0, ErrAmountFormat
	}

	cents := units*centsPerUnit + frac
	if negative {
		cents = -cents
	}

	return NewMoney(cents)
}

// String devuelve el monto en formato decimal, siempre con dos decimales.
// Es la única capa donde el dinero deja de ser entero.
func (m Money) String() string {
	sign := ""
	v := int64(m)
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/centsPerUnit, v%centsPerUnit)
}

// Cents devuelve el valor crudo en centavos.
func (m Money) Cents() int64 { return int64(m) }

// ToUint128Bytes convierte el monto al formato de 128 bits que espera
// TigerBeetle, en little-endian.
func (m Money) ToUint128Bytes() [16]byte {
	var out [16]byte
	v := uint64(m)
	for i := 0; i < 8; i++ {
		out[i] = byte(v >> (8 * i))
	}
	return out
}

// MoneyFromBigInt convierte un entero de 128 bits de TigerBeetle a Money.
//
// TigerBeetle usa u128 para los montos, pero nuestros saldos siempre entran
// holgadamente en int64. Si un valor no entra, es señal de corrupción de datos
// y preferimos fallar ruidosamente antes que truncar en silencio.
func MoneyFromBigInt(b *big.Int) (Money, error) {
	if !b.IsInt64() {
		return 0, fmt.Errorf("el valor %s excede el rango de int64", b.String())
	}
	return Money(b.Int64()), nil
}
