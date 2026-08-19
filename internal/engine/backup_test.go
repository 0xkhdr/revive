package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/crypto"
)

func actionFor(res *BackupResult, assetID string) BackupItem {
	for _, item := range res.Items {
		if item.AssetID == assetID {
			return item
		}
	}
	return BackupItem{}
}

func (w *workspace) backupOpts(profiles ...string) BackupOptions {
	return BackupOptions{
		RepoDir:      w.repo,
		ManifestPath: filepath.Join(w.repo, "manifest.yaml"),
		Profiles:     profiles,
	}
}

// Phase 11: rv backup copies a modified system file into the repository.
func TestBackupCopiesAModifiedFile(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "old repo content\n")
	target := filepath.Join(w.home, "conf")
	require.NoError(t, os.WriteFile(target, []byte("edited on the machine\n"), 0o644))
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	res, err := w.restorer().Backup(context.Background(), w.backupOpts("base"))
	require.NoError(t, err)
	require.Equal(t, BackedUp, actionFor(res, "conf").Action)

	got, err := os.ReadFile(filepath.Join(w.repo, "assets", "conf"))
	require.NoError(t, err)
	require.Equal(t, "edited on the machine\n", string(got))
}

// Phase 11: rv backup re-encrypts secrets with the public key derived from the identity.
func TestBackupReEncryptsSecrets(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t).withIdentity()
	w.encryptRepo("secrets/env.age", "TOKEN=old\n")
	target := filepath.Join(w.home, ".env")
	require.NoError(t, os.WriteFile(target, []byte("TOKEN=rotated-on-the-machine\n"), 0o600))
	w.manifest(fmt.Sprintf(
		"version: 2\nsecrets: [{id: env, source: secrets/env.age, target: %s}]\nprofiles: {base: {secrets: [env]}}\n",
		target))

	res, err := w.restorer().Backup(context.Background(), w.backupOpts("base"))
	require.NoError(t, err)
	require.Equal(t, BackedUp, actionFor(res, "env").Action)

	ciphertext, err := os.ReadFile(filepath.Join(w.repo, "secrets", "env.age"))
	require.NoError(t, err)
	require.NotContains(t, string(ciphertext), "rotated-on-the-machine",
		"the repository must never hold plaintext")

	plaintext, err := crypto.Decrypt(ciphertext, w.identity)
	require.NoError(t, err)
	require.Equal(t, "TOKEN=rotated-on-the-machine\n", string(plaintext))
}

// Phase 11: templates are skipped with a warning — a rendered file cannot be un-rendered.
func TestBackupSkipsTemplates(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	source := w.writeRepo("assets/t.tmpl", "email = {{ .email }}\n")
	target := filepath.Join(w.home, ".gitconfig")
	require.NoError(t, os.WriteFile(target, []byte("email = dev@example.com\n"), 0o644))
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: t, type: template, source: assets/t.tmpl, target: %s}]\nprofiles: {base: {assets: [t]}}\n",
		target))

	res, err := w.restorer().Backup(context.Background(), w.backupOpts("base"))
	require.NoError(t, err)
	require.Equal(t, SkippedItem, actionFor(res, "t").Action)
	require.Contains(t, actionFor(res, "t").Reason, "template")

	got, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, "email = {{ .email }}\n", string(got), "the template itself must survive")
}

// Phase 11: a symlink already pointing at the repository is reported as in sync.
func TestBackupReportsSymlinksIntoTheRepoAsInSync(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	source := w.writeRepo("assets/zshrc", "export EDITOR=vim\n")
	target := filepath.Join(w.home, ".zshrc")
	require.NoError(t, os.Symlink(source, target))
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: zshrc, type: symlink, source: assets/zshrc, target: %s}]\nprofiles: {base: {assets: [zshrc]}}\n",
		target))

	res, err := w.restorer().Backup(context.Background(), w.backupOpts("base"))
	require.NoError(t, err)
	require.Equal(t, AlreadyInSync, actionFor(res, "zshrc").Action)
	require.Contains(t, actionFor(res, "zshrc").Reason, "symlink into the repository")
}

// A symlink pointing outside the repository is followed and its real contents copied.
func TestBackupFollowsSymlinksOutsideTheRepo(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "old\n")
	real := filepath.Join(w.home, "real.conf")
	require.NoError(t, os.WriteFile(real, []byte("the real contents\n"), 0o644))
	target := filepath.Join(w.home, "conf")
	require.NoError(t, os.Symlink(real, target))

	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	res, err := w.restorer().Backup(context.Background(), w.backupOpts("base"))
	require.NoError(t, err)
	require.Equal(t, BackedUp, actionFor(res, "conf").Action)

	got, err := os.ReadFile(filepath.Join(w.repo, "assets", "conf"))
	require.NoError(t, err)
	require.Equal(t, "the real contents\n", string(got))
}

