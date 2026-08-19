// Package status reports drift: where the machine no longer matches what the manifest declares.
// It mutates nothing.
package status

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/engine"
	"github.com/0xkhdr/revive/internal/lockfile"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/permissions"
	"github.com/0xkhdr/revive/internal/profile"
)

// Value is one target's drift status.
type Value string

// The six status values. The overall report is drifted when any target is not InSync.
const (
	InSync             Value = "in_sync"
	Missing            Value = "missing"
	TypeMismatch       Value = "type_mismatch"
	PermissionsDrifted Value = "permissions_drifted"
	Modified           Value = "modified"
	Error              Value = "error"
)

// Default modes when an asset declares none.
const (
	defaultMode = "0644"
	secretMode  = "0600"
)

// mtimeTolerance absorbs filesystem timestamp granularity when comparing against the lockfile.
const mtimeTolerance = time.Millisecond

// Result is one target's status.
type Result struct {
	AssetID string `json:"asset_id"`
	Target  string `json:"target"`
	Status  Value  `json:"status"`
	Detail  string `json:"detail,omitempty"`
}

// Report is the whole drift picture.
type Report struct {
	Drifted bool     `json:"drifted"`
	Results []Result `json:"results"`
}

// Checker compares a resolved profile against the filesystem.
type Checker struct {
	Handler  *engine.Handler
	Lockfile *lockfile.Lockfile
}

// New builds a Checker.
func New(h *engine.Handler, lf *lockfile.Lockfile) *Checker {
	if lf == nil {
		lf = lockfile.New()
	}
	return &Checker{Handler: h, Lockfile: lf}
}

// Check walks every asset and secret in the resolved profile, in resolution order so the report
// is reproducible.
func (c *Checker) Check(resolved *profile.Resolved) *Report {
	report := &Report{}
	for _, id := range resolved.AssetIDs() {
		report.Results = append(report.Results, c.CheckAsset(resolved.Assets[id])...)
	}
	for _, id := range resolved.SecretIDs() {
		report.Results = append(report.Results, c.CheckAsset(resolved.Secrets[id].Asset())...)
	}
	for _, r := range report.Results {
		if r.Status != InSync {
			report.Drifted = true
			break
		}
	}
	return report
}

// CheckAsset returns one result per target.
func (c *Checker) CheckAsset(asset manifest.Asset) []Result {
	targets, err := c.Handler.Targets(asset)
	if err != nil {
		// A target that will not interpolate cannot be checked at all.
		return []Result{{AssetID: asset.ID, Status: Error, Detail: err.Error()}}
	}

	absSource := c.Handler.AbsSource(asset)
	out := make([]Result, 0, len(targets))
	for _, target := range targets {
		status, detail := c.checkTarget(asset, absSource, target)
		out = append(out, Result{AssetID: asset.ID, Target: target, Status: status, Detail: detail})
	}
	return out
}

// checkTarget applies the checks in the order the spec fixes: existence, then type, then
// permissions, then content.
//
// The order is the point. Reporting "modified" on a file that is actually the wrong type would
// send the user looking at a diff for a problem a diff cannot show.
func (c *Checker) checkTarget(asset manifest.Asset, absSource, target string) (Value, string) {
	source, err := c.Handler.ResolveSource(absSource, target, asset.Encrypted)
	if err != nil {
		return Error, err.Error()
	}

	fi, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return Missing, ""
	}
	if err != nil {
		return Error, err.Error()
	}
	isLink := fi.Mode()&fs.ModeSymlink != 0

	if asset.Type == manifest.TypeSymlink {
		if !isLink {
			return TypeMismatch, "a symlink was declared but a regular file is present"
		}
		link, err := os.Readlink(target)
		if err != nil {
			return Error, err.Error()
		}
		// A relative link resolves against the link's own directory, not the process's cwd.
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(target), link)
		}
		if filepath.Clean(link) != filepath.Clean(source) {
			return Modified, fmt.Sprintf("points at %s, expected %s", link, source)
		}
		return InSync, ""
	}

	if isLink {
		return TypeMismatch, "a regular file was declared but a symlink is present"
	}

	expected := expectedMode(asset)
	ok, err := permissions.Verify(target, expected)
	if err != nil {
		return Error, err.Error()
	}
	if !ok {
		return PermissionsDrifted, fmt.Sprintf("expected %s, found %s",
			expected, permissions.FormatManifest(fi.Mode().Perm()))
	}

	return c.checkContent(asset, source, target, fi)
}

func expectedMode(asset manifest.Asset) string {
	if asset.Permissions != nil && *asset.Permissions != "" {
		return *asset.Permissions
	}
	if asset.Type == manifest.TypeSecret {
		return secretMode
	}
	return defaultMode
}

// checkContent compares what is on disk against what the declaration would produce.
func (c *Checker) checkContent(asset manifest.Asset, source, target string, fi fs.FileInfo) (Value, string) {
	switch {
	case asset.Type == manifest.TypeTemplate:
		rendered, err := c.Handler.RenderAsset(asset, source)
		if err != nil {
			return Error, err.Error()
		}
		return compareHash(hashBytes(rendered), target)

	case asset.Encrypted:
		return c.compareEncrypted(asset, source, target, fi)

	case isDir(source):
		// The deterministic sorted-walk hash the lockfile already computes is used here too.
		// The reference compared a directory's mtime against the lockfile, which misses an
		// edited file and false-positives on a preserved timestamp.
		want, err := lockfile.SHA256(source)
		if err != nil {
			return Error, err.Error()
		}
		got, err := lockfile.SHA256(target)
		if err != nil {
			return Error, err.Error()
		}
		if want != got {
			return Modified, "directory contents differ"
		}
		return InSync, ""

	default:
		want, err := lockfile.SHA256(source)
		if err != nil {
			return Error, err.Error()
		}
		return compareHash(want, target)
	}
}

// compareEncrypted compares an encrypted source against its target.
//
// Without an identity there is nothing to compare against, so it falls back to the lockfile's
// recorded mtime, and reports drift when even that is unavailable. Failing safe here means
// assuming the file changed.
func (c *Checker) compareEncrypted(asset manifest.Asset, source, target string, fi fs.FileInfo) (Value, string) {
	if c.Handler.Identity != "" {
		plaintext, err := c.Handler.Decrypt(asset.ID, source)
		if err == nil {
			defer crypto.Zero(plaintext)
			return compareHash(hashBytes(plaintext), target)
		}
		// Decryption failure falls through to the mtime path rather than reporting an error:
		// a wrong key should not make status useless.
	}

	entry, ok := c.Lockfile.Entries[asset.ID]
	if !ok {
		return Modified, "no identity and no lockfile entry, so the content cannot be verified"
	}
	recorded, ok := entry.MTimeFor(target)
	if !ok {
		return Modified, "no identity and no recorded mtime for this target"
	}

	actual := float64(fi.ModTime().UnixNano()) / float64(time.Second)
	if diff := actual - recorded; diff > mtimeTolerance.Seconds() || diff < -mtimeTolerance.Seconds() {
		return Modified, "modification time differs from the lockfile"
	}
	return InSync, ""
}

func compareHash(want, target string) (Value, string) {
	got, err := lockfile.SHA256(target)
	if err != nil {
		return Error, err.Error()
	}
	if want != got {
		return Modified, "content differs from the source"
	}
	return InSync, ""
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// isBinary reports whether content looks like a binary file, using the same NUL-byte heuristic
// git uses. Diffing binary content produces noise, not information.
func isBinary(content []byte) bool {
	limit := min(len(content), 8000)
	return bytes.IndexByte(content[:limit], 0) >= 0
}
