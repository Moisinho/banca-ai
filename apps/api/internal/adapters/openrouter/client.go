// Package openrouter implementa ports.AIProvider sobre la API de OpenRouter.
//
// OpenRouter expone una interfaz compatible con la de OpenAI, así que el mismo
// adaptador sirve para acceder a Claude, GPT y otros modelos cambiando sólo el
// identificador del modelo.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Moisinho/banca-ai/apps/api/internal/ports"
)

// requestTimeout acota cuánto esperamos al modelo.
//
// Una respuesta con uso de herramientas puede tardar bastante, pero pasado
// este punto es preferible fallar y avisar que dejar al usuario esperando.
const requestTimeout = 60 * time.Second

// Fallos del proveedor que el operador del sistema puede resolver.
//
// Se distinguen de un error genérico para que el mensaje al usuario sea útil y
// para que quede claro en los logs que no es un fallo del código.
var (
	// ErrInsufficientCredits: la cuenta de OpenRouter se quedó sin saldo.
	ErrInsufficientCredits = errors.New("la cuenta del proveedor de IA no tiene créditos suficientes")

	// ErrRateLimited: se superó el límite de peticiones del proveedor.
	ErrRateLimited = errors.New("se alcanzó el límite de peticiones del proveedor de IA")

	// ErrInvalidAPIKey: la clave configurada no es válida.
	ErrInvalidAPIKey = errors.New("la clave del proveedor de IA no es válida")
)

// Client habla con la API de OpenRouter.
type Client struct {
	apiKey  string
	model   string
	baseURL string
	http    *http.Client
	log     *slog.Logger
}

var _ ports.AIProvider = (*Client)(nil)

func New(apiKey, model, baseURL string, log *slog.Logger) *Client {
	return &Client{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		http:    &http.Client{Timeout: requestTimeout},
		log:     log,
	}
}

// ------------------------------------------------------------------------------
// Formato de la API
// ------------------------------------------------------------------------------

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []toolSpec    `json:"tools,omitempty"`

	// "auto" deja que el modelo decida si usar una herramienta o responder
	// directamente. Forzar el uso haría que invente llamadas cuando la
	// pregunta no las necesita.
	ToolChoice string `json:"tool_choice,omitempty"`

	// Temperatura baja: en un contexto bancario preferimos respuestas
	// predecibles y literales antes que creativas.
	Temperature float64 `json:"temperature"`

	// MaxTokens acota la respuesta.
	//
	// Sin este límite el proveedor reserva el máximo de la ventana del modelo
	// (decenas de miles de tokens) y rechaza la petición si el saldo de la
	// cuenta no cubre ese techo, aunque la respuesta real vaya a ser corta.
	// Una respuesta bancaria entra de sobra en este margen.
	MaxTokens int `json:"max_tokens"`
}

// maxResponseTokens es el techo de tokens de una respuesta.
//
// Alcanza para explicar una operación o listar movimientos, y evita que una
// respuesta desbocada consuma la cuota.
const maxResponseTokens = 2000

type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolSpec struct {
	Type     string       `json:"type"`
	Function functionSpec `json:"function"`
}

type functionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name string `json:"name"`
	// Los argumentos llegan como una cadena JSON, no como objeto: así lo
	// define el formato de OpenAI.
	Arguments string `json:"arguments"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []toolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`

	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

// ------------------------------------------------------------------------------
// Implementación del puerto
// ------------------------------------------------------------------------------

// Complete envía la conversación al modelo y devuelve su respuesta.
func (c *Client) Complete(ctx context.Context, req ports.CompletionRequest) (ports.CompletionResponse, error) {
	payload := chatRequest{
		Model:       c.model,
		Messages:    toAPIMessages(req.Messages),
		Tools:       toAPITools(req.Tools),
		Temperature: 0.2,
		MaxTokens:   maxResponseTokens,
	}

	if len(payload.Tools) > 0 {
		payload.ToolChoice = "auto"
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ports.CompletionResponse{}, fmt.Errorf("no se pudo serializar la petición: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ports.CompletionResponse{}, fmt.Errorf("no se pudo construir la petición: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	// OpenRouter usa estas cabeceras para atribuir el consumo a la aplicación.
	httpReq.Header.Set("HTTP-Referer", "https://github.com/Moisinho/banca-ai")
	httpReq.Header.Set("X-Title", "Banca AI")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return ports.CompletionResponse{}, fmt.Errorf("no se pudo contactar al proveedor de IA: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ports.CompletionResponse{}, fmt.Errorf("no se pudo leer la respuesta del proveedor: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		c.log.Error("el proveedor de IA devolvió un error",
			"status", resp.StatusCode,
			"body", string(responseBody),
		)

		// Distinguimos los fallos que el operador puede resolver de los
		// transitorios: un 402 significa que hay que cargar créditos, no que
		// haya un problema en el código.
		switch resp.StatusCode {
		case http.StatusPaymentRequired:
			return ports.CompletionResponse{}, ErrInsufficientCredits
		case http.StatusTooManyRequests:
			return ports.CompletionResponse{}, ErrRateLimited
		case http.StatusUnauthorized, http.StatusForbidden:
			return ports.CompletionResponse{}, ErrInvalidAPIKey
		}

		return ports.CompletionResponse{}, fmt.Errorf("el proveedor de IA respondió con estado %d", resp.StatusCode)
	}

	var parsed chatResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return ports.CompletionResponse{}, fmt.Errorf("no se pudo interpretar la respuesta del proveedor: %w", err)
	}

	// OpenRouter puede devolver 200 con un error en el cuerpo.
	if parsed.Error != nil {
		return ports.CompletionResponse{}, fmt.Errorf("el proveedor de IA devolvió un error: %s", parsed.Error.Message)
	}

	if len(parsed.Choices) == 0 {
		return ports.CompletionResponse{}, fmt.Errorf("el proveedor de IA no devolvió ninguna respuesta")
	}

	choice := parsed.Choices[0]

	calls := make([]ports.ToolCall, 0, len(choice.Message.ToolCalls))
	for _, call := range choice.Message.ToolCalls {
		// Los argumentos vienen como cadena JSON. Si el modelo genera algo
		// malformado, lo registramos y descartamos esa llamada en lugar de
		// tumbar la conversación entera.
		args := map[string]any{}
		if call.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				c.log.Warn("el modelo generó argumentos inválidos",
					"tool", call.Function.Name,
					"arguments", call.Function.Arguments,
					"error", err,
				)
				continue
			}
		}

		calls = append(calls, ports.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: args,
		})
	}

	return ports.CompletionResponse{
		Content:   choice.Message.Content,
		ToolCalls: calls,
	}, nil
}

func toAPIMessages(messages []ports.ChatMessage) []chatMessage {
	out := make([]chatMessage, 0, len(messages))

	for _, m := range messages {
		msg := chatMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}

		for _, call := range m.ToolCalls {
			arguments, err := json.Marshal(call.Arguments)
			if err != nil {
				arguments = []byte("{}")
			}

			msg.ToolCalls = append(msg.ToolCalls, toolCall{
				ID:   call.ID,
				Type: "function",
				Function: functionCall{
					Name:      call.Name,
					Arguments: string(arguments),
				},
			})
		}

		out = append(out, msg)
	}

	return out
}

func toAPITools(tools []ports.ToolDefinition) []toolSpec {
	out := make([]toolSpec, 0, len(tools))

	for _, t := range tools {
		out = append(out, toolSpec{
			Type: "function",
			Function: functionSpec{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	return out
}
