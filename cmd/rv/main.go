// Command rv makes a developer machine's configuration declarative, transactional and reversible.
package main

import (
	"os"

	"github.com/0xkhdr/revive/internal/cli"
)

// Build metadata, injected with -ldflags at release time.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	if err := cli.Execute(fullVersion()); err != nil {
		os.Exit(cli.ExitCode(err))
	}
}

// fullVersion renders what `rv --version` prints. A bug report is much easier to act on when it
// names the commit the binary was built from.
func fullVersion() string {
	out := version
	if commit != "" {
		out += " (" + commit
		if date != "" {
			out += ", " + date
		}
		out += ")"
	}
	return out
}
