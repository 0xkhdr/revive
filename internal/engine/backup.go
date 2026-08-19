package engine

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/profile"
	"github.com/0xkhdr/revive/internal/transaction"
)

// BackupAction is what happened, or would happen, to one item.
type BackupAction string

// Backup actions.
const (
	BackedUp      BackupAction = "backed_up"
	AlreadyInSync BackupAction = "in_sync"
	SkippedItem   BackupAction = "skipped"
	FailedItem    BackupAction = "failed"
)

// BackupItem is one target's outcome.
type BackupItem struct {
	AssetID string
	Target  string
	// Destination is the repository path written, empty when nothing was.
	Destination string
	Action      BackupAction
	Reason      string
}

// BackupResult summarizes a run.
type BackupResult struct {
	Items  []BackupItem
	DryRun bool
}

// BackupOptions configures one backup run.
type BackupOptions struct {
	RepoDir      string
	ManifestPath string
	Profiles     []string
	Identity     string
	DryRun       bool
}

// Backup pulls machine state back into the repository — the inverse of restore, for a file that
// was edited in place and should be committed.
//
// Writes go through the same atomic-write helper restore uses, so an interrupted backup cannot
// leave a truncated file in the repository. [DIVERGE] — the Python implementation wrote directly.
func (r *Restorer) Backup(ctx context.Context, opts BackupOptions) (*BackupResult, error) {
	m, err := manifest.Load(opts.ManifestPath)
	if err != nil {
		return nil, err
	}
	resolved, err := profile.Resolve(m, opts.Profiles...)
	if err != nil {
		return nil, err
	}
	// Machine overrides are merged first, so machine-specific target paths resolve correctly.
	if err := profile.ApplyOverrides(m, resolved, opts.RepoDir, r.Hostname); err != nil {
		return nil, err
	}

	identity, err := r.resolveIdentity(Options{Identity: opts.Identity}, resolved)
	if err != nil {
		return nil, err
	}
	h := r.newHandler(Options{RepoDir: opts.RepoDir}, identity)

	result := &BackupResult{DryRun: opts.DryRun}
	for _, id := range resolved.AssetIDs() {
		result.Items = append(result.Items, r.backupAsset(ctx, h, resolved.Assets[id], opts)...)
	}
	for _, id := range resolved.SecretIDs() {
		result.Items = append(result.Items, r.backupAsset(ctx, h, resolved.Secrets[id].Asset(), opts)...)
	}
	return result, nil
}

func (r *Restorer) backupAsset(ctx context.Context, h *Handler, asset manifest.Asset, opts BackupOptions) []BackupItem {
	// A rendered file cannot be un-rendered: writing it back over the template would destroy
	// the template.
	if asset.Type == manifest.TypeTemplate {
		r.log().Warn("skipping template asset; a rendered file cannot be un-rendered", "asset_id", asset.ID)
		return []BackupItem{{AssetID: asset.ID, Action: SkippedItem, Reason: "templates cannot be backed up"}}
	}

	targets, err := h.Targets(asset)
	if err != nil {
		return []BackupItem{{AssetID: asset.ID, Action: FailedItem, Reason: err.Error()}}
	}

	absSource := h.AbsSource(asset)
	multi := len(asset.Target.Values) > 1 || isDirectory(absSource)

	items := make([]BackupItem, 0, len(targets))
	for _, target := range targets {
		items = append(items, r.backupTarget(ctx, asset, absSource, target, multi, opts))
	}
	return items
}

func (r *Restorer) backupTarget(ctx context.Context, asset manifest.Asset, absSource, target string, multi bool, opts BackupOptions) BackupItem {
	item := BackupItem{AssetID: asset.ID, Target: target}

	fi, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		item.Action, item.Reason = SkippedItem, "the target does not exist on this machine"
		r.log().Warn("skipping missing target", "asset_id", asset.ID, "target", target)
		return item
	}
	if err != nil {
		item.Action, item.Reason = FailedItem, err.Error()
		return item
	}

	if fi.Mode()&fs.ModeSymlink != 0 {
		link, err := os.Readlink(target)
		if err != nil {
			item.Action, item.Reason = FailedItem, err.Error()
			return item
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(target), link)
		}
		// A link already pointing into the repository is the steady state of a symlink asset:
		// editing through it already edited the repo.
		if withinRepo(opts.RepoDir, link) {
			item.Action, item.Reason = AlreadyInSync, "the target is a symlink into the repository"
			return item
		}
		// A link pointing elsewhere is followed, and the real contents are what get copied.
	}

	content, err := os.ReadFile(target)
	if err != nil {
		item.Action, item.Reason = FailedItem, err.Error()
		return item
	}
	defer crypto.Zero(content)

	// A directory source or a list of targets writes one file per target basename, and only
	// that form gains the .age suffix — a single source path already carries its own name.
	destination := absSource
	if multi {
		destination = filepath.Join(absSource, filepath.Base(target))
		if asset.Encrypted {
			destination += ".age"
		}
	}
	item.Destination = destination

	if opts.DryRun {
		item.Action = BackedUp
		return item
	}

	payload := content
	if asset.Encrypted {
		recipient, err := crypto.PublicKeyFromIdentity(r.identityFor(opts))
		if err != nil {
			item.Action, item.Reason = FailedItem, err.Error()
			return item
		}
		ciphertext, err := crypto.Encrypt(content, []string{recipient})
		if err != nil {
			item.Action, item.Reason = FailedItem, err.Error()
			return item
		}
		payload = ciphertext
	}

	if err := ctx.Err(); err != nil {
		item.Action, item.Reason = FailedItem, err.Error()
		return item
	}
	if err := transaction.AtomicWrite(destination, payload, 0o644); err != nil {
		item.Action, item.Reason = FailedItem, err.Error()
		return item
	}
	item.Action = BackedUp
	return item
}

// identityFor resolves the identity used to derive the recipient a secret is re-encrypted to.
func (r *Restorer) identityFor(opts BackupOptions) string {
	if opts.Identity != "" {
		return opts.Identity
	}
	for _, candidate := range r.Paths.IdentityCandidates() {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func withinRepo(repoDir, path string) bool {
	rel, err := filepath.Rel(repoDir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !isParentEscape(rel)
}

func isParentEscape(rel string) bool {
	return len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}

func isDirectory(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
