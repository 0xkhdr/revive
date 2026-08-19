//go:build unix

package plugins

import (
	"os/exec"
	"syscall"
	"time"
)

// killGraceDelay bounds how long Wait blocks after the process is killed.
//
// Without it, Run waits for the stdout pipe to close, and a descendant that inherited it keeps
// it open — so a plugin that spawns `sleep 30` would defeat its own timeout entirely.
const killGraceDelay = time.Second

// isolate puts the plugin in its own process group and makes cancellation kill that whole group.
//
// Killing only the direct child leaves its descendants running with rv's file descriptors, which
// is both a leak and a way to outlive the timeout.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative PID addresses the process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = killGraceDelay
}
