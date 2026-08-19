package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/recovery"
	"github.com/0xkhdr/revive/internal/transaction"
)

// crash leaves a journal mid-flight, the way a killed restore does.
func (h *harness) crash(txID string) (target, original string) {
	h.t.Helper()
	target = filepath.Join(h.work, txID+".conf")
	original = "original\n"
	require.NoError(h.t, os.WriteFile(target, []byte(original), 0o644))

	tx := transaction.New(transaction.Options{TxID: txID, Paths: h.env.Paths, Now: h.env.Now})
	tx.Plan(transaction.Operation{
		Type:   transaction.OpTypeCopy,
		Target: target,
		Source: transaction.SourceBytes{Data: []byte("half-applied\n")},
	})
	require.NoError(h.t, tx.Validate())
	require.NoError(h.t, tx.Snapshot())
	require.NoError(h.t, tx.Execute(h.t.Context()))
	tx.Status = transaction.StatusExecuting
	require.NoError(h.t, transaction.WriteJournal(tx.JournalPath(), tx.Journal()))
	return target, original
}

func TestRecoverWithNothingToDo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	out, err := h.run("recover")
	require.NoError(t, err)
	require.Contains(t, out, "nothing to recover")
}

// Phase 11: rv recover --auto rolls back the newest journal and exits 0.
func TestRecoverAuto(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target, original := h.crash("crashed")

	out, err := h.run("recover", "--auto")
	require.NoError(t, err)
	require.Contains(t, out, "rolled back crashed")

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, original, string(got))
	require.NoFileExists(t, h.env.Paths.JournalPath("crashed"))
}

func TestRecoverInteractiveRollback(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target, original := h.crash("crashed")
	h.env.In = strings.NewReader("y\n")

	out, err := h.run("recover")
	require.NoError(t, err)
	require.Contains(t, out, "crashed")
	require.Contains(t, out, "rolled back")

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, original, string(got))
}

// Answering no discards the journal and leaves the files as they are.
func TestRecoverInteractiveDiscard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target, _ := h.crash("crashed")
	h.env.In = strings.NewReader("n\n")

	out, err := h.run("recover")
	require.NoError(t, err)
	require.Contains(t, out, "discarded crashed")

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "half-applied\n", string(got))
	require.NoFileExists(t, h.env.Paths.JournalPath("crashed"))
}

// --headless has nobody to prompt, so an interactive recover is an error rather than a guess.
func TestRecoverHeadlessRequiresAuto(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.crash("crashed")

	_, err := h.run("recover", "--headless")
	require.ErrorIs(t, err, ErrUsage)

	_, err = h.run("recover", "--auto", "--headless")
	require.NoError(t, err, "--auto is the CI and boot-script path")
}

// Hooks that already ran are reported: rollback restores files but cannot un-run them.
func TestRecoverReportsExecutedHooks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, os.MkdirAll(h.env.Paths.JournalDir, 0o755))
	require.NoError(t, transaction.WriteJournal(h.env.Paths.JournalPath("hooked"), &transaction.Journal{
		TxID: "hooked", Status: transaction.StatusExecuting, Timestamp: 1700000000,
		ExecutedHooks: []transaction.ExecutedHook{
			{AssetID: "nginx", Stage: "pre", Command: []string{"nginx", "-t"}, Result: transaction.HookOK},
		},
	}))

	out, err := h.run("recover", "--auto")
	require.NoError(t, err)
	require.Contains(t, out, "were NOT reversed")
	require.Contains(t, out, "nginx")
}

// Phase 11: a restore refuses to start with an unrecovered journal. [DIVERGE]
func TestRestoreRefusesWithAnUnrecoveredJournal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.crash("crashed")

	target := filepath.Join(h.work, "new.conf")
	h.write("assets/conf", "content\n")
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	_, err := h.run("restore", "base")
	require.ErrorIs(t, err, recovery.ErrIncomplete)
	require.Contains(t, err.Error(), "rv recover")
	require.NoFileExists(t, target,
		"restoring on top would snapshot the broken state as the pre-state")

	// Once recovered, the restore proceeds.
	_, err = h.run("recover", "--auto")
	require.NoError(t, err)
	_, err = h.run("restore", "base")
	require.NoError(t, err)
	require.FileExists(t, target)
}

