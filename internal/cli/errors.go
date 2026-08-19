package cli

import (
	"errors"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/doctor"
	"github.com/0xkhdr/revive/internal/engine"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/profile"
	"github.com/0xkhdr/revive/internal/workspace"
)

// Sentinel errors owned by the CLI layer.
var (
	// ErrUsage marks a user-facing misuse: bad flags, a missing profile, an existing workspace.
	ErrUsage = errors.New("usage error")
	// ErrOperation marks a failed operation, as opposed to a bad request.
	ErrOperation = errors.New("operation failed")
)

// ExitCode maps an error to the process exit code.
//
//	0  success
//	1  user or configuration error — bad flags, missing manifest, unknown profile, invalid
//	   manifest, missing identity, unhealthy doctor
//	2  operation failure — a restore that failed and rolled back
//
// Every case matches a sentinel with errors.Is. Error message text never decides an exit code.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUsage),
		errors.Is(err, manifest.ErrValidation),
		errors.Is(err, manifest.ErrUnsupportedSchemaVersion),
		errors.Is(err, manifest.ErrNotFound),
		errors.Is(err, profile.ErrNotFound),
		errors.Is(err, profile.ErrCycle),
		errors.Is(err, profile.ErrOverride),
		errors.Is(err, paths.ErrUnsetVariable),
		errors.Is(err, crypto.ErrIdentityRequired),
		errors.Is(err, engine.ErrTargetConflict),
		errors.Is(err, engine.ErrSourceNotFound),
		errors.Is(err, workspace.ErrNotFound),
		errors.Is(err, workspace.ErrDuplicate),
		// An unhealthy doctor is a configuration problem, and exit 1 is what makes it usable
		// as a CI gate rather than being confused with a crash.
		errors.Is(err, doctor.ErrUnhealthy):
		return 1
	default:
		return 2
	}
}
