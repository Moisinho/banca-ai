package tigerbeetle

import (
	"context"
	"log/slog"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// Tests de integración contra un TigerBeetle real.
//
// Se saltean si no hay un clúster disponible, para que `go test ./...` siga
// funcionando sin infraestructura. En Docker y en CI sí corren.

func newTestLedger(t *testing.T) *Ledger {
	t.Helper()

	addresses := os.Getenv("TIGERBEETLE_ADDRESSES")
	if addresses == "" {
		t.Skip("TIGERBEETLE_ADDRESSES no está definido, se omiten los tests de integración")
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ledger, err := New(0, []string{addresses}, log)
	require.NoError(t, err, "no se pudo conectar con TigerBeetle")

	t.Cleanup(func() { _ = ledger.Close() })
	return ledger
}

// uniqueAccountID genera un id de cuenta irrepetible.
//
// Las cuentas en TigerBeetle son inmutables y no se pueden borrar, así que
// cada test necesita ids propios para no chocar con corridas anteriores.
func uniqueAccountID(t *testing.T, suffix int64) *big.Int {
	t.Helper()
	return big.NewInt(time.Now().UnixNano() + suffix)
}

func TestCreateAccountYConsultarSaldo(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	accountID := uniqueAccountID(t, 1)

	err := ledger.CreateAccount(ctx, accountID, domain.AccountTypeSavings)
	require.NoError(t, err)

	balance, err := ledger.GetBalance(ctx, accountID)
	require.NoError(t, err)

	// Una cuenta recién creada no tiene movimientos.
	assert.Equal(t, domain.Money(0), balance.Available)
	assert.Equal(t, domain.Money(0), balance.Posted)
	assert.Equal(t, domain.Money(0), balance.Pending)
}

func TestCreateAccountRechazaDuplicados(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	accountID := uniqueAccountID(t, 2)

	require.NoError(t, ledger.CreateAccount(ctx, accountID, domain.AccountTypeChecking))

	err := ledger.CreateAccount(ctx, accountID, domain.AccountTypeChecking)
	assert.ErrorIs(t, err, domain.ErrAccountAlreadyExists)
}

func TestDepositoDesdeLaCuentaDelOperador(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	require.NoError(t, ledger.EnsureOperatorAccount(ctx))

	accountID := uniqueAccountID(t, 3)
	require.NoError(t, ledger.CreateAccount(ctx, accountID, domain.AccountTypeSavings))

	amount, err := domain.ParseMoney("1000.00")
	require.NoError(t, err)

	// Un depósito es dinero que entra desde fuera del banco: sale de la cuenta
	// del operador y entra a la del usuario.
	_, err = ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: big.NewInt(int64(domain.OperatorAccountID)),
		ToTigerBeetleID:   accountID,
		Amount:            amount,
		Type:              domain.TransactionTypeDeposit,
	})
	require.NoError(t, err)

	balance, err := ledger.GetBalance(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, amount, balance.Available)
	assert.Equal(t, "1000.00", balance.Available.String())
}

// Este es el test más importante del adaptador.
//
// Verifica que la validación de fondos la hace TigerBeetle y no código Go.
// Un `if saldo < monto` en la aplicación tiene una ventana de carrera entre la
// lectura y la escritura; la base no la tiene.
func TestRetiroSinFondosLoRechazaLaBase(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	require.NoError(t, ledger.EnsureOperatorAccount(ctx))

	accountID := uniqueAccountID(t, 4)
	require.NoError(t, ledger.CreateAccount(ctx, accountID, domain.AccountTypeChecking))

	deposit, _ := domain.ParseMoney("100.00")
	_, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: big.NewInt(int64(domain.OperatorAccountID)),
		ToTigerBeetleID:   accountID,
		Amount:            deposit,
		Type:              domain.TransactionTypeDeposit,
	})
	require.NoError(t, err)

	// Intenta retirar más de lo que hay.
	excessive, _ := domain.ParseMoney("500.00")
	_, err = ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: accountID,
		ToTigerBeetleID:   big.NewInt(int64(domain.OperatorAccountID)),
		Amount:            excessive,
		Type:              domain.TransactionTypeWithdrawal,
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientFunds)

	// El saldo tiene que haber quedado intacto.
	balance, err := ledger.GetBalance(ctx, accountID)
	require.NoError(t, err)
	assert.Equal(t, deposit, balance.Available)
}

