package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/workspace"
)

// Phase 9: rv init scaffolds a workspace.
func TestInitScaffolds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	out, err := h.run("init")
	require.NoError(t, err)
	require.Contains(t, out, "Workspace created")

	for _, dir := range []string{"assets", "secrets", "machine", ".agents/skills/rv"} {
		require.DirExists(t, filepath.Join(h.work, dir), dir)
	}
	require.FileExists(t, filepath.Join(h.work, ".agents", "skills", "rv", "SKILL.md"))

	// The scaffolded manifest must actually load and validate.
	m, err := manifest.Load(filepath.Join(h.work, "manifest.yaml"))
	require.NoError(t, err)
	require.Equal(t, 2, m.SchemaVersion())
	require.Contains(t, m.Profiles, "base")
	require.Len(t, m.Assets, 1)
}

// Phase 9: rv init refuses to run over an existing workspace.
func TestInitRefusesOverAnExistingWorkspace(t *testing.T) {
	t.Parallel()
	for _, name := range manifestNames {
		h := newHarness(t)
		h.write(name, "version: 2\n")

		_, err := h.run("init")
		require.ErrorIs(t, err, ErrUsage, name)
		require.Equal(t, 1, ExitCode(err))

		// The existing declaration must be untouched.
		raw, readErr := os.ReadFile(filepath.Join(h.work, name))
		require.NoError(t, readErr)
		require.Equal(t, "version: 2\n", string(raw))
	}
}

// Phase 9: rv clone clones, registers a workspace, and optionally restores.
func TestClone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	out, err := h.run("clone", "https://example.com/dotfiles.git")
	require.NoError(t, err)

	dest := filepath.Join(h.work, "dotfiles")
	require.Contains(t, h.git[0], "clone https://example.com/dotfiles.git "+dest)
	require.Contains(t, out, "registered workspace dotfiles")

	cfg, err := workspace.Load(h.env.Paths.WorkspaceFile)
	require.NoError(t, err)
	ws, ok := cfg.Find("dotfiles")
	require.True(t, ok)
	require.Equal(t, dest, ws.Path)
	require.Equal(t, "dotfiles", cfg.DefaultWorkspace)
}

func TestCloneWithAnExplicitDestination(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, err := h.run("clone", "git@github.com:someone/config.git", "myconfig")
	require.NoError(t, err)
	require.DirExists(t, filepath.Join(h.work, "myconfig"))

	cfg, err := workspace.Load(h.env.Paths.WorkspaceFile)
	require.NoError(t, err)
	_, ok := cfg.Find("myconfig")
	require.True(t, ok)
}

func TestCloneThenRestore(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dest := filepath.Join(h.work, "dotfiles")
	target := filepath.Join(h.work, "restored.conf")

	// The fake git populates the clone the way a real one would.
	h.env.Git = func(dir string, args ...string) ([]byte, error) {
		require.Equal(t, "clone", args[0])
		require.NoError(t, os.MkdirAll(filepath.Join(dest, "assets"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dest, "assets", "conf"), []byte("cloned\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dest, "manifest.yaml"), []byte(fmt.Sprintf(
			"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
			target)), 0o644))
		return nil, nil
	}

	_, err := h.run("clone", "https://example.com/dotfiles.git", "--restore", "base")
	require.NoError(t, err)

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "cloned\n", string(got), "the restore must run inside the clone")
}

func TestCloneFailureSurfaces(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.gitErr["clone https://example.com/x.git "+filepath.Join(h.work, "x")] = errors.New("repository not found")

	_, err := h.run("clone", "https://example.com/x.git")
	require.Error(t, err)
	require.Contains(t, err.Error(), "repository not found")
}

