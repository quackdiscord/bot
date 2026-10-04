package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/quackdiscord/bot/internal/quack"
)

const jsonContentType = "application/json; charset=utf-8"

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

// serviceErrors maps one service's sentinel errors to responses. Handlers
// pass every service error through one of these, so a sentinel always gets
// the same status, and anything unrecognized becomes a 500 (or
// fallbackStatus) that names the operation without leaking the cause.
type serviceErrors struct {
	known []knownError
	// fallback is the message for errors that match nothing in known.
	fallback string
	// fallbackStatus and fallbackCode replace 500 and internal_error for
	// unrecognized errors when set.
	fallbackStatus int
	fallbackCode   errorCode
}

// withFallback returns a copy of e that answers unrecognized errors with
// message, for handlers whose failure message names one operation.
func (e serviceErrors) withFallback(message string) serviceErrors {
	e.fallback = message
	return e
}

// knownError is one sentinel's response. An empty message sends the
// error's own text, which the services write to be shown to staff.
type knownError struct {
	target  error
	status  int
	code    errorCode
	message string
}

var caseErrors = serviceErrors{
	known: []knownError{
		{quack.ErrCaseValidation, http.StatusBadRequest, codeValidation, ""},
		{quack.ErrCasePermissionDenied, http.StatusForbidden, codeAuthorization, ""},
		{quack.ErrAuthorizationDenied, http.StatusForbidden, codeAuthorization, ""},
		{quack.ErrCaseTemplateNotAvailable, http.StatusNotFound, codeNotFound, ""},
		{quack.ErrCaseNotFound, http.StatusNotFound, codeNotFound, ""},
	},
	fallback: "case operation failed",
}

var templateErrors = serviceErrors{
	known: []knownError{
		{quack.ErrTemplatePermissionDenied, http.StatusForbidden, codeAuthorization, "template access denied"},
		{quack.ErrTemplateValidation, http.StatusBadRequest, codeValidation, ""},
		{quack.ErrTemplateNotFound, http.StatusNotFound, codeNotFound, ""},
		{quack.ErrTemplateConflict, http.StatusConflict, codeConflict, ""},
	},
	fallback: "template operation failed",
}

var settingsErrors = serviceErrors{
	known: []knownError{
		{quack.ErrGuildSettingsValidation, http.StatusBadRequest, codeValidation, ""},
		{quack.ErrGuildSettingsPermissionDenied, http.StatusForbidden, codeAuthorization, ""},
		{quack.ErrGuildSettingsNotFound, http.StatusNotFound, codeNotFound, ""},
	},
	fallback: "guild settings operation failed",
}

// appealErrors never sends the error's own text, and answers an ineligible
// case exactly like a missing appeal, so members cannot probe which of
// their cases exist or are appealable.
var appealErrors = serviceErrors{
	known: []knownError{
		{quack.ErrAppealValidation, http.StatusBadRequest, codeValidation, "invalid appeal request"},
		{quack.ErrAppealPermissionDenied, http.StatusForbidden, codeAuthorization, "appeal access denied"},
		{quack.ErrAppealNotFound, http.StatusNotFound, codeNotFound, "appeal not found"},
		{quack.ErrAppealCaseIneligible, http.StatusNotFound, codeNotFound, "appeal not found"},
		{quack.ErrAppealConflict, http.StatusConflict, codeConflict, "appeal state conflict"},
	},
	fallback: "appeal operation failed",
}

// guildListErrors maps failures listing the caller's Discord guilds. The
// list comes straight from Discord, so anything unrecognized is a 502.
var guildListErrors = serviceErrors{
	fallback:       "failed to list discord guilds",
	fallbackStatus: http.StatusBadGateway,
	fallbackCode:   codeDependency,
}

var auditErrors = serviceErrors{
	known: []knownError{
		{quack.ErrAuditValidation, http.StatusBadRequest, codeValidation, ""},
		{quack.ErrAuditPermissionDenied, http.StatusForbidden, codeAuthorization, ""},
	},
	fallback: "audit operation failed",
}

var statisticsErrors = serviceErrors{
	known: []knownError{
		{quack.ErrStatisticsValidation, http.StatusBadRequest, codeValidation, ""},
		{quack.ErrStatisticsPermissionDenied, http.StatusForbidden, codeAuthorization, "statistics access denied"},
	},
	fallback: "statistics operation failed",
}

// write answers with the first known error that err matches.
func (e serviceErrors) write(w http.ResponseWriter, r *http.Request, err error) {
	for _, known := range e.known {
		if !errors.Is(err, known.target) {
			continue
		}
		message := known.message
		if message == "" {
			message = err.Error()
		}
		writeError(w, r, known.status, known.code, message)
		return
	}
	status, code := http.StatusInternalServerError, codeInternal
	if e.fallbackStatus != 0 {
		status, code = e.fallbackStatus, e.fallbackCode
	}
	writeError(w, r, status, code, e.fallback)
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

// decode reads the request body into v, answering 400 with message and
// returning false if it is not a valid payload.
func decode(w http.ResponseWriter, r *http.Request, v any, message string) bool {
	if err := decodeJSON(r, v); err != nil {
		writeError(w, r, http.StatusBadRequest, codeValidation, message)
		return false
	}
	return true
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
