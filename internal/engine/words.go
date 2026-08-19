package engine

import (
	"errors"
	"fmt"
	"strings"
)

// ErrBadHookSyntax is returned when a hook command cannot be word-split, typically an unbalanced
// quote. It is caught at plan time, before anything is snapshotted.
var ErrBadHookSyntax = errors.New("invalid hook command syntax")

// SplitWords splits a hook command the way a POSIX shell would. Exported so doctor validates a
// hook exactly as planning does.
func SplitWords(s string) ([]string, error) { return splitWords(s) }

// splitWords splits a command the way a POSIX shell would, and the result is then executed
// WITHOUT a shell. Handing the raw string to `sh -c` instead would reintroduce every injection
// this splitting exists to avoid.
//
// Written rather than imported: the rules that matter here are single quotes (fully literal),
// double quotes (backslash escapes a quote, a backslash or a dollar), and a bare backslash. An
// unterminated quote is an error, never a silently truncated command.
func splitWords(s string) ([]string, error) {
	var (
		words   []string
		current strings.Builder
		started bool
	)
	const (
		bare = iota
		inSingle
		inDouble
	)
	state := bare

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch state {
		case bare:
			switch c {
			case ' ', '\t', '\n', '\r':
				if started {
					words = append(words, current.String())
					current.Reset()
					started = false
				}
			case '\'':
				state, started = inSingle, true
			case '"':
				state, started = inDouble, true
			case '\\':
				if i+1 >= len(s) {
					return nil, fmt.Errorf("%w: trailing backslash in %q", ErrBadHookSyntax, s)
				}
				i++
				current.WriteByte(s[i])
				started = true
			default:
				current.WriteByte(c)
				started = true
			}
		case inSingle:
			if c == '\'' {
				state = bare
				continue
			}
			current.WriteByte(c)
		case inDouble:
			switch {
			case c == '"':
				state = bare
			case c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\' || s[i+1] == '$' || s[i+1] == '`'):
				i++
				current.WriteByte(s[i])
			default:
				current.WriteByte(c)
			}
		}
	}

	if state != bare {
		return nil, fmt.Errorf("%w: unbalanced quote in %q", ErrBadHookSyntax, s)
	}
	if started {
		words = append(words, current.String())
	}
	if len(words) == 0 {
		return nil, fmt.Errorf("%w: empty command", ErrBadHookSyntax)
	}
	return words, nil
}
