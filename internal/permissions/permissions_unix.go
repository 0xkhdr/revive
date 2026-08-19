//go:build unix

// Package permissions enforces and verifies POSIX file modes and ownership.
//
// It is POSIX-only by design. The Python implementation mapped modes to Windows read-only
// attributes and skipped chown with a warning, which made "0600 enforced" untrue on that
// platform; a build tag is a better answer than a security-relevant lie.
package permissions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"strconv"
	"strings"
)

// Sentinel errors. Callers distinguish "the manifest is wrong" from "the OS said no".
var (
	// ErrInvalidMode covers an unparseable permission string.
	ErrInvalidMode = errors.New("invalid octal permissions")
	// ErrUnknownUser is a validation error: the named owner does not exist on this system.
	ErrUnknownUser = errors.New("user does not exist on this system")
	// ErrEnforce is an operating-system refusal, typically chown without root.
	ErrEnforce = errors.New("failed to enforce permissions")
)

// Parse converts a permission string to a file mode.
//
// Two spellings are accepted: `0644`, the manifest form, and `0o644`, the form Python's oct()
// writes into a journal. The journal spelling is part of the compatibility contract, so it must
// parse here even though the manifest loader rejects it. Everything else — `644`, `rwx` — is an
// error.
func Parse(s string) (fs.FileMode, error) {
	var digits string
	switch {
	case strings.HasPrefix(s, "0o"), strings.HasPrefix(s, "0O"):
		digits = s[2:]
	case strings.HasPrefix(s, "0"):
		digits = s[1:]
	default:
		return 0, fmt.Errorf("%w: %q must start with 0 or 0o", ErrInvalidMode, s)
	}
	if digits == "" || len(digits) > 4 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidMode, s)
	}
	v, err := strconv.ParseUint(digits, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidMode, s)
	}
	return fs.FileMode(v) & fs.ModePerm, nil
}

// Format renders a mode the way the journal stores it, matching Python's oct().
func Format(mode fs.FileMode) string { return fmt.Sprintf("0o%o", mode.Perm()) }

// FormatManifest renders a mode in the manifest's 4-digit form.
func FormatManifest(mode fs.FileMode) string { return fmt.Sprintf("0%03o", mode.Perm()) }

// Enforce applies mode to path, and ownership when owner is set.
//
// Symlinks are skipped: chmod follows the link, which would silently change the mode of whatever
// it points at rather than the link rv just created.
func Enforce(path, mode string, owner *string) error {
	m, err := Parse(mode)
	if err != nil {
		return err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrEnforce, err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		// A symlink has no meaningful mode of its own on Linux.
		return enforceOwner(path, owner)
	}
	if err := os.Chmod(path, m); err != nil {
		return fmt.Errorf("%w: chmod %s to %s: %w", ErrEnforce, path, mode, err)
	}
	return enforceOwner(path, owner)
}

func enforceOwner(path string, owner *string) error {
	if owner == nil || *owner == "" {
		return nil
	}
	u, err := user.Lookup(*owner)
	if err != nil {
		var unknown user.UnknownUserError
		if errors.As(err, &unknown) {
			return fmt.Errorf("%w: %q", ErrUnknownUser, *owner)
		}
		return fmt.Errorf("%w: looking up %q: %w", ErrEnforce, *owner, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("%w: uid %q of user %q is not numeric", ErrEnforce, u.Uid, *owner)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("%w: gid %q of user %q is not numeric", ErrEnforce, u.Gid, *owner)
	}
	// Lchown, not Chown: never write ownership through a symlink.
	if err := os.Lchown(path, uid, gid); err != nil {
		return fmt.Errorf("%w: chown %s to %s: %w", ErrEnforce, path, *owner, err)
	}
	return nil
}

// Verify reports whether path's mode equals the expected permission string.
func Verify(path, mode string) (bool, error) {
	m, err := Parse(mode)
	if err != nil {
		return false, err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrEnforce, err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		// A symlink's own mode is not meaningful and rv never sets it.
		return true, nil
	}
	return fi.Mode().Perm() == m, nil
}
