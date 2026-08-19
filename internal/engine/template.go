package engine

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"
)

// ErrTemplate covers a template that will not parse or will not render.
var ErrTemplate = errors.New("template error")

// funcMap is the fixed set of helpers available in a template.
//
// It is deliberately small. A template language is not a scripting language, and every function
// added here becomes part of the manifest's public contract — additions are one-way.
var funcMap = template.FuncMap{
	"upper":   strings.ToUpper,
	"lower":   strings.ToLower,
	"trim":    strings.TrimSpace,
	"replace": func(old, new, s string) string { return strings.ReplaceAll(s, old, new) },
	"join":    func(sep string, xs []string) string { return strings.Join(xs, sep) },
	"default": func(fallback, v any) any {
		if v == nil || v == "" {
			return fallback
		}
		return v
	},
	"env": os.Getenv,
}

// Render renders a template source with the merged context.
//
// missingkey=error is mandatory: an unset variable is a hard error, never an empty string. The
// alternative writes a broken config file that fails much later and much more confusingly.
func Render(assetID string, source []byte, ctx map[string]any) ([]byte, error) {
	tmpl, err := template.New(assetID).Funcs(funcMap).Option("missingkey=error").Parse(string(source))
	if err != nil {
		return nil, fmt.Errorf("%w: parsing template for asset %q: %w", ErrTemplate, assetID, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return nil, fmt.Errorf("%w: rendering template for asset %q: %w", ErrTemplate, assetID, err)
	}
	return buf.Bytes(), nil
}
