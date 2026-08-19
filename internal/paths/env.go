package paths

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LoadEnvFile parses a .env file at repoDir/.env and applies it to setenv, never overwriting a
// variable already present in lookup. A missing file is not an error.
func LoadEnvFile(repoDir string, lookup Lookup, setenv func(k, v string) error) error {
	f, err := os.Open(filepath.Join(repoDir, ".env"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("opening .env: %w", err)
	}
	defer func() { _ = f.Close() }()

	vars, err := ParseEnv(f)
	if err != nil {
		return fmt.Errorf("reading .env: %w", err)
	}
	for k, v := range vars {
		if _, exists := lookup(k); exists {
			continue
		}
		if err := setenv(k, v); err != nil {
			return fmt.Errorf("setting %s from .env: %w", k, err)
		}
	}
	return nil
}

// LoadEnv loads repoDir/.env into the process environment.
func LoadEnv(repoDir string) error { return LoadEnvFile(repoDir, os.LookupEnv, os.Setenv) }

// ParseEnv reads dotenv lines: blanks and # comments are skipped, the split is on the FIRST =,
// both sides are trimmed, and one layer of matching surrounding quotes is stripped.
func ParseEnv(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if len(val) >= 2 && val[0] == val[len(val)-1] && (val[0] == '\'' || val[0] == '"') {
			val = val[1 : len(val)-1]
		}
		out[key] = val
	}
	return out, sc.Err()
}