func TestPruneWithNothingToDo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	out, err := h.run("prune")
	require.NoError(t, err)
	require.Contains(t, out, "nothing to prune")
}

func TestPruneCommand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	old := h.env.Paths.BackupPathFor("old")
	require.NoError(t, os.MkdirAll(old, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(old, "backup_0_x"), make([]byte, 2048), 0o600))
	when := time.Unix(1700000000, 0).AddDate(0, 0, -90)
	require.NoError(t, os.Chtimes(old, when, when))

	out, err := h.run("prune", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "old")
	require.Contains(t, out, "2.0 KiB")
	require.Contains(t, out, "nothing was deleted")
	require.DirExists(t, old)

	out, err = h.run("prune", "--yes")
	require.NoError(t, err)
	require.Contains(t, out, "deleted 1 snapshot")
	require.NoDirExists(t, old)
}

func TestPruneConfirms(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	old := h.env.Paths.BackupPathFor("old")
	require.NoError(t, os.MkdirAll(old, 0o700))
	when := time.Unix(1700000000, 0).AddDate(0, 0, -90)
	require.NoError(t, os.Chtimes(old, when, when))

	h.env.In = strings.NewReader("n\n")
	out, err := h.run("prune")
	require.NoError(t, err)
	require.Contains(t, out, "nothing was deleted")
	require.DirExists(t, old)
}

// The manifest's backup_retention is the default, and an explicit flag overrides it.
func TestPruneUsesManifestRetention(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("manifest.yaml", "version: 2\nbackup_retention: {max_count: 10, max_age_days: 365}\nprofiles: {base: {}}\n")

	old := h.env.Paths.BackupPathFor("old")
	require.NoError(t, os.MkdirAll(old, 0o700))
	when := time.Unix(1700000000, 0).AddDate(0, 0, -90)
	require.NoError(t, os.Chtimes(old, when, when))

	out, err := h.run("prune", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "nothing to prune", "365 days from the manifest keeps a 90-day snapshot")

	out, err = h.run("prune", "--dry-run", "--max-age-days", "30")
	require.NoError(t, err)
	require.Contains(t, out, "old", "an explicit flag overrides the manifest")
}

// A restore prunes automatically after success, per backup_retention.
func TestRestorePrunesAutomatically(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := filepath.Join(h.work, "conf")
	h.write("assets/conf", "content\n")
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nbackup_retention: {max_count: 1, max_age_days: 1}\n"+
			"assets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	stale := h.env.Paths.BackupPathFor("stale")
	require.NoError(t, os.MkdirAll(stale, 0o700))
	when := time.Unix(1700000000, 0).AddDate(0, 0, -90)
	require.NoError(t, os.Chtimes(stale, when, when))

	_, err := h.run("restore", "base")
	require.NoError(t, err)
	require.NoDirExists(t, stale, "pruning runs after a successful restore")
}

func TestBackupCommand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("assets/conf", "old repo content\n")
	target := filepath.Join(h.work, "targets", "conf")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("edited on the machine\n"), 0o644))
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	out, err := h.run("backup", "base")
	require.NoError(t, err)
	require.Contains(t, out, "backed_up")

	got, err := os.ReadFile(filepath.Join(h.work, "assets", "conf"))
	require.NoError(t, err)
	require.Equal(t, "edited on the machine\n", string(got))
}

func TestBackupDryRunCommand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("assets/conf", "old\n")
	target := filepath.Join(h.work, "targets", "conf")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("new\n"), 0o644))
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	out, err := h.run("backup", "base", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "nothing was written")

	got, err := os.ReadFile(filepath.Join(h.work, "assets", "conf"))
	require.NoError(t, err)
	require.Equal(t, "old\n", string(got))
}

