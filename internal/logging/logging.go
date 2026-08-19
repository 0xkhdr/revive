// Package logging wires log/slog for the console. The JSON audit handler and the scrubbing
// wrapper arrive with phases 4 and 8.
package logging

import (
	"io"
	"log/slog"
)

// Options controls console log output.
type Options struct {
	Verbose  bool // debug level
	Headless bool // plain stream logs, no decoration
}

// New builds the console logger. Headless output carries the timestamp; interactive output
// drops it, since the console is not an audit trail.
func New(w io.Writer, opts Options) *slog.Logger {
	level := slog.LevelInfo
	if opts.Verbose {
		level = slog.LevelDebug
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if !opts.Headless && len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	return slog.New(h)
}
