// Package response centraliza el formato de las respuestas HTTP.
//
// Toda la API responde con la misma forma, así el frontend puede manejar
// errores de manera uniforme sin adivinar la estructura según el endpoint.
package response

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Moisinho/banca-ai/apps/api/internal/logger"
)

// ErrorBody es el cuerpo de una respuesta de error.
//
// Code es un identificador estable en inglés que el frontend compara.
// Message es el texto en español que ve la persona.
// Esa separación es deliberada: el código no cambia aunque reescribamos el
// mensaje, así que el frontend nunca depende de la redacción exacta.
type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

type errorResponse struct {
	Error ErrorBody `json:"error"`
}

// JSON escribe una respuesta exitosa.
func JSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if payload == nil {
		return
	}

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// El estado ya se envió, así que no podemos cambiar la respuesta.
		// Lo único útil acá es dejar constancia en los logs.
		slog.ErrorContext(r.Context(), "no se pudo serializar la respuesta",
			"error", err,
			"request_id", logger.RequestIDFrom(r.Context()),
		)
	}
}

// Error escribe una respuesta de error con código y mensaje.
func Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, r, status, errorResponse{
		Error: ErrorBody{Code: code, Message: message},
	})
}

// ValidationError escribe un error de validación con el detalle por campo,
// para que el formulario del frontend pueda marcar exactamente qué está mal.
func ValidationError(w http.ResponseWriter, r *http.Request, fields map[string]string) {
	JSON(w, r, http.StatusUnprocessableEntity, errorResponse{
		Error: ErrorBody{
			Code:    "VALIDATION_ERROR",
			Message: "Los datos enviados no son válidos",
			Fields:  fields,
		},
	})
}

// NoContent responde 204, para operaciones que no devuelven cuerpo.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}
