package cli

import (
	"fmt"
	"os/exec"
	"strings"
	"text/tabwriter"
)

// All rendering lives here, so the business-logic packages return data and never import a
// terminal library.
//
// --headless suppresses every piece of decoration: a CI log is grepped, not read.

// heading prints a section title.
func (e *Env) heading(text string) {
	if e.Headless {
		_, _ = fmt.Fprintln(e.out(), text)
		return
	}
	_, _ = fmt.Fprintf(e.out(), "\n%s\n%s\n", text, strings.Repeat("─", len(text)))
}

// item prints a bullet line.
func (e *Env) item(format string, args ...any) {
	prefix := "  - "
	if e.Headless {
		prefix = "  "
	}
	_, _ = fmt.Fprintf(e.out(), prefix+format+"\n", args...)
}

// line prints a plain line.
func (e *Env) line(format string, args ...any) {
	_, _ = fmt.Fprintf(e.out(), format+"\n", args...)
}

// table renders aligned columns. Under --headless the columns are tab-separated, which is what a
// script wants.
func (e *Env) table(headers []string, rows [][]string) {
	if e.Headless {
		_, _ = fmt.Fprintln(e.out(), strings.Join(headers, "\t"))
		for _, row := range rows {
			_, _ = fmt.Fprintln(e.out(), strings.Join(row, "\t"))
		}
		return
	}
	w := tabwriter.NewWriter(e.out(), 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, strings.Join(headers, "\t"))
	_, _ = fmt.Fprintln(w, strings.Join(underlines(headers), "\t"))
	for _, row := range rows {
		_, _ = fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	_ = w.Flush()
}

func underlines(headers []string) []string {
	out := make([]string, len(headers))
	for i, h := range headers {
		out[i] = strings.Repeat("─", len(h))
	}
	return out
}

// execGit runs git in a directory. It is the default for Env.Git.
func execGit(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return out, nil
}
