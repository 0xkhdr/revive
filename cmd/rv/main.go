// Command rv makes a developer machine's configuration declarative, transactional and reversible.
package main

import (
	"os"

	"github.com/0xkhdr/revive/internal/cli"
)

// version is injected at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		os.Exit(cli.ExitCode(err))
	}
}