func TestDefaultCloneDestination(t *testing.T) {
	t.Parallel()
	for url, want := range map[string]string{
		"https://example.com/dotfiles.git": "dotfiles",
		"https://example.com/dotfiles":     "dotfiles",
		"https://example.com/dotfiles/":    "dotfiles",
		"git@github.com:someone/cfg.git":   "cfg",
		"/local/path/repo":                 "repo",
		"":                                 "workspace",
	} {
		require.Equal(t, want, defaultCloneDest(url), url)
	}
}

func TestWorkspaceListAddRemove(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	out, err := h.run("workspace", "list")
	require.NoError(t, err)
	require.Contains(t, out, "no workspaces registered")

	repo := filepath.Join(h.work, "dotfiles")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	_, err = h.run("workspace", "add", repo, "--name", "dots")
	require.NoError(t, err)

	out, err = h.run("workspace", "list")
	require.NoError(t, err)
	require.Contains(t, out, "dots")
	require.Contains(t, out, repo)

	_, err = h.run("workspace", "remove", "dots")
	require.NoError(t, err)
	require.DirExists(t, repo, "unregistering must never delete the directory")

	_, err = h.run("workspace", "remove", "dots")
	require.ErrorIs(t, err, workspace.ErrNotFound)
	require.Equal(t, 1, ExitCode(err))
}

// Phase 9: workspace sync continues past a failing workspace and exits 1 if any failed.
func TestWorkspaceSyncContinuesPastFailures(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	var repos []string
	for _, name := range []string{"first", "broken", "last"} {
		repo := filepath.Join(h.work, name)
		require.NoError(t, os.MkdirAll(filepath.Join(repo, "assets"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(repo, "assets", "conf"), []byte(name+"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(repo, "manifest.yaml"), []byte(fmt.Sprintf(
			"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
			filepath.Join(h.work, name+".out"))), 0o644))
		repos = append(repos, repo)

		_, err := h.run("workspace", "add", repo)
		require.NoError(t, err)
	}

	h.gitErr["pull"] = nil
	h.env.Git = func(dir string, args ...string) ([]byte, error) {
		if strings.HasSuffix(dir, "broken") {
			return nil, errors.New("fatal: could not read from remote repository")
		}
		return nil, nil
	}

	out, err := h.run("workspace", "sync", "-p", "base")
	require.ErrorIs(t, err, ErrOperation)
	require.Equal(t, 2, ExitCode(err))

	require.Contains(t, out, "failed")
	require.Contains(t, out, "first")
	require.Contains(t, out, "last")

	// The workspaces either side of the failure were still restored.
	require.FileExists(t, filepath.Join(h.work, "first.out"))
	require.FileExists(t, filepath.Join(h.work, "last.out"))
	require.NoFileExists(t, filepath.Join(h.work, "broken.out"))
	require.Len(t, repos, 3)
}

func TestWorkspaceSyncWithNothingRegistered(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	out, err := h.run("workspace", "sync", "-p", "base")
	require.NoError(t, err)
	require.Contains(t, out, "no workspaces registered")
}

func TestWorkspaceSyncWithoutAProfilePullsOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	repo := filepath.Join(h.work, "dotfiles")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	_, err := h.run("workspace", "add", repo)
	require.NoError(t, err)

	out, err := h.run("workspace", "sync")
	require.NoError(t, err)
	require.Contains(t, out, "no profile given")
}

func TestSecretKeygen(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	out, err := h.run("secret", "keygen")
	require.NoError(t, err)
	require.Contains(t, out, "public key:")
	require.Contains(t, out, "WARNING", "an unsaved private key has to be called out")

	path := filepath.Join(h.work, "identity.txt")
	out, err = h.run("secret", "keygen", "--output", path)
	require.NoError(t, err)
	require.Contains(t, out, "0600")

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	pub, err := crypto.PublicKeyFromIdentity(path)
	require.NoError(t, err)
	require.Contains(t, out, pub)
}

func TestSecretEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	identity := filepath.Join(h.work, "identity.txt")
	_, err := h.run("secret", "keygen", "--output", identity)
	require.NoError(t, err)
	pub, err := crypto.PublicKeyFromIdentity(identity)
	require.NoError(t, err)

	plain := h.write("plain.env", "TOKEN=sk-live-1\n")
	encrypted := filepath.Join(h.work, "plain.env.age")
	_, err = h.run("secret", "encrypt", plain, "-o", encrypted, "-r", pub)
	require.NoError(t, err)

	raw, err := os.ReadFile(encrypted)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "sk-live-1")

	decrypted := filepath.Join(h.work, "out.env")
	_, err = h.run("secret", "decrypt", encrypted, "-o", decrypted, "-i", identity)
	require.NoError(t, err)

	got, err := os.ReadFile(decrypted)
	require.NoError(t, err)
	require.Equal(t, "TOKEN=sk-live-1\n", string(got))

	fi, err := os.Stat(decrypted)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(),
		"decrypted content never lands with a permissive mode")
}

func TestSecretRotate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	oldIdentity := filepath.Join(h.work, "old.txt")
	newIdentity := filepath.Join(h.work, "new.txt")
	_, err := h.run("secret", "keygen", "--output", oldIdentity)
	require.NoError(t, err)
	_, err = h.run("secret", "keygen", "--output", newIdentity)
	require.NoError(t, err)

	oldPub, err := crypto.PublicKeyFromIdentity(oldIdentity)
	require.NoError(t, err)
	newPub, err := crypto.PublicKeyFromIdentity(newIdentity)
	require.NoError(t, err)

	plain := h.write("plain.env", "TOKEN=rotate-me\n")
	encrypted := filepath.Join(h.work, "secret.age")
	_, err = h.run("secret", "encrypt", plain, "-o", encrypted, "-r", oldPub)
	require.NoError(t, err)

	_, err = h.run("secret", "rotate", encrypted, "-i", oldIdentity, "--new-recipient", newPub)
	require.NoError(t, err)

	// The new identity can read it and the old one cannot.
	ciphertext, err := os.ReadFile(encrypted)
	require.NoError(t, err)
	got, err := crypto.Decrypt(ciphertext, newIdentity)
	require.NoError(t, err)
	require.Equal(t, "TOKEN=rotate-me\n", string(got))
	_, err = crypto.Decrypt(ciphertext, oldIdentity)
	require.Error(t, err, "the old key must lose access, which is the point of a rotation")
}

// --from-plaintext destroys its source, so it may not be reachable by accident.
func TestSecretRotateFromPlaintextRequiresConfirm(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	identity := filepath.Join(h.work, "identity.txt")
	_, err := h.run("secret", "keygen", "--output", identity)
	require.NoError(t, err)
	pub, err := crypto.PublicKeyFromIdentity(identity)
	require.NoError(t, err)

	plain := h.write("plain.env", "TOKEN=fresh\n")
	encrypted := filepath.Join(h.work, "secret.age")

	_, err = h.run("secret", "rotate", encrypted, "--new-recipient", pub, "--from-plaintext", plain)
	require.ErrorIs(t, err, ErrUsage)
	require.FileExists(t, plain, "nothing may be destroyed without --confirm")

	_, err = h.run("secret", "rotate", encrypted, "--new-recipient", pub, "--from-plaintext", plain, "--confirm")
	require.NoError(t, err)
	require.NoFileExists(t, plain, "the plaintext source is wiped and deleted")

	ciphertext, err := os.ReadFile(encrypted)
	require.NoError(t, err)
	got, err := crypto.Decrypt(ciphertext, identity)
	require.NoError(t, err)
	require.Equal(t, "TOKEN=fresh\n", string(got))
}

func TestSecretUsageErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plain := h.write("plain.env", "x")

	for name, args := range map[string][]string{
		"encrypt without output":     {"secret", "encrypt", plain, "-r", "age1x"},
		"encrypt without recipient":  {"secret", "encrypt", plain, "-o", "/tmp/x.age"},
		"decrypt without output":     {"secret", "decrypt", plain},
		"rotate without a recipient": {"secret", "rotate", plain},
	} {
		_, err := h.run(args...)
		require.ErrorIs(t, err, ErrUsage, name)
	}
}

func TestSecretDecryptWithoutAnIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plain := h.write("x.age", "not really encrypted")
	_, err := h.run("secret", "decrypt", plain, "-o", filepath.Join(h.work, "out"))
	require.ErrorIs(t, err, crypto.ErrIdentityRequired)
	require.Equal(t, 1, ExitCode(err))
}

func TestSecretDecryptWithAMissingIdentityFile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	plain := h.write("x.age", "not really encrypted")
	_, err := h.run("secret", "decrypt", plain, "-o", filepath.Join(h.work, "out"),
		"-i", filepath.Join(h.work, "absent.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestWipeOverwritesBeforeUnlinking(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	secret := []byte("PASSWORD=hunter2")
	require.NoError(t, os.WriteFile(path, secret, 0o600))

	observer, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = observer.Close() }()

	require.NoError(t, wipe(path))
	require.NoFileExists(t, path)

	after := make([]byte, len(secret))
	n, _ := observer.ReadAt(after, 0)
	require.Equal(t, make([]byte, len(secret)), after[:n], "the bytes must be overwritten, not just unlinked")
}

func TestWipeOnAMissingFile(t *testing.T) {
	t.Parallel()
	require.Error(t, wipe(filepath.Join(t.TempDir(), "absent")))
}

func TestSelfUninstall(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	binary := filepath.Join(h.work, "rv")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755))
	h.env.Executable = binary
	require.NoError(t, os.MkdirAll(h.env.Paths.ConfigDir, 0o700))

	out, err := h.run("self-uninstall", "--force")
	require.NoError(t, err)
	require.Contains(t, out, "removed "+binary)
	require.NoFileExists(t, binary)
	require.DirExists(t, h.env.Paths.ConfigDir, "the config survives without --purge-config")
}

func TestSelfUninstallConfirms(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	binary := filepath.Join(h.work, "rv")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755))
	h.env.Executable = binary
	h.env.In = strings.NewReader("n\n")

	out, err := h.run("self-uninstall")
	require.NoError(t, err)
	require.Contains(t, out, "nothing was removed")
	require.FileExists(t, binary)
}

// --purge-config deletes the age identity, so the user is told plainly before it happens.
func TestSelfUninstallPurgeWarnsAboutTheIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	binary := filepath.Join(h.work, "rv")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755))
	h.env.Executable = binary

	pub, identity, err := crypto.GenerateKeypair()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(h.env.Paths.ConfigDir, 0o700))
	require.NoError(t, crypto.WriteIdentityFile(h.env.Paths.IdentityFile, pub, identity))

	out, err := h.run("self-uninstall", "--force", "--purge-config")
	require.NoError(t, err)
	require.Contains(t, out, "permanently undecryptable")
	require.NoDirExists(t, h.env.Paths.ConfigDir)
	require.NoFileExists(t, binary)
}

// Purging deletes the backups an unrecovered transaction needs, so that is called out too.
func TestSelfUninstallPurgeWarnsAboutUnrecoveredJournals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	binary := filepath.Join(h.work, "rv")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755))
	h.env.Executable = binary
	h.crash("crashed")

	out, err := h.run("self-uninstall", "--force", "--purge-config")
	require.NoError(t, err)
	require.Contains(t, out, "rv recover")
	require.Contains(t, out, "interrupted transaction")
}

func TestSelfUninstallLeavesWorkspacesAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	binary := filepath.Join(h.work, "rv")
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755))
	h.env.Executable = binary
	h.write("manifest.yaml", "version: 2\nprofiles: {base: {}}\n")

	_, err := h.run("self-uninstall", "--force", "--purge-config")
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(h.work, "manifest.yaml"))
}
