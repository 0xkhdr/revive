package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/0xkhdr/revive/internal/scrub"
)

// NewAudit opens the append-only JSON audit log and returns a logger for it, plus the closer.
//
// The audit logger does not propagate to the console: the console has its own handler, and
// wiring both to one logger prints everything twice. Output is scrubbed after marshalling, so a
// secret in any field — not just the message — is caught.
func NewAudit(path string, s *scrub.Scrubber) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("creating audit log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("opening audit log: %w", err)
	}

	h := slog.NewJSONHandler(Scrubbed(f, s), &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			// Match the field names docs/05 §2 documents.
			switch a.Key {
			case slog.TimeKey:
				a.Key = "timestamp"
			case slog.MessageKey:
				a.Key = "message"
			case slog.LevelKey:
				a.Value = slog.StringValue(strings.ToUpper(a.Value.String()))
			}
			return a
		},
	})
	return slog.New(h), f, nil
}

// Fanout sends every record to several handlers. It is how one logger reaches both the console
// and the audit file without either being able to skip the scrubber.
type Fanout struct{ handlers []slog.Handler }

// NewFanout builds a logger writing to every given logger's handler.
func NewFanout(loggers ...*slog.Logger) *slog.Logger {
	f := &Fanout{}
	for _, l := range loggers {
		if l != nil {
			f.handlers = append(f.handlers, l.Handler())
		}
	}
	return slog.New(f)
}

// Enabled reports whether any handler wants this level.
func (f *Fanout) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle passes the record to every handler that wants it.
func (f *Fanout) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, h := range f.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// WithAttrs returns a Fanout whose handlers all carry the attributes.
func (f *Fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := &Fanout{handlers: make([]slog.Handler, len(f.handlers))}
	for i, h := range f.handlers {
		out.handlers[i] = h.WithAttrs(attrs)
	}
	return out
}

// WithGroup returns a Fanout whose handlers are all grouped.
func (f *Fanout) WithGroup(name string) slog.Handler {
	out := &Fanout{handlers: make([]slog.Handler, len(f.handlers))}
	for i, h := range f.handlers {
		out.handlers[i] = h.WithGroup(name)
	}
	return out
}
