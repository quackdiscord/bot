package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/lmittmann/tint"
	"github.com/quackdiscord/bot/internal/quack"
)

// NewLogger returns the process logger: colored text in development, JSON
// otherwise, at levelName (blank means info). Records carry the request and
// correlation IDs from their context. Logs hold operational metadata only;
// case content stays in storage.
func NewLogger(out io.Writer, development bool, levelName string) (*slog.Logger, error) {
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

// traceHandler adds the context's trace IDs to each record. WithAttrs and
// WithGroup rewrap, so derived loggers keep doing it.
type traceHandler struct{ slog.Handler }

// Handle adds the trace IDs, when ctx has them, and forwards the record.
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

// WithAttrs returns a traceHandler around the inner handler's WithAttrs.
func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}

// WithGroup returns a traceHandler around the inner handler's WithGroup.
func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}
