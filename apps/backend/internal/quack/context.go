package quack

import (
	"context"
	"strings"
	"unicode"
)

// contextKey keys the request-scoped values the services read from a
// context.
type contextKey int

const (
	requestIDKey contextKey = iota
	correlationIDKey
	auditSourceKey
	staffKey
)

// NewTraceID returns a fresh request or correlation ID.
func NewTraceID() string {
	return NewID()
}

// ContextWithTrace returns ctx carrying a request ID and a correlation ID.
// Invalid or empty IDs are replaced with new ones, and a missing correlation
// ID falls back to the request ID so one request is traceable end to end.
func ContextWithTrace(ctx context.Context, requestID, correlationID string) context.Context {
	requestID = normalizeTraceID(requestID)
	if requestID == "" {
		requestID = NewTraceID()
	}
	correlationID = normalizeTraceID(correlationID)
	if correlationID == "" {
		correlationID = requestID
	}
	ctx = context.WithValue(ctx, requestIDKey, requestID)
	return context.WithValue(ctx, correlationIDKey, correlationID)
}

// RequestIDFromContext returns the request ID in ctx, or "".
func RequestIDFromContext(ctx context.Context) string {
	return traceIDFromContext(ctx, requestIDKey)
}

// CorrelationIDFromContext returns the correlation ID in ctx, or "".
func CorrelationIDFromContext(ctx context.Context) string {
	return traceIDFromContext(ctx, correlationIDKey)
}

// TraceIDsFromContext returns the request and correlation IDs in ctx. The
// correlation ID falls back to the request ID.
func TraceIDsFromContext(ctx context.Context) (requestID, correlationID string) {
	requestID = RequestIDFromContext(ctx)
	correlationID = CorrelationIDFromContext(ctx)
	if correlationID == "" {
		correlationID = requestID
	}
	return requestID, correlationID
}

// ensureTraceContext gives ctx request and correlation IDs if it lacks them,
// so every audit entry a service writes is traceable.
func ensureTraceContext(ctx context.Context) context.Context {
	requestID, correlationID := RequestIDFromContext(ctx), CorrelationIDFromContext(ctx)
	if requestID != "" && correlationID != "" {
		return ctx
	}
	return ContextWithTrace(ctx, requestID, correlationID)
}

func traceIDFromContext(ctx context.Context, key contextKey) string {
	value, _ := ctx.Value(key).(string)
	return normalizeTraceID(value)
}

// normalizeTraceID returns value if it is a safe trace ID (at most 128
// letters, digits, and - _ . :) and "" otherwise. Trace IDs arrive in
// headers, so anything else is dropped rather than logged.
func normalizeTraceID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-_.:", r) {
			return ""
		}
	}
	return value
}

// ContextWithAuditSource returns ctx recording which adapter is calling, so
// audit entries written deeper in the services carry the right source.
func ContextWithAuditSource(ctx context.Context, source AuditSource) context.Context {
	return context.WithValue(ctx, auditSourceKey, source)
}

// AuditSourceFromContext returns the audit source in ctx, defaulting to the
// API.
func AuditSourceFromContext(ctx context.Context) AuditSource {
	if source, ok := ctx.Value(auditSourceKey).(AuditSource); ok && validAuditSource(source) {
		return source
	}
	return AuditSourceAPI
}

// ContextWithStaff returns ctx carrying the caller's live staff context, as
// resolved by an adapter's guild authorization.
func ContextWithStaff(ctx context.Context, staff *GuildStaffContext) context.Context {
	return context.WithValue(ctx, staffKey, staff)
}

// StaffFromContext returns the staff context stored by ContextWithStaff, or
// nil outside a guild request.
func StaffFromContext(ctx context.Context) *GuildStaffContext {
	staff, _ := ctx.Value(staffKey).(*GuildStaffContext)
	return staff
}
