package doctor

import (
	"fmt"
	"regexp"
	"strings"
)

// Jinja2 detection. This check is what makes the templating change survivable.
//
// `text/template` does not fail on Jinja2 `{% … %}` tags — it passes them through as literal
// text, straight into the user's config file. Only a linter catches that, which is why it is
// critical severity and why it reports the line and the replacement rather than just a warning.

var (
	// jinjaStatement matches `{% ... %}`, which text/template treats as plain text.
	jinjaStatement = regexp.MustCompile(`\{%-?\s*(\w+)([^%]*)-?%\}`)
	// jinjaAction matches the inside of a `{{ ... }}` action.
	jinjaAction = regexp.MustCompile(`\{\{-?([^}]*)-?\}\}`)
	// bareIdentifier matches a leading variable reference with no dot and no function call.
	bareIdentifier = regexp.MustCompile(`^\s*([A-Za-z_]\w*)\s*$`)
)

// registeredFuncs are the names a bare `{{ name }}` may legitimately be, matching the func map
// in internal/engine.
var registeredFuncs = map[string]bool{
	"upper": true, "lower": true, "trim": true, "replace": true,
	"join": true, "default": true, "env": true,
	// text/template built-ins that legitimately appear bare.
	"if": true, "else": true, "end": true, "range": true, "with": true, "template": true,
	"block": true, "define": true, "print": true, "printf": true, "println": true,
	"len": true, "index": true, "slice": true, "and": true, "or": true, "not": true,
	"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true,
	"html": true, "js": true, "urlquery": true, "call": true, "break": true, "continue": true,
}

// jinjaEquivalents maps a Jinja2 statement keyword to its text/template form.
var jinjaEquivalents = map[string]string{
	"if":      "{{ if .x }} … {{ end }}",
	"elif":    "{{ else if .x }}",
	"else":    "{{ else }}",
	"endif":   "{{ end }}",
	"for":     "{{ range .xs }} … {{ end }}",
	"endfor":  "{{ end }}",
	"set":     "{{ $name := .value }}",
	"macro":   "a named template: {{ define \"name\" }} … {{ end }}",
	"include": "{{ template \"name\" . }}",
	"block":   "{{ block \"name\" . }} … {{ end }}",
	"raw":     "no equivalent; escape the braces instead",
}

// templateFinding is one problem in one template source.
type templateFinding struct {
	Line    int
	Snippet string
	Advice  string
}

// scanTemplate finds Jinja2 syntax that text/template will not execute.
func scanTemplate(content string) []templateFinding {
	var findings []templateFinding

	for i, line := range strings.Split(content, "\n") {
		lineNo := i + 1

		for _, m := range jinjaStatement.FindAllStringSubmatch(line, -1) {
			advice, ok := jinjaEquivalents[strings.ToLower(m[1])]
			if !ok {
				advice = "text/template has no {% … %} form; rewrite as a {{ … }} action"
			}
			findings = append(findings, templateFinding{
				Line:    lineNo,
				Snippet: strings.TrimSpace(m[0]),
				Advice: fmt.Sprintf("Jinja2 statement tags are passed through as literal text "+
					"and land in the output file. Use: %s", advice),
			})
		}

		for _, m := range jinjaAction.FindAllStringSubmatch(line, -1) {
			inner := strings.TrimSpace(m[1])
			if inner == "" {
				continue
			}

			// A Jinja2 filter pipe: `{{ x | upper }}` versus text/template's `{{ upper .x }}`.
			// A `|` between two actions is valid text/template piping, so only flag it when the
			// left side has no leading dot and is not a call.
			if before, after, found := strings.Cut(inner, "|"); found {
				left := strings.TrimSpace(before)
				if bareIdentifier.MatchString(left) && !registeredFuncs[left] {
					findings = append(findings, templateFinding{
						Line:    lineNo,
						Snippet: strings.TrimSpace(m[0]),
						Advice: fmt.Sprintf("Jinja2 filter syntax. Use: {{ %s .%s }}",
							strings.TrimSpace(strings.Split(after, "(")[0]), left),
					})
					continue
				}
			}

			if name := bareIdentifier.FindStringSubmatch(inner); name != nil && !registeredFuncs[name[1]] {
				findings = append(findings, templateFinding{
					Line:    lineNo,
					Snippet: strings.TrimSpace(m[0]),
					Advice:  fmt.Sprintf("the context is a map, so every variable needs a leading dot. Use: {{ .%s }}", name[1]),
				})
			}
		}
	}
	return findings
}
