// Package logging wires log/slog for the console. The JSON audit handler and the scrubbing
// wrapper arrive with phases 4 and 8.
package logging

import (
	"io"
	"log/slog"

	"github.com/0xkhdr/revive/internal/scrub"
)

// Options controls console log output.
type Options struct {
	Verbose  bool // debug level
	Headless bool // plain stream logs, no decoration
	// Scrubber redacts every line. Nil means the process-wide scrubber.
	Scrubber *scrub.Scrubber
}

func (o Options) scrubber() *scrub.Scrubber {
	if o.Scrubber != nil {
		return o.Scrubber
	}
	return scrub.Default
}

// scrubbingWriter redacts secrets from a log line before it reaches the underlying writer.
//
// Scrubbing at the writer rather than at the handler is deliberate: a slog handler emits one
// fully serialized record per Write, so this catches the message, every attribute, and anything
// a future handler adds — there is no path around it.
type scrubbingWriter struct {
	w io.Writer
	s *scrub.Scrubber
}

func (sw scrubbingWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(sw.w, sw.s.Scrub(string(p))); err != nil {
		return 0, err
	}
	// Report the caller's length: the scrubbed line is shorter, and a short write is an error.
	return len(p), nil
}

// Scrubbed wraps w so everything written to it passes through the scrubber.
func Scrubbed(w io.Writer, s *scrub.Scrubber) io.Writer { return scrubbingWriter{w: w, s: s} }

// New builds the console logger. Headless output carries the timestamp; interactive output
// drops it, since the console is not an audit trail.
//
// Output is scrubbed unconditionally.
func New(w io.Writer, opts Options) *slog.Logger {
	level := slog.LevelInfo
	if opts.Verbose {
		level = slog.LevelDebug
	}
	h := slog.NewTextHandler(Scrubbed(w, opts.scrubber()), &slog.HandlerOptions{
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
