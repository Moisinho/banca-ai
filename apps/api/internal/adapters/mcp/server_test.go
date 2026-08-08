package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
)

// El test más importante de este paquete.
//
// Ninguna herramienta puede aceptar un identificador de usuario como parámetro.
// Si alguna lo hiciera, el modelo podría intentar operar en nombre de otra
// persona, y una inyección de prompt pasaría de ser una molestia a ser un
// problema de seguridad real.
//
// Este test recorre las estructuras de argumentos por reflexión: si alguien
// agrega un campo sospechoso en el futuro, falla.
func TestNingunaHerramientaAceptaIdentificadorDeUsuario(t *testing.T) {
	// Nombres que jamás deben aparecer como parámetro de una herramienta.
	prohibidos := []string{
		"userid", "user_id", "user", "usuario",
		"accountid", "account_id", // el id interno tampoco: se resuelve del lado del servidor
		"ownerid", "owner_id",
		"customerid", "customer_id",
	}

	argTypes := []any{
		getBalanceArgs{},
		listTransactionsArgs{},
		depositArgs{},
		withdrawArgs{},
		transferArgs{},
		getAccountInfoArgs{},
	}

	for _, argType := range argTypes {
		typ := reflect.TypeOf(argType)

		t.Run(typ.Name(), func(t *testing.T) {
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)

				// Se comprueba tanto el nombre del campo como su etiqueta JSON,
				// que es lo que el modelo realmente ve.
				jsonTag := strings.Split(field.Tag.Get("json"), ",")[0]

				for _, prohibido := range prohibidos {
					assert.NotEqual(t, prohibido, strings.ToLower(field.Name),
						"el campo %s.%s permitiría al modelo elegir sobre quién opera",
						typ.Name(), field.Name)

					assert.NotEqual(t, prohibido, strings.ToLower(jsonTag),
						"la etiqueta JSON %q en %s permitiría al modelo elegir sobre quién opera",
						jsonTag, typ.Name())
				}
			}
		})
	}
}

// Sin usuario en el contexto, ninguna herramienta puede operar.
//
// Es la segunda mitad de la defensa: el usuario sólo puede venir del contexto,
// y si no está, la herramienta falla en lugar de asumir uno.
func TestSinUsuarioEnContextoLasHerramientasFallan(t *testing.T) {
	ctx := context.Background()

	_, ok := userIDFrom(ctx)
	assert.False(t, ok, "un contexto sin usuario no puede resolver ninguno")
}

func TestWithUserIDGuardaYRecupera(t *testing.T) {
	ctx := WithUserID(context.Background(), "usuario-123")

	got, ok := userIDFrom(ctx)
	require.True(t, ok)
	assert.Equal(t, "usuario-123", got)
}

// La clave del contexto es de un tipo privado del paquete.
//
// Eso impide que otro paquete escriba un usuario distinto: sólo este paquete
// puede decidir en nombre de quién se ejecuta una herramienta.
func TestLaClaveDelContextoEsPrivada(t *testing.T) {
	// Escribir con una clave de otro tipo no debe afectar la lectura.
	type claveAjena struct{}
	ctx := context.WithValue(context.Background(), claveAjena{}, "atacante")

	_, ok := userIDFrom(ctx)
	assert.False(t, ok, "una clave de otro tipo no puede suplantar al usuario")
}

// Las herramientas que mueven dinero deben dejar claro que no ejecutan nada.
//
// Si la descripción no lo dijera, el modelo podría anunciarle a la persona que
// la transferencia ya se hizo cuando en realidad está esperando confirmación.
//
// Se consultan las herramientas realmente registradas, no una lista escrita a
// mano: si alguien agrega una herramienta nueva que mueve dinero sin la
// advertencia, este test falla.
func TestLasHerramientasQueMuevenDineroAvisanQueNoEjecutan(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(nil, nil, log)

	session := connectTestSession(t, server)

	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.NotEmpty(t, listed.Tools, "el servidor debe exponer herramientas")

	revisadas := 0
	for _, tool := range listed.Tools {
		if !requiresConfirmation(tool.Name) {
			continue
		}
		revisadas++

		t.Run(tool.Name, func(t *testing.T) {
			assert.Contains(t, tool.Description, "NO ejecuta",
				"la descripción debe advertir que la operación no se ejecuta sin confirmación")
			assert.Contains(t, tool.Description, "confirmar",
				"la descripción debe mencionar que el usuario debe confirmar")
		})
	}

	assert.Equal(t, 3, revisadas, "deben existir tres herramientas que mueven dinero")
}