// Verifica el flujo de confirmación que usa el chat con IA.
func TestTransferenciaEnDosFasesConfirmada(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	require.NoError(t, ledger.EnsureOperatorAccount(ctx))

	origen := uniqueAccountID(t, 5)
	destino := uniqueAccountID(t, 6)
	require.NoError(t, ledger.CreateAccount(ctx, origen, domain.AccountTypeChecking))
	require.NoError(t, ledger.CreateAccount(ctx, destino, domain.AccountTypeSavings))

	fondos, _ := domain.ParseMoney("1000.00")
	_, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: big.NewInt(int64(domain.OperatorAccountID)),
		ToTigerBeetleID:   origen,
		Amount:            fondos,
		Type:              domain.TransactionTypeDeposit,
	})
	require.NoError(t, err)

	// Fase 1: la IA propone la transferencia, los fondos quedan reservados.
	monto, _ := domain.ParseMoney("300.00")
	pendingID, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: origen,
		ToTigerBeetleID:   destino,
		Amount:            monto,
		Type:              domain.TransactionTypeTransfer,
		Pending:           true,
		PendingTimeout:    5 * time.Minute,
	})
	require.NoError(t, err)

	// El dinero todavía no se movió, pero ya no está disponible.
	balanceOrigen, err := ledger.GetBalance(ctx, origen)
	require.NoError(t, err)
	assert.Equal(t, monto, balanceOrigen.Pending, "los fondos deben quedar reservados")
	assert.Equal(t, domain.Money(70000), balanceOrigen.Available, "el disponible baja a 700.00")
	assert.Equal(t, fondos, balanceOrigen.Posted, "lo liquidado no cambia hasta confirmar")

	balanceDestino, err := ledger.GetBalance(ctx, destino)
	require.NoError(t, err)
	assert.Equal(t, domain.Money(0), balanceDestino.Available, "el destino no recibe nada aún")

	// Fase 2: el usuario confirma y el dinero se mueve.
	require.NoError(t, ledger.PostPending(ctx, pendingID))

	balanceOrigen, err = ledger.GetBalance(ctx, origen)
	require.NoError(t, err)
	assert.Equal(t, domain.Money(70000), balanceOrigen.Available)
	assert.Equal(t, domain.Money(0), balanceOrigen.Pending, "ya no hay nada reservado")

	balanceDestino, err = ledger.GetBalance(ctx, destino)
	require.NoError(t, err)
	assert.Equal(t, monto, balanceDestino.Available, "el destino recibió el dinero")
}

// Verifica que rechazar una propuesta de la IA deja todo como estaba.
func TestTransferenciaEnDosFasesCancelada(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	require.NoError(t, ledger.EnsureOperatorAccount(ctx))

	origen := uniqueAccountID(t, 7)
	destino := uniqueAccountID(t, 8)
	require.NoError(t, ledger.CreateAccount(ctx, origen, domain.AccountTypeChecking))
	require.NoError(t, ledger.CreateAccount(ctx, destino, domain.AccountTypeSavings))

	fondos, _ := domain.ParseMoney("500.00")
	_, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: big.NewInt(int64(domain.OperatorAccountID)),
		ToTigerBeetleID:   origen,
		Amount:            fondos,
		Type:              domain.TransactionTypeDeposit,
	})
	require.NoError(t, err)

	monto, _ := domain.ParseMoney("200.00")
	pendingID, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: origen,
		ToTigerBeetleID:   destino,
		Amount:            monto,
		Type:              domain.TransactionTypeTransfer,
		Pending:           true,
		PendingTimeout:    5 * time.Minute,
	})
	require.NoError(t, err)

	// El usuario rechaza la operación.
	require.NoError(t, ledger.VoidPending(ctx, pendingID))

	// Todo vuelve al estado anterior: nada se movió.
	balanceOrigen, err := ledger.GetBalance(ctx, origen)
	require.NoError(t, err)
	assert.Equal(t, fondos, balanceOrigen.Available, "los fondos se liberaron")
	assert.Equal(t, domain.Money(0), balanceOrigen.Pending)

	balanceDestino, err := ledger.GetBalance(ctx, destino)
	require.NoError(t, err)
	assert.Equal(t, domain.Money(0), balanceDestino.Available, "el destino nunca recibió nada")
}

