package cli

import (
	"errors"

	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/profile"
)

// ErrUsage marks a user-facing misuse of the CLI: bad flags, missing manifest, unknown command.
var ErrUsage = errors.New("usage error")

// ErrNotImplemented is returned by command stubs that later phases fill in.
var ErrNotImplemented = errors.New("not implemented")

// ExitCode maps an error to the process exit code: 0 success, 1 user error, 2 operation failure.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUsage),
		errors.Is(err, manifest.ErrValidation),
		errors.Is(err, manifest.ErrUnsupportedSchemaVersion),
		errors.Is(err, profile.ErrNotFound):
		return 1
	default:
		return 2
	}
}
