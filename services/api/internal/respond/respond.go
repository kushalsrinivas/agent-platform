// Package respond contains the HTTP response and error shape shared by all API handlers.
package respond

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// ErrorDetail is the stable machine-readable error shape.
type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   any    `json:"details,omitempty"`
	RequestID string `json:"request_id"`
}

type errorBody struct {
	Error ErrorDetail `json:"error"`
}

// JSON writes a JSON response.
func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Error writes the platform error envelope with the request ID from chi.
func Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	ErrorWithDetails(w, r, status, code, message, nil)
}

// ErrorWithDetails adds non-secret structured context.
func ErrorWithDetails(w http.ResponseWriter, r *http.Request, status int, code, message string, details any) {
	JSON(w, status, errorBody{Error: ErrorDetail{
		Code:      code,
		Message:   message,
		Details:   details,
		RequestID: middleware.GetReqID(r.Context()),
	}})
}

// Decode reads one JSON value, rejects unknown fields, and enforces a body limit.
func Decode(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

// IsBodyTooLarge reports whether Decode hit the byte limit.
func IsBodyTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}