// Una transferencia confirmada queda en TigerBeetle como dos registros: la
// reserva (pending) y la que la resuelve (post), con ids distintos unidos por
// PendingID. Antes del fix, ListTransfers devolvía ambos y la operación
// aparecía dos veces en el historial con el mismo monto y concepto — el
// dinero nunca se duplicó, pero la lista sí mostraba el registro repetido.
func TestLaOperacionConfirmadaApareceUnaSolaVezEnElHistorial(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	require.NoError(t, ledger.EnsureOperatorAccount(ctx))

	origen := uniqueAccountID(t, 20)
	destino := uniqueAccountID(t, 21)
	require.NoError(t, ledger.CreateAccount(ctx, origen, domain.AccountTypeChecking))
	require.NoError(t, ledger.CreateAccount(ctx, destino, domain.AccountTypeSavings))

	fondos, _ := domain.ParseMoney("1000.00")
	_, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: big.NewInt(int64(domain.OperatorAccountID)),
		ToTigerBeetleID:   origen,
		Amount:            fondos,
		Type:              domain.TransactionTypeDeposit,
	})
	require.NoError(t, err)

	monto, _ := domain.ParseMoney("150.00")
	pendingID, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: origen,
		ToTigerBeetleID:   destino,
		Amount:            monto,
		Type:              domain.TransactionTypeTransfer,
		Pending:           true,
		PendingTimeout:    5 * time.Minute,
	})
	require.NoError(t, err)

	require.NoError(t, ledger.PostPending(ctx, pendingID))

	transacciones, err := ledger.ListTransfers(ctx, origen, ports.TransferFilter{Limit: 10})
	require.NoError(t, err)

	// Sólo debe aparecer UNA fila para esta transferencia, no dos.
	coincidencias := 0
	var vista domain.Transaction
	for _, tx := range transacciones {
		if tx.Amount == monto {
			coincidencias++
			vista = tx
		}
	}

	assert.Equal(t, 1, coincidencias,
		"una operación confirmada debe aparecer una sola vez en el historial")
	assert.Equal(t, domain.TransactionStatusCompleted, vista.Status,
		"la fila que queda debe mostrar el estado final, no 'pendiente'")
}

// A diferencia de una confirmación, cancelar una reserva NO es un movimiento:
// el dinero nunca salió de la cuenta, TigerBeetle sólo liberó lo reservado.
// Ni la reserva ni el registro que la canceló deben aparecer en el historial
// de movimientos. El estado "cancelada" sigue visible donde tiene sentido —la
// tarjeta de confirmación del chat—, pero no como una fila con signo negativo
// entre depósitos y transferencias reales.
func TestLaOperacionCanceladaNoApareceEnElHistorial(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	require.NoError(t, ledger.EnsureOperatorAccount(ctx))

	origen := uniqueAccountID(t, 22)
	destino := uniqueAccountID(t, 23)
	require.NoError(t, ledger.CreateAccount(ctx, origen, domain.AccountTypeChecking))
	require.NoError(t, ledger.CreateAccount(ctx, destino, domain.AccountTypeSavings))

	fondos, _ := domain.ParseMoney("1000.00")
	_, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: big.NewInt(int64(domain.OperatorAccountID)),
		ToTigerBeetleID:   origen,
		Amount:            fondos,
		Type:              domain.TransactionTypeDeposit,
	})
	require.NoError(t, err)

	monto, _ := domain.ParseMoney("77.00")
	pendingID, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: origen,
		ToTigerBeetleID:   destino,
		Amount:            monto,
		Type:              domain.TransactionTypeTransfer,
		Pending:           true,
		PendingTimeout:    5 * time.Minute,
	})
	require.NoError(t, err)

	require.NoError(t, ledger.VoidPending(ctx, pendingID))

	transacciones, err := ledger.ListTransfers(ctx, origen, ports.TransferFilter{Limit: 10})
	require.NoError(t, err)

	for _, tx := range transacciones {
		assert.NotEqual(t, monto, tx.Amount,
			"una operación cancelada no debe aparecer como movimiento en el historial")
	}
}