func TestBackupWithoutAProfile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("manifest.yaml", "version: 2\nprofiles: {base: {}}\n")
	_, err := h.run("backup")
	require.ErrorIs(t, err, ErrUsage)
}

func TestBackupReportsFailures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("secrets/env.age", "ciphertext")
	target := filepath.Join(h.work, "targets", ".env")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("TOKEN=x\n"), 0o600))
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nsecrets: [{id: env, source: secrets/env.age, target: %s}]\nprofiles: {base: {secrets: [env]}}\n",
		target))

	_, err := h.run("backup", "base")
	require.ErrorIs(t, err, crypto.ErrIdentityRequired)
}

func TestFormatSize(t *testing.T) {
	t.Parallel()
	for bytes, want := range map[int64]string{
		0: "0 B", 512: "512 B", 1024: "1.0 KiB", 1536: "1.5 KiB",
		1048576: "1.0 MiB", 1073741824: "1.0 GiB",
	} {
		require.Equal(t, want, formatSize(bytes), bytes)
	}
}

// Phase 12: a plugin runs during a real restore, and --no-plugins skips it.
func TestRestoreRunsPlugins(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := filepath.Join(h.work, "conf")
	marker := filepath.Join(h.work, "plugin-ran")
	h.write("assets/conf", "content\n")
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	h.write("plugins/notify/plugin.yaml",
		"name: notify\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n")
	script := filepath.Join(h.work, "plugins", "notify", "run.sh")
	require.NoError(t, os.WriteFile(script,
		[]byte("#!/bin/sh\ncat > "+marker+"\necho '{\"status\":\"success\",\"message\":\"done\"}'\n"), 0o755))

	_, err := h.run("restore", "base")
	require.NoError(t, err)
	require.FileExists(t, marker)

	// The context the plugin received names the profile and the planned target.
	raw, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"profile_name":"base"`)
	require.Contains(t, string(raw), target)

	require.NoError(t, os.Remove(marker))
	require.NoError(t, os.Remove(target))
	_, err = h.run("restore", "base", "--no-plugins")
	require.NoError(t, err)
	require.NoFileExists(t, marker)

	require.NoError(t, os.Remove(target))
	_, err = h.run("restore", "base", "--dry-run")
	require.NoError(t, err)
	require.NoFileExists(t, marker, "--dry-run invokes no plugin at any stage")
}

// Phase 12: a failing plugin fails the restore and rolls it back.
func TestFailingPluginRollsBackTheRestore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := filepath.Join(h.work, "conf")
	require.NoError(t, os.WriteFile(target, []byte("original\n"), 0o644))
	h.write("assets/conf", "new content\n")
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s, conflict_strategy: overwrite}]\n"+
			"profiles: {base: {assets: [conf]}}\n", target))

	h.write("plugins/gate/plugin.yaml",
		"name: gate\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n")
	require.NoError(t, os.WriteFile(filepath.Join(h.work, "plugins", "gate", "run.sh"),
		[]byte("#!/bin/sh\necho 'the gate said no' >&2\nexit 1\n"), 0o755))

	_, err := h.run("restore", "base")
	require.Error(t, err)
	require.Contains(t, err.Error(), "rolled back")

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "original\n", string(got))
}

// A workspace plugin shadows a user-global one of the same name.
func TestPluginPrecedenceThroughTheCLI(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := filepath.Join(h.work, "conf")
	marker := filepath.Join(h.work, "which-ran")
	h.write("assets/conf", "content\n")
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	for dir, label := range map[string]string{
		filepath.Join(h.work, "plugins", "notify"):      "workspace",
		filepath.Join(h.env.Paths.PluginsDir, "notify"): "user-global",
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.yaml"),
			[]byte("name: notify\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "run.sh"),
			[]byte("#!/bin/sh\nprintf %s "+label+" > "+marker+"\n"), 0o755))
	}

	_, err := h.run("restore", "base")
	require.NoError(t, err)

	got, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "workspace", string(got), "workspace-local wins over user-global")
}
