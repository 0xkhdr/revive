package paths

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrUnsetVariable is returned when ${VAR} names an unset variable and no default was given.
// Expanding to "" would write files to /, so this is a hard error by design.
var ErrUnsetVariable = errors.New("environment variable is required but not set")

// interpolationPattern matches ${VAR} and ${VAR:-default}. Verbatim from docs/02 §2.
var interpolationPattern = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)(?::-([^}]+))?\}`)

// Lookup resolves a variable name, reporting whether it is set. os.LookupEnv satisfies it.
type Lookup func(name string) (string, bool)

// Interpolate substitutes ${VAR} and ${VAR:-default} in text using lookup.
func Interpolate(text string, lookup Lookup) (string, error) {
	var firstErr error
	out := interpolationPattern.ReplaceAllStringFunc(text, func(match string) string {
		g := interpolationPattern.FindStringSubmatch(match)
		name, def := g[1], g[2]
		if v, ok := lookup(name); ok {
			return v
		}
		// A ${VAR:-} with an empty default cannot occur: the regex requires one character.
		if strings.Contains(match, ":-") {
			return def
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("interpolating %q: %w: %s", text, ErrUnsetVariable, name)
		}
		return match
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}