// Una operación que todavía espera confirmación SÍ debe verse: no hay
// segundo registro que la resuelva todavía, así que no hay nada que filtrar.
func TestLaOperacionSinConfirmarSigueVisibleEnElHistorial(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	require.NoError(t, ledger.EnsureOperatorAccount(ctx))

	origen := uniqueAccountID(t, 24)
	destino := uniqueAccountID(t, 25)
	require.NoError(t, ledger.CreateAccount(ctx, origen, domain.AccountTypeChecking))
	require.NoError(t, ledger.CreateAccount(ctx, destino, domain.AccountTypeSavings))

	fondos, _ := domain.ParseMoney("1000.00")
	_, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: big.NewInt(int64(domain.OperatorAccountID)),
		ToTigerBeetleID:   origen,
		Amount:            fondos,
		Type:              domain.TransactionTypeDeposit,
	})
	require.NoError(t, err)

	monto, _ := domain.ParseMoney("42.00")
	_, err = ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: origen,
		ToTigerBeetleID:   destino,
		Amount:            monto,
		Type:              domain.TransactionTypeTransfer,
		Pending:           true,
		PendingTimeout:    5 * time.Minute,
	})
	require.NoError(t, err)

	transacciones, err := ledger.ListTransfers(ctx, origen, ports.TransferFilter{Limit: 10})
	require.NoError(t, err)

	coincidencias := 0
	var vista domain.Transaction
	for _, tx := range transacciones {
		if tx.Amount == monto {
			coincidencias++
			vista = tx
		}
	}

	assert.Equal(t, 1, coincidencias, "la reserva debe verse mientras espera confirmación")
	assert.Equal(t, domain.TransactionStatusPending, vista.Status)
}

func TestNoSePuedeConfirmarDosVeces(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	require.NoError(t, ledger.EnsureOperatorAccount(ctx))

	origen := uniqueAccountID(t, 9)
	destino := uniqueAccountID(t, 10)
	require.NoError(t, ledger.CreateAccount(ctx, origen, domain.AccountTypeChecking))
	require.NoError(t, ledger.CreateAccount(ctx, destino, domain.AccountTypeSavings))

	fondos, _ := domain.ParseMoney("500.00")
	_, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: big.NewInt(int64(domain.OperatorAccountID)),
		ToTigerBeetleID:   origen,
		Amount:            fondos,
		Type:              domain.TransactionTypeDeposit,
	})
	require.NoError(t, err)

	monto, _ := domain.ParseMoney("100.00")
	pendingID, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: origen,
		ToTigerBeetleID:   destino,
		Amount:            monto,
		Type:              domain.TransactionTypeTransfer,
		Pending:           true,
		PendingTimeout:    5 * time.Minute,
	})
	require.NoError(t, err)

	require.NoError(t, ledger.PostPending(ctx, pendingID))

	// Reintentar la confirmación no puede duplicar el movimiento.
	err = ledger.PostPending(ctx, pendingID)
	assert.ErrorIs(t, err, domain.ErrTransferResolved)

	balanceDestino, err := ledger.GetBalance(ctx, destino)
	require.NoError(t, err)
	assert.Equal(t, monto, balanceDestino.Available, "el destino recibió el dinero una sola vez")
}

func TestTransferenciaALaMismaCuentaSeRechaza(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	accountID := uniqueAccountID(t, 11)
	require.NoError(t, ledger.CreateAccount(ctx, accountID, domain.AccountTypeChecking))

	monto, _ := domain.ParseMoney("50.00")
	_, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: accountID,
		ToTigerBeetleID:   accountID,
		Amount:            monto,
		Type:              domain.TransactionTypeTransfer,
	})
	assert.ErrorIs(t, err, domain.ErrSameAccount)
}