// A multi-target asset writes back to <source>/<basename of target>.
func TestBackupMultiTargetWritesPerBasename(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/app/one", "old one\n")
	w.writeRepo("assets/app/two", "old two\n")
	one := filepath.Join(w.home, "one")
	two := filepath.Join(w.home, "two")
	require.NoError(t, os.WriteFile(one, []byte("new one\n"), 0o644))
	require.NoError(t, os.WriteFile(two, []byte("new two\n"), 0o644))

	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: app, type: copy, source: assets/app, target: [%s, %s]}]\nprofiles: {base: {assets: [app]}}\n",
		one, two))

	res, err := w.restorer().Backup(context.Background(), w.backupOpts("base"))
	require.NoError(t, err)
	require.Len(t, res.Items, 2)

	for name, want := range map[string]string{"one": "new one\n", "two": "new two\n"} {
		got, err := os.ReadFile(filepath.Join(w.repo, "assets", "app", name))
		require.NoError(t, err)
		require.Equal(t, want, string(got))
	}
}

func TestBackupMultiTargetSecretAppendsAge(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t).withIdentity()
	w.encryptRepo("secrets/app_env/.env.age", "OLD=1\n")
	one := filepath.Join(w.home, ".env")
	two := filepath.Join(w.home, ".env.deploy")
	require.NoError(t, os.WriteFile(one, []byte("NEW=1\n"), 0o600))
	require.NoError(t, os.WriteFile(two, []byte("NEW=2\n"), 0o600))

	w.manifest(fmt.Sprintf(
		"version: 2\nsecrets: [{id: env, source: secrets/app_env, target: [%s, %s]}]\nprofiles: {base: {secrets: [env]}}\n",
		one, two))

	res, err := w.restorer().Backup(context.Background(), w.backupOpts("base"))
	require.NoError(t, err)
	require.Len(t, res.Items, 2)

	for name, want := range map[string]string{".env.age": "NEW=1\n", ".env.deploy.age": "NEW=2\n"} {
		ciphertext, err := os.ReadFile(filepath.Join(w.repo, "secrets", "app_env", name))
		require.NoError(t, err)
		plaintext, err := crypto.Decrypt(ciphertext, w.identity)
		require.NoError(t, err)
		require.Equal(t, want, string(plaintext))
	}
}

// A target that does not exist is a warning and a skip, not a failure.
func TestBackupSkipsMissingTargets(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "content\n")
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		filepath.Join(w.home, "absent")))

	res, err := w.restorer().Backup(context.Background(), w.backupOpts("base"))
	require.NoError(t, err)
	require.Equal(t, SkippedItem, actionFor(res, "conf").Action)
	require.Contains(t, actionFor(res, "conf").Reason, "does not exist")
}

// Phase 11: --dry-run reports the planned items without writing.
func TestBackupDryRun(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "old repo content\n")
	target := filepath.Join(w.home, "conf")
	require.NoError(t, os.WriteFile(target, []byte("edited\n"), 0o644))
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	opts := w.backupOpts("base")
	opts.DryRun = true
	res, err := w.restorer().Backup(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, res.DryRun)
	require.Equal(t, BackedUp, actionFor(res, "conf").Action)
	require.NotEmpty(t, actionFor(res, "conf").Destination)

	got, err := os.ReadFile(filepath.Join(w.repo, "assets", "conf"))
	require.NoError(t, err)
	require.Equal(t, "old repo content\n", string(got), "a dry run writes nothing")
}

// Machine overrides are merged before backup, so machine-specific target paths resolve.
func TestBackupAppliesMachineOverrides(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "old\n")
	hostTarget := filepath.Join(w.home, "host-specific.conf")
	require.NoError(t, os.WriteFile(hostTarget, []byte("host content\n"), 0o644))

	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		filepath.Join(w.home, "default.conf")))
	w.writeRepo("machine/test-host.yaml", fmt.Sprintf(
		"assets:\n  - {id: conf, type: copy, source: assets/conf, target: %s}\n", hostTarget))

	res, err := w.restorer().Backup(context.Background(), w.backupOpts("base"))
	require.NoError(t, err)
	require.Equal(t, BackedUp, actionFor(res, "conf").Action)

	got, err := os.ReadFile(filepath.Join(w.repo, "assets", "conf"))
	require.NoError(t, err)
	require.Equal(t, "host content\n", string(got))
}

func TestBackupErrors(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.manifest("version: 3\n")
	_, err := w.restorer().Backup(context.Background(), w.backupOpts("base"))
	require.Error(t, err)

	w2 := newWorkspace(t)
	w2.manifest("version: 2\nprofiles: {base: {}}\n")
	_, err = w2.restorer().Backup(context.Background(), w2.backupOpts("nope"))
	require.Error(t, err)
}

// A secret with no identity cannot be re-encrypted, and that must not be silent.
func TestBackupSecretWithoutAnIdentityFails(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("secrets/env.age", "ciphertext")
	target := filepath.Join(w.home, ".env")
	require.NoError(t, os.WriteFile(target, []byte("TOKEN=x\n"), 0o600))
	w.manifest(fmt.Sprintf(
		"version: 2\nsecrets: [{id: env, source: secrets/env.age, target: %s}]\nprofiles: {base: {secrets: [env]}}\n",
		target))

	_, err := w.restorer().Backup(context.Background(), w.backupOpts("base"))
	require.ErrorIs(t, err, ErrIdentityRequired)
}

func TestWithinRepo(t *testing.T) {
	t.Parallel()
	require.True(t, withinRepo("/repo", "/repo/assets/zshrc"))
	require.True(t, withinRepo("/repo", "/repo"))
	require.False(t, withinRepo("/repo", "/etc/passwd"))
	require.False(t, withinRepo("/repo", "/repo-other/x"))
}