// Las herramientas expuestas son exactamente las esperadas.
//
// Si aparece una nueva sin pasar por revisión, este test lo detecta.
func TestElServidorExponeLasHerramientasEsperadas(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(nil, nil, log)

	session := connectTestSession(t, server)

	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)

	nombres := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		nombres = append(nombres, tool.Name)
	}

	assert.ElementsMatch(t, []string{
		"get_balance",
		"list_transactions",
		"get_account_info",
		"deposit",
		"withdraw",
		"transfer",
	}, nombres)
}

// Ningún esquema publicado contiene un campo de usuario.
//
// Complementa al test por reflexión: verifica lo que el modelo realmente
// recibe, ya serializado, no sólo las estructuras de Go.
func TestLosEsquemasPublicadosNoExponenIdentificadorDeUsuario(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(nil, nil, log)

	session := connectTestSession(t, server)

	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)

	prohibidos := []string{"userid", "user_id", "accountid", "account_id", "owner"}

	for _, tool := range listed.Tools {
		t.Run(tool.Name, func(t *testing.T) {
			raw, err := json.Marshal(tool.InputSchema)
			require.NoError(t, err)

			esquema := strings.ToLower(string(raw))
			for _, prohibido := range prohibidos {
				assert.NotContains(t, esquema, `"`+prohibido+`"`,
					"el esquema de %s expone %q, lo que permitiría al modelo elegir sobre quién opera",
					tool.Name, prohibido)
			}
		})
	}
}

// connectTestSession abre una sesión in-process contra el servidor.
func connectTestSession(t *testing.T, server *Server) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()

	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()

	_, err := server.MCPServer().Connect(ctx, serverTransport, nil)
	require.NoError(t, err)

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)

	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestMovesMoneyDistingueLasHerramientas(t *testing.T) {
	// Las de escritura requieren confirmación del usuario.
	escritura := []string{"deposit", "withdraw", "transfer"}
	for _, tool := range escritura {
		assert.True(t, requiresConfirmation(tool), "%s mueve dinero", tool)
	}

	// Las de lectura se ejecutan directamente.
	lectura := []string{"get_balance", "list_transactions", "get_account_info"}
	for _, tool := range lectura {
		assert.False(t, requiresConfirmation(tool), "%s sólo lee", tool)
	}
}

// El resultado de una propuesta debe decir explícitamente que falta confirmar.
func TestElTextoDeLaPropuestaAdvierteQueFaltaConfirmar(t *testing.T) {
	proposal := PendingOperation{
		Operation:   "transfer",
		Amount:      30000,
		Currency:    "USD",
		FromAccount: "4001-1111-2222-3333",
		ToAccount:   "4001-4444-5555-6666",
		Description: "Pago de alquiler",
	}

	result := proposalResult(proposal)
	text := extractResultText(result)

	assert.Contains(t, text, "PENDIENTE DE CONFIRMACIÓN")
	assert.Contains(t, text, "NO se ha movido")
	assert.Contains(t, text, "300.00", "el monto debe aparecer para que la persona lo revise")
	assert.Contains(t, text, "4001-4444-5555-6666", "la cuenta destino debe aparecer")
}

// Un error de una herramienta se marca como tal, para que el modelo pueda
// explicarlo en vez de continuar como si nada.
func TestErrorResultSeMarcaComoError(t *testing.T) {
	result := errorResult("No hay fondos suficientes.")

	assert.True(t, result.IsError)
	assert.Contains(t, extractResultText(result), "fondos suficientes")
}

// Los errores del dominio nunca revelan que una cuenta ajena existe.
func TestElMensajeDeCuentaAjenaNoRevelaSuExistencia(t *testing.T) {
	// Una cuenta de otra persona y una inexistente producen el mismo texto:
	// distinguirlas permitiría enumerar cuentas del sistema.
	assert.Equal(t,
		userMessage(domain.ErrForbidden),
		userMessage(domain.ErrAccountNotFound),
		"una cuenta ajena y una inexistente deben ser indistinguibles",
	)
}

// Los esquemas de las herramientas se serializan a JSON válido.
//
// Si no lo hicieran, el proveedor de IA rechazaría la petición entera.
func TestLosArgumentosSeSerializanAJSON(t *testing.T) {
	argTypes := []any{
		getBalanceArgs{AccountNumber: "4001-1111-2222-3333"},
		listTransactionsArgs{Limit: 10, Type: "deposit"},
		depositArgs{Amount: "100.00", Description: "Prueba"},
		withdrawArgs{Amount: "50.00"},
		transferArgs{ToAccountNumber: "4001-4444-5555-6666", Amount: "25.50"},
		getAccountInfoArgs{},
	}

	for _, args := range argTypes {
		t.Run(reflect.TypeOf(args).Name(), func(t *testing.T) {
			raw, err := json.Marshal(args)
			require.NoError(t, err)

			var decoded map[string]any
			require.NoError(t, json.Unmarshal(raw, &decoded))
		})
	}
}
