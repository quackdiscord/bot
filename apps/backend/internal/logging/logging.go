// Package logging configures standard slog output for the process and propagates
// request traces. Logs contain operational metadata; case content stays in storage.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/lmittmann/tint"
	"github.com/quackdiscord/bot/internal/quack"
)

// New creates a logger with colorized development output or structured production
// JSON. A blank level means info; debug must be explicitly enabled by the operator.
func New(out io.Writer, development bool, levelName string) (*slog.Logger, error) {
	level := slog.LevelInfo
	if name := strings.TrimSpace(levelName); name != "" {
		if err := level.UnmarshalText([]byte(name)); err != nil {
			return nil, fmt.Errorf("invalid log level: %w", err)
		}
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewJSONHandler(out, opts)
	if development {
		handler = tint.NewTextHandler(out, &tint.Options{Level: level})
	}
	return slog.New(traceHandler{handler}), nil
}

// traceHandler adds transport-independent trace IDs only when a caller supplied
// them. WithAttrs and WithGroup preserve the wrapper for derived loggers.
type traceHandler struct{ slog.Handler }

// Handle forwards the record with the request and correlation IDs from context.
func (h traceHandler) Handle(ctx context.Context, record slog.Record) error {
	requestID, correlationID := quack.TraceIDsFromContext(ctx)
	if requestID != "" {
		record.AddAttrs(slog.String("request_id", requestID))
	}
	if correlationID != "" {
		record.AddAttrs(slog.String("correlation_id", correlationID))
	}
	return h.Handler.Handle(ctx, record)
}

// WithAttrs preserves trace injection when a component binds its own attributes.
func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}

// WithGroup preserves trace injection when a component groups its attributes.
func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}
