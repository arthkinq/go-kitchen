package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/arthkinq/go-kitchen/internal/domain"
)

// isPostgresText reports whether s can be bound as a PostgreSQL text parameter.
//
// PostgreSQL rejects two kinds of input with SQLSTATE 22021: a malformed UTF-8 sequence and an
// embedded NUL, which a C string cannot carry. Both are well-formed as far as Go is concerned —
// "\x00" is even valid UTF-8 — so nothing upstream catches them, and the driver reports them as
// an ordinary query failure. Every such string comes from the client, so it is rejected at the
// edge: otherwise a client mistake is answered with a 500 and lands in the 5xx alerting.
func isPostgresText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, '\x00')
}

// Response envelope for successful responses.
type Response struct {
	Data any `json:"data"`
}

// ErrorDetail contains machine-readable and human-readable error descriptions.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorResponse envelope for API error responses.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// MaxBodyBytes limits request payloads to 1MB to protect against DoS attacks.
const MaxBodyBytes = 1 << 20 // 1 MB

// decodeJSON safely parses incoming JSON payload with size limits and EOF verification.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)

	if err := dec.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return fmt.Errorf("%w: request body exceeds 1MB limit", domain.ErrInvalidInput)
		}
		return fmt.Errorf("%w: invalid JSON syntax", domain.ErrInvalidInput)
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: request body must contain only a single JSON object", domain.ErrInvalidInput)
	}

	return nil
}

// respondJSON serializes data as JSON. A nil payload yields a bodiless response (e.g. 204).
func respondJSON(w http.ResponseWriter, status int, data any) {
	if data == nil {
		w.WriteHeader(status)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("failed to encode JSON response", "error", err)
	}
}

// respondError translates domain errors into structured, sanitized HTTP error responses.
func respondError(w http.ResponseWriter, err error) {
	var (
		status  int
		code    string
		message string
	)

	switch {
	case errors.Is(err, domain.ErrNotFound),
		errors.Is(err, domain.ErrRestaurantNotFound),
		errors.Is(err, domain.ErrCategoryNotFound),
		errors.Is(err, domain.ErrMenuItemNotFound):
		status = http.StatusNotFound
		code = "NOT_FOUND"
		message = "requested resource not found"

	case errors.Is(err, domain.ErrUnauthorized):
		status = http.StatusUnauthorized
		code = "UNAUTHORIZED"
		message = "missing or invalid credentials"

	case errors.Is(err, domain.ErrInvalidInput):
		status = http.StatusBadRequest
		code = "BAD_REQUEST"
		message = "invalid request data or syntax"

	case errors.Is(err, domain.ErrEmptyOrder):
		status = http.StatusBadRequest
		code = "EMPTY_ORDER"
		message = "order must contain at least one item"

	case errors.Is(err, domain.ErrOutOfStock):
		status = http.StatusConflict
		code = "OUT_OF_STOCK"
		message = "one or more items are out of stock"

	case errors.Is(err, domain.ErrConflict):
		status = http.StatusConflict
		code = "CONFLICT"
		message = "resource already exists or conflicts with existing state"

	case errors.Is(err, domain.ErrRestaurantInactive):
		status = http.StatusUnprocessableEntity
		code = "RESTAURANT_INACTIVE"
		message = "restaurant is currently closed or inactive"

	case errors.Is(err, domain.ErrItemUnavailable):
		status = http.StatusUnprocessableEntity
		code = "ITEM_UNAVAILABLE"
		message = "menu item is currently unavailable"

	case errors.Is(err, domain.ErrRestaurantMismatch):
		status = http.StatusUnprocessableEntity
		code = "RESTAURANT_MISMATCH"
		message = "all ordered items must belong to the same restaurant"

	case errors.Is(err, domain.ErrInvalidStatusTransition):
		status = http.StatusUnprocessableEntity
		code = "INVALID_STATUS_TRANSITION"
		message = "illegal order status transition"

	case errors.Is(err, domain.ErrOrderAlreadyProcessed):
		status = http.StatusUnprocessableEntity
		code = "ORDER_ALREADY_PROCESSED"
		message = "order cannot be cancelled in its current status"

	default:
		slog.Error("unhandled internal server error", "error", err)
		status = http.StatusInternalServerError
		code = "INTERNAL_SERVER_ERROR"
		message = "internal server error"
	}

	respondJSON(w, status, ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
		},
	})
}
