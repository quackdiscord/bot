package quack

import (
	"context"
	"strings"
	"unicode"

	"github.com/oklog/ulid/v2"
)

// NewID returns a new ULID. Every record and lease token uses this format so
// IDs sort by creation time.
func NewID() string {
	return ulid.Make().String()
}

// NewTraceID returns a fresh request or correlation ID.
func NewTraceID() string {
	return NewID()
}

type traceKey int

const (
	requestIDKey traceKey = iota
	correlationIDKey
	auditSourceKey
)

// ContextWithTrace returns ctx carrying a request ID and a correlation ID.
// Invalid or empty IDs are replaced; a missing correlation ID falls back to
// the request ID so one request is traceable end to end.
func ContextWithTrace(ctx context.Context, requestID, correlationID string) context.Context {
	ctx = ContextWithRequestID(ctx, requestID)
	if NormalizeTraceID(correlationID) == "" {
		correlationID = RequestIDFromContext(ctx)
	}
	return ContextWithCorrelationID(ctx, correlationID)
}

// ContextWithRequestID returns ctx carrying requestID, or a new ID when
// requestID is empty or unsafe.
func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	requestID = NormalizeTraceID(requestID)
	if requestID == "" {
		requestID = NewTraceID()
	}
	return context.WithValue(ctx, requestIDKey, requestID)
}

// ContextWithCorrelationID returns ctx carrying correlationID, or a new ID
// when correlationID is empty or unsafe.
func ContextWithCorrelationID(ctx context.Context, correlationID string) context.Context {
	correlationID = NormalizeTraceID(correlationID)
	if correlationID == "" {
		correlationID = NewTraceID()
	}
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

// NormalizeTraceID returns value if it is a safe trace ID (at most 128
// letters, digits, and - _ . :) and "" otherwise. Trace IDs arrive in
// headers, so anything else is dropped rather than logged.
func NormalizeTraceID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' || r == ':' {
			continue
		}
		return ""
	}
	return value
}

func traceIDFromContext(ctx context.Context, key traceKey) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(key).(string)
	return NormalizeTraceID(value)
}

// ensureTraceContext gives ctx request and correlation IDs if it lacks them,
// so every audit entry a service writes is traceable.
func ensureTraceContext(ctx context.Context) context.Context {
	if RequestIDFromContext(ctx) != "" && CorrelationIDFromContext(ctx) != "" {
		return ctx
	}
	return ContextWithTrace(ctx, RequestIDFromContext(ctx), CorrelationIDFromContext(ctx))
}

// ContextWithAuditSource returns ctx recording which adapter is calling, so
// audit entries written deeper in the services carry the right source.
func ContextWithAuditSource(ctx context.Context, source AuditSource) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, auditSourceKey, source)
}

// AuditSourceFromContext returns the audit source in ctx, defaulting to the
// API.
func AuditSourceFromContext(ctx context.Context) AuditSource {
	if ctx != nil {
		if source, ok := ctx.Value(auditSourceKey).(AuditSource); ok && validAuditSource(source) {
			return source
		}
	}
	return AuditSourceAPI
}

// AuditSourceForModuleAction returns the audit source for a module audit
// action. Imports and honeypot automation are attributed to themselves;
// staff-driven module actions keep the caller's source.
func AuditSourceForModuleAction(ctx context.Context, action string) AuditSource {
	action = strings.ToLower(strings.TrimSpace(action))
	if strings.Contains(action, "v4_import") || strings.Contains(action, "v4_settings_import") {
		return AuditSourceImport
	}
	if strings.HasPrefix(action, "honeypot.trigger.") || action == "honeypot.case.created" || action == "honeypot.configuration.disabled" {
		return AuditSourceHoneypot
	}
	return AuditSourceFromContext(ctx)
}

// auditSourceForCaseSource maps where a case came from to the audit source
// its entries record.
func auditSourceForCaseSource(source CaseSource) AuditSource {
	switch source {
	case CaseSourceDiscord:
		return AuditSourceDiscord
	case CaseSourceHoneypot:
		return AuditSourceHoneypot
	case CaseSourceV4Import:
		return AuditSourceImport
	default:
		return AuditSourceWeb
	}
}