func TestHistorialDeTransferencias(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	require.NoError(t, ledger.EnsureOperatorAccount(ctx))

	accountID := uniqueAccountID(t, 12)
	require.NoError(t, ledger.CreateAccount(ctx, accountID, domain.AccountTypeChecking))

	deposito, _ := domain.ParseMoney("1000.00")
	_, err := ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: big.NewInt(int64(domain.OperatorAccountID)),
		ToTigerBeetleID:   accountID,
		Amount:            deposito,
		Type:              domain.TransactionTypeDeposit,
	})
	require.NoError(t, err)

	retiro, _ := domain.ParseMoney("250.00")
	_, err = ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: accountID,
		ToTigerBeetleID:   big.NewInt(int64(domain.OperatorAccountID)),
		Amount:            retiro,
		Type:              domain.TransactionTypeWithdrawal,
	})
	require.NoError(t, err)

	transacciones, err := ledger.ListTransfers(ctx, accountID, ports.TransferFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, transacciones, 2)

	// Vienen en orden inverso: lo más reciente primero.
	assert.Equal(t, domain.TransactionTypeWithdrawal, transacciones[0].Type)
	assert.Equal(t, retiro, transacciones[0].Amount)
	assert.Equal(t, domain.DirectionOut, transacciones[0].Direction, "un retiro sale de la cuenta")

	assert.Equal(t, domain.TransactionTypeDeposit, transacciones[1].Type)
	assert.Equal(t, deposito, transacciones[1].Amount)
	assert.Equal(t, domain.DirectionIn, transacciones[1].Direction, "un depósito entra a la cuenta")
}

// Verifica el invariante contable fundamental: el dinero no se crea ni se
// destruye.
//
// Lo que sale de la cuenta del operador tiene que estar, exactamente, en las
// cuentas de los usuarios. Si esta suma no da cero, hay un bug contable.
func TestInvarianteContableLaSumaDaCero(t *testing.T) {
	ledger := newTestLedger(t)
	ctx := context.Background()

	require.NoError(t, ledger.EnsureOperatorAccount(ctx))

	saldoOperadorInicial, err := ledger.GetBalance(ctx, big.NewInt(int64(domain.OperatorAccountID)))
	require.NoError(t, err)

	cuentaA := uniqueAccountID(t, 13)
	cuentaB := uniqueAccountID(t, 14)
	require.NoError(t, ledger.CreateAccount(ctx, cuentaA, domain.AccountTypeChecking))
	require.NoError(t, ledger.CreateAccount(ctx, cuentaB, domain.AccountTypeSavings))

	deposito, _ := domain.ParseMoney("800.00")
	_, err = ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: big.NewInt(int64(domain.OperatorAccountID)),
		ToTigerBeetleID:   cuentaA,
		Amount:            deposito,
		Type:              domain.TransactionTypeDeposit,
	})
	require.NoError(t, err)

	transferencia, _ := domain.ParseMoney("300.00")
	_, err = ledger.Transfer(ctx, domain.TransferRequest{
		FromTigerBeetleID: cuentaA,
		ToTigerBeetleID:   cuentaB,
		Amount:            transferencia,
		Type:              domain.TransactionTypeTransfer,
	})
	require.NoError(t, err)

	saldoA, err := ledger.GetBalance(ctx, cuentaA)
	require.NoError(t, err)
	saldoB, err := ledger.GetBalance(ctx, cuentaB)
	require.NoError(t, err)
	saldoOperadorFinal, err := ledger.GetBalance(ctx, big.NewInt(int64(domain.OperatorAccountID)))
	require.NoError(t, err)

	// La transferencia interna entre A y B no cambia el total: sólo lo redistribuye.
	assert.Equal(t, domain.Money(50000), saldoA.Available, "A queda con 500.00")
	assert.Equal(t, domain.Money(30000), saldoB.Available, "B queda con 300.00")

	// El operador se movió exactamente lo que entró al sistema.
	variacionOperador := saldoOperadorFinal.Posted - saldoOperadorInicial.Posted
	assert.Equal(t, -deposito, variacionOperador,
		"lo que salió del operador debe igualar lo que entró a las cuentas de usuario")

	// El invariante: todo suma cero.
	total := (saldoA.Posted + saldoB.Posted) + variacionOperador
	assert.Equal(t, domain.Money(0), total,
		"el dinero no se crea ni se destruye: la suma de todos los saldos debe dar cero")
}
