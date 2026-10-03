package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/quackdiscord/bot/internal/quack"
)

// errorCode is the machine-readable failure class in the error envelope. The
// dashboard branches on these values, so they never change.
type errorCode string

const (
	codeValidation     errorCode = "validation_failed"
	codeAuthentication errorCode = "authentication_required"
	codeReauthenticate errorCode = "reauthentication_required"
	codeAuthorization  errorCode = "authorization_denied"
	codeNotFound       errorCode = "not_found"
	codeConflict       errorCode = "conflict"
	codeRateLimited    errorCode = "rate_limited"
	codeCSRF           errorCode = "csrf_rejected"
	codeOrigin         errorCode = "origin_rejected"
	codeBodyTooLarge   errorCode = "body_too_large"
	codeDependency     errorCode = "dependency_unavailable"
	codeInternal       errorCode = "internal_error"
)

const jsonContentType = "application/json; charset=utf-8"

// errorResponse is the body of every response with a status of 400 or more.
type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code          errorCode `json:"code"`
	Message       string    `json:"message"`
	RequestID     string    `json:"request_id"`
	CorrelationID string    `json:"correlation_id"`
}

// writeJSON writes v as the response body. Like the dashboard has always
// seen, the body has no trailing newline and HTML characters are escaped.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		status, body = http.StatusInternalServerError, nil
	}
	w.Header().Set("Content-Type", jsonContentType)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeError writes the error envelope with the request's trace IDs.
func writeError(w http.ResponseWriter, r *http.Request, status int, code errorCode, message string) {
	writeJSON(w, status, newErrorResponse(r.Context(), code, message))
}

func newErrorResponse(ctx context.Context, code errorCode, message string) errorResponse {
	requestID, correlationID := quack.TraceIDsFromContext(ctx)
	return errorResponse{Error: errorDetail{
		Code:          code,
		Message:       message,
		RequestID:     requestID,
		CorrelationID: correlationID,
	}}
}

// defaultError is the code and message used when a handler fails without an
// envelope of its own, such as a module writing {"error": "..."}.
func defaultError(status int) (errorCode, string) {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return codeValidation, "request validation failed"
	case http.StatusUnauthorized:
		return codeAuthentication, "authentication required"
	case http.StatusForbidden:
		return codeAuthorization, "access denied"
	case http.StatusNotFound:
		return codeNotFound, "resource not found"
	case http.StatusConflict:
		return codeConflict, "request conflicts with current state"
	case http.StatusRequestEntityTooLarge:
		return codeBodyTooLarge, "request body is too large"
	case http.StatusTooManyRequests:
		return codeRateLimited, "rate limit exceeded"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return codeDependency, "dependency unavailable"
	default:
		return codeInternal, "request failed"
	}
}

// normalizeError returns body if it is already an envelope, and otherwise a
// default envelope for status. Raw error text never reaches the client.
func normalizeError(ctx context.Context, status int, body []byte) []byte {
	var envelope errorResponse
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Code == "" {
		code, message := defaultError(status)
		envelope = newErrorResponse(ctx, code, message)
	}
	normalized, _ := json.Marshal(envelope)
	return normalized
}

// decodeJSON decodes exactly one JSON value into v and rejects unknown
// fields, so retired or misspelled fields fail loudly instead of being
// ignored.
func decodeJSON(r *http.Request, v any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func writeCaseError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, quack.ErrCaseValidation):
		writeError(w, r, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, quack.ErrCasePermissionDenied), errors.Is(err, quack.ErrAuthorizationDenied):
		writeError(w, r, http.StatusForbidden, codeAuthorization, err.Error())
	case errors.Is(err, quack.ErrCaseTemplateNotAvailable), errors.Is(err, quack.ErrCaseNotFound):
		writeError(w, r, http.StatusNotFound, codeNotFound, err.Error())
	default:
		writeError(w, r, http.StatusInternalServerError, codeInternal, "case operation failed")
	}
}

func writeTemplateError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, quack.ErrTemplatePermissionDenied):
		writeError(w, r, http.StatusForbidden, codeAuthorization, "template access denied")
	case errors.Is(err, quack.ErrTemplateValidation):
		writeError(w, r, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, quack.ErrTemplateNotFound):
		writeError(w, r, http.StatusNotFound, codeNotFound, err.Error())
	default:
		writeError(w, r, http.StatusInternalServerError, codeInternal, "template operation failed")
	}
}

func writeSettingsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, quack.ErrGuildSettingsValidation):
		writeError(w, r, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, quack.ErrGuildSettingsPermissionDenied):
		writeError(w, r, http.StatusForbidden, codeAuthorization, err.Error())
	case errors.Is(err, quack.ErrGuildSettingsNotFound):
		writeError(w, r, http.StatusNotFound, codeNotFound, err.Error())
	default:
		writeError(w, r, http.StatusInternalServerError, codeInternal, "guild settings operation failed")
	}
}

// writeAppealError hides whether an ineligible case exists: both it and a
// missing appeal are 404s.
func writeAppealError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, quack.ErrAppealValidation):
		writeError(w, r, http.StatusBadRequest, codeValidation, "invalid appeal request")
	case errors.Is(err, quack.ErrAppealPermissionDenied):
		writeError(w, r, http.StatusForbidden, codeAuthorization, "appeal access denied")
	case errors.Is(err, quack.ErrAppealNotFound), errors.Is(err, quack.ErrAppealCaseIneligible):
		writeError(w, r, http.StatusNotFound, codeNotFound, "appeal not found")
	case errors.Is(err, quack.ErrAppealConflict):
		writeError(w, r, http.StatusConflict, codeConflict, "appeal state conflict")
	default:
		writeError(w, r, http.StatusInternalServerError, codeInternal, "appeal operation failed")
	}
}

func writeAuditError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, quack.ErrAuditValidation):
		writeError(w, r, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, quack.ErrAuditPermissionDenied):
		writeError(w, r, http.StatusForbidden, codeAuthorization, err.Error())
	default:
		writeError(w, r, http.StatusInternalServerError, codeInternal, "audit operation failed")
	}
}

func writeStatisticsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, quack.ErrStatisticsValidation):
		writeError(w, r, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, quack.ErrStatisticsPermissionDenied):
		writeError(w, r, http.StatusForbidden, codeAuthorization, "statistics access denied")
	default:
		writeError(w, r, http.StatusInternalServerError, codeInternal, "statistics operation failed")
	}
}
