//go:build unix

package transaction

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/sys/unix"
)

// ExecHook runs a hook's argv directly — never through a shell. Word splitting happened at plan
// time; handing the string to `sh -c` here would reintroduce every injection the splitting
// avoided.
func ExecHook(ctx context.Context, hook HookOp, target, txID string) error {
	cmd := exec.CommandContext(ctx, hook.Command[0], hook.Command[1:]...)
	cmd.Env = append(os.Environ(),
		"RV_ASSET_ID="+hook.AssetID,
		"RV_ASSET_TARGET="+target,
		"RV_TX_ID="+txID,
		"RV_HOOK_STAGE="+hook.Stage,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", hook.Command[0], err, out)
	}
	return nil
}

// lookPath resolves a hook's argv[0] so a typo fails during validation rather than halfway
// through the mutations.
func lookPath(name string) (string, error) { return exec.LookPath(name) }

// unixAccessWritable reports whether the current process can write to path. os.Access is the
// honest check: a mode alone does not account for ownership or ACLs.
func unixAccessWritable(path string) bool {
	return unix.Access(path, unix.W_OK) == nil
}
