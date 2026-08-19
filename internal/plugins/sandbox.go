package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// stderrLimit bounds how much of a failing plugin's stderr reaches the error message.
const stderrLimit = 2000

// Runner executes plugins.
type Runner struct {
	Loader *Loader
	Log    *slog.Logger
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Run executes every plugin subscribed to a stage, in discovery order, and stops at the first
// failure. A plugin failure fails the restore, which rolls it back.
func (r *Runner) Run(ctx context.Context, pluginCtx Context) ([]Result, error) {
	var results []Result
	for _, p := range r.Loader.For(pluginCtx.HookType) {
		result, err := r.RunOne(ctx, p, pluginCtx)
		if err != nil {
			return results, err
		}
		r.log().Info("plugin finished",
			"plugin", p.Name(), "stage", pluginCtx.HookType, "status", result.Status, "message", result.Message)
		results = append(results, result)
	}
	return results, nil
}

// RunOne executes a single plugin.
//
// The context goes in as JSON on stdin rather than as a base64 argv element: no length limit, no
// encoding step, and the plugin reads it with one json.Decode. [DIVERGE]
func (r *Runner) RunOne(ctx context.Context, p Plugin, pluginCtx Context) (Result, error) {
	payload, err := json.Marshal(pluginCtx)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %s: marshalling context: %w", ErrPlugin, p.Name(), err)
	}

	// The timeout is enforced by killing the process, so a hung plugin cannot hang a restore.
	runCtx, cancel := context.WithTimeout(ctx, p.Timeout())
	defer cancel()

	cmd := exec.CommandContext(runCtx, p.Entrypoint)
	cmd.Dir = p.Dir
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = environment(p.Manifest.Permissions)
	isolate(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return Result{}, fmt.Errorf("%w: %s exceeded %s", ErrTimeout, p.Name(), p.Timeout())
	}
	if runErr != nil {
		return Result{}, fmt.Errorf("%w: %s: %w: stderr: %s stdout: %s",
			ErrPlugin, p.Name(), runErr, truncate(stderr.String()), truncate(stdout.String()))
	}

	return parseResult(p, stdout.String()), nil
}

// parseResult applies the result protocol: JSON on stdout is the result, and anything else from a
// plugin that exited 0 is still a success — the plugin did its job even if it printed prose.
func parseResult(p Plugin, stdout string) Result {
	trimmed := strings.TrimSpace(stdout)

	var result Result
	if trimmed != "" && json.Unmarshal([]byte(trimmed), &result) == nil && result.Status != "" {
		result.Plugin = p.Name()
		return result
	}
	return Result{Status: "success", Stdout: trimmed, Plugin: p.Name()}
}

// environment builds the plugin's environment from its declared permissions.
//
// With network disabled, proxy variables point at a dead loopback address. This stops a
// well-behaved HTTP client; it does not stop a raw socket, and the documentation says so.
func environment(perms Permissions) []string {
	env := append(os.Environ(),
		"RV_PLUGIN_SHELL="+boolEnv(perms.Shell),
		"RV_PLUGIN_NETWORK="+boolEnv(perms.Network),
	)
	if len(perms.AllowedPaths) > 0 {
		env = append(env, "RV_PLUGIN_ALLOWED_PATHS="+strings.Join(perms.AllowedPaths, string(os.PathListSeparator)))
	}
	if !perms.Network {
		env = append(env,
			"http_proxy=http://127.0.0.1:0",
			"https_proxy=http://127.0.0.1:0",
			"HTTP_PROXY=http://127.0.0.1:0",
			"HTTPS_PROXY=http://127.0.0.1:0",
			"no_proxy=*",
			"NO_PROXY=*",
		)
	}
	return env
}

func boolEnv(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func truncate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= stderrLimit {
		return s
	}
	return s[:stderrLimit] + "…"
}
