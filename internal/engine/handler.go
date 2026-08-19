// Package engine turns a resolved profile into planned transaction operations, and drives the
// restore.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"time"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/lockfile"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/transaction"
)

// Sentinel errors.
var (
	// ErrSourceNotFound is returned when an asset's source is missing from the repo.
	ErrSourceNotFound = errors.New("asset source not found")
	// ErrTargetConflict is returned when a target exists and the conflict strategy forbids
	// overwriting it — including `prompt` with nobody to ask.
	ErrTargetConflict = errors.New("target exists and conflict strategy forbids overwrite")
	// ErrSymlinkLoop is returned when an asset's source is a cyclic symlink chain.
	ErrSymlinkLoop = errors.New("symlink loop detected in asset source")
	// ErrUnsupportedType is returned for an asset type the handler does not know.
	ErrUnsupportedType = errors.New("unsupported asset type")
)

// secretMode is the mode a secret lands with when the manifest declares none.
const secretMode = "0600"

// ownershipTolerance absorbs filesystem timestamp granularity when deciding whether a target is
// still exactly what rv last wrote.
const ownershipTolerance = time.Millisecond

// Confirm asks the user a yes/no question. It is nil in non-interactive mode, where a `prompt`
// conflict is a hard error rather than a silent skip.
type Confirm func(prompt string) (bool, error)

// Handler plans one asset at a time. It performs I/O — stat, read, decrypt — but never mutates,
// which is what makes planning safe to run in parallel, safe under --dry-run, and safe to
// abandon.
type Handler struct {
	RepoDir  string
	Paths    paths.Config
	Identity string // "" when no identity was resolved
	Lookup   paths.Lookup
	Confirm  Confirm
	// Lockfile records what rv last wrote. It is what lets conflict resolution tell a target rv
	// owns from one the user put there. Nil means "own nothing", which is the first-run case.
	Lockfile *lockfile.Lockfile

	// Template built-ins, injected so tests do not depend on the host.
	Hostname string
	User     string
	Platform string
	Arch     string
	Home     string
}

// NewHandler builds a Handler with the built-ins filled in from the running system.
func NewHandler(repoDir string, cfg paths.Config) *Handler {
	h := &Handler{
		RepoDir:  repoDir,
		Paths:    cfg,
		Lookup:   os.LookupEnv,
		Platform: runtime.GOOS,
		Arch:     runtime.GOARCH,
		Home:     cfg.Home,
	}
	if name, err := os.Hostname(); err == nil {
		h.Hostname = name
	}
	if u, err := user.Current(); err == nil {
		h.User = u.Username
	}
	return h
}

// Plan is one asset's contribution to the transaction.
type Plan struct {
	AssetID string
	// Ops are in execution order: delete, pre-hook, write, chmod, post-hook, per target.
	Ops []transaction.Operation
	// Targets are the absolute paths that were planned, for the lockfile.
	Targets []string
	// RenderedChecksum is the SHA-256 of a template's rendered output, empty otherwise.
	RenderedChecksum string
	// Skipped is true when conflict resolution skipped every target.
	Skipped bool
}

// PlanAsset turns one asset into operations. It returns them rather than appending to a shared
// transaction, so parallel workers cannot interleave and the caller merges in item order.
func (h *Handler) PlanAsset(asset manifest.Asset) (Plan, error) {
	plan := Plan{AssetID: asset.ID}

	absSource := filepath.Join(h.RepoDir, asset.Source)
	if _, err := os.Lstat(absSource); err != nil && !asset.Encrypted {
		return plan, fmt.Errorf("%w: %s for asset %q", ErrSourceNotFound, absSource, asset.ID)
	}

	for _, expr := range asset.Target.Values {
		interpolated, err := paths.Interpolate(expr, h.lookup())
		if err != nil {
			return plan, fmt.Errorf("asset %q: %w", asset.ID, err)
		}
		absTarget := h.Paths.Canonicalize(interpolated)

		targetSource, err := h.resolveSource(absSource, absTarget, asset.Encrypted)
		if err != nil {
			return plan, fmt.Errorf("asset %q: %w", asset.ID, err)
		}

		proceed, err := h.resolveConflict(asset, targetSource, absTarget)
		if err != nil {
			return plan, err
		}
		if !proceed {
			// A skipped target contributes no operations at all, hooks included: the asset was
			// not applied, so its hooks have nothing to bracket.
			continue
		}

		ops, checksum, err := h.planTarget(asset, targetSource, absTarget)
		if err != nil {
			return plan, err
		}
		plan.Ops = append(plan.Ops, ops...)
		plan.Targets = append(plan.Targets, absTarget)
		if checksum != "" {
			plan.RenderedChecksum = checksum
		}
	}

	plan.Skipped = len(plan.Targets) == 0
	return plan, nil
}

func (h *Handler) lookup() paths.Lookup {
	if h.Lookup != nil {
		return h.Lookup
	}
	return os.LookupEnv
}

// AbsSource returns an asset's source path inside the repository.
func (h *Handler) AbsSource(asset manifest.Asset) string {
	return filepath.Join(h.RepoDir, asset.Source)
}

// Targets interpolates and canonicalizes every target of an asset.
//
// Exported so the status engine resolves targets exactly the way planning does: two
// implementations of the same rules would drift, and drift here means status disagreeing with
// restore about which file it is talking about.
func (h *Handler) Targets(asset manifest.Asset) ([]string, error) {
	out := make([]string, 0, len(asset.Target.Values))
	for _, expr := range asset.Target.Values {
		interpolated, err := paths.Interpolate(expr, h.lookup())
		if err != nil {
			return nil, fmt.Errorf("asset %q: %w", asset.ID, err)
		}
		out = append(out, h.Paths.Canonicalize(interpolated))
	}
	return out, nil
}

// ResolveSource picks the file inside a directory source that matches a target's basename.
func (h *Handler) ResolveSource(absSource, absTarget string, encrypted bool) (string, error) {
	return h.resolveSource(absSource, absTarget, encrypted)
}

// RenderAsset renders a template asset's source with the merged context.
func (h *Handler) RenderAsset(asset manifest.Asset, source string) ([]byte, error) {
	return h.render(asset, source)
}

// Decrypt returns the plaintext of an encrypted source. The caller zeros it once consumed.
func (h *Handler) Decrypt(assetID, source string) ([]byte, error) {
	return h.decrypt(assetID, source)
}

// resolveSource picks the file inside a directory source that matches this target's basename.
// One `secrets/app_env/` directory populates both `.env` and `.env.deploy` this way.
func (h *Handler) resolveSource(absSource, absTarget string, encrypted bool) (string, error) {
	fi, err := os.Stat(absSource)
	if err != nil || !fi.IsDir() {
		//nolint:nilerr // A source that cannot be statted is simply not a directory, so there is
		// no fan-out to do. Whether it exists at all was decided by the caller.
		return absSource, nil
	}
	base := filepath.Base(absTarget)

	candidates := make([]string, 0, 2)
	if encrypted {
		// `<basename>.age` first: an encrypted directory holds ciphertext, not plaintext.
		candidates = append(candidates, filepath.Join(absSource, base+".age"))
	}
	candidates = append(candidates, filepath.Join(absSource, base))

	for _, c := range candidates {
		if _, err := os.Lstat(c); err == nil {
			return c, nil
		}
	}

	// No match: the directory itself is the source, which is a directory copy.
	return absSource, nil
}

// resolveConflict reports whether this target should be planned.
func (h *Handler) resolveConflict(asset manifest.Asset, source, absTarget string) (bool, error) {
	if _, err := os.Lstat(absTarget); err != nil {
		//nolint:nilerr // Nothing at the target means no conflict to resolve, which is the
		// common case, not an error.
		return true, nil
	}

	switch asset.ConflictStrategy {
	case manifest.ConflictSkip:
		return false, nil
	case manifest.ConflictOverwrite:
		return true, nil
	case manifest.ConflictAbort:
		return false, fmt.Errorf("%w: %s for asset %q (strategy: abort)", ErrTargetConflict, absTarget, asset.ID)
	case manifest.ConflictPrompt:
		// A target rv itself wrote, and that nobody has touched since, is not a conflict —
		// prompting exists to protect the user's files, not rv's own output. Without this a
		// second `rv restore` on the default strategy is an error rather than a no-op.
		//
		// Only `prompt` is short-circuited. `skip` and `abort` are explicit instructions about
		// what to do when the target exists, and rv does not get to reinterpret them.
		if h.ownsTarget(asset.ID, source, absTarget) {
			return true, nil
		}
		if h.Confirm == nil {
			// Never silently skip: that is silent data loss wearing a success message.
			return false, fmt.Errorf("%w: %s for asset %q has strategy 'prompt' but there is no "+
				"interactive terminal to ask; set conflict_strategy explicitly or run interactively",
				ErrTargetConflict, absTarget, asset.ID)
		}
		ok, err := h.Confirm(fmt.Sprintf("Target %q already exists. Overwrite?", absTarget))
		if err != nil {
			return false, fmt.Errorf("asset %q: %w", asset.ID, err)
		}
		return ok, nil
	default:
		return false, fmt.Errorf("%w: conflict strategy %q", ErrUnsupportedType, asset.ConflictStrategy)
	}
}

// ownsTarget reports whether the lockfile says rv wrote this exact target and nothing has
// changed it since.
//
// Failing closed — treating an unknown or mismatched target as a conflict — is what keeps this
// from becoming a silent overwrite.
func (h *Handler) ownsTarget(assetID, source, absTarget string) bool {
	if h.Lockfile == nil {
		return false
	}
	entry, ok := h.Lockfile.Entries[assetID]
	if !ok {
		return false
	}
	if _, ok := entry.MTimeFor(absTarget); !ok {
		// The lockfile knows the asset but not this target, so rv has never written here.
		return false
	}

	fi, err := os.Lstat(absTarget)
	if err != nil {
		return false
	}

	// A symlink is owned when it still points where rv pointed it. Its own modification time is
	// no use here: rv recreates the link on every restore, and the lockfile records the
	// destination's timestamp, which moves whenever the repo source is edited — which is the
	// normal way to work with a symlink asset.
	if fi.Mode()&fs.ModeSymlink != 0 {
		link, err := os.Readlink(absTarget)
		if err != nil {
			return false
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(absTarget), link)
		}
		return filepath.Clean(link) == filepath.Clean(source)
	}

	// For a regular file the recorded modification time is the test: rv sets it on every write,
	// and any edit by the user or another program moves it.
	recorded, _ := entry.MTimeFor(absTarget)
	actual := float64(fi.ModTime().UnixNano()) / float64(time.Second)
	return math.Abs(actual-recorded) <= ownershipTolerance.Seconds()
}

// planTarget emits one target's operations in the order the spec fixes:
// delete → pre-hook → write → chmod → post-hook.
func (h *Handler) planTarget(asset manifest.Asset, source, target string) ([]transaction.Operation, string, error) {
	var ops []transaction.Operation

	if _, err := os.Lstat(target); err == nil {
		ops = append(ops, transaction.Operation{Type: transaction.OpTypeDelete, Target: target})
	}

	pre, err := planHooks(asset, asset.Hooks.Pre, "pre", target)
	if err != nil {
		return nil, "", err
	}
	ops = append(ops, pre...)

	write, checksum, err := h.planWrite(asset, source, target)
	if err != nil {
		return nil, "", err
	}
	ops = append(ops, write)

	// The write already carries the mode, so a secret is never briefly world-readable. The
	// separate chmod is what applies `owner`, and it keeps the operation sequence the one the
	// spec fixes.
	if write.Permissions != "" || asset.Owner != nil {
		ops = append(ops, transaction.Operation{
			Type:        transaction.OpTypeChmod,
			Target:      target,
			Permissions: write.Permissions,
			Owner:       asset.Owner,
		})
	}

	post, err := planHooks(asset, asset.Hooks.Post, "post", target)
	if err != nil {
		return nil, "", err
	}
	return append(ops, post...), checksum, nil
}

func (h *Handler) planWrite(asset manifest.Asset, source, target string) (transaction.Operation, string, error) {
	op := transaction.Operation{Target: target, Owner: asset.Owner}
	if asset.Permissions != nil {
		op.Permissions = *asset.Permissions
	}

	switch asset.Type {
	case manifest.TypeSymlink:
		if h.Paths.DetectSymlinkLoop(source) {
			return op, "", fmt.Errorf("%w: %s for asset %q", ErrSymlinkLoop, source, asset.ID)
		}
		op.Type = transaction.OpTypeSymlink
		op.Source = transaction.SourcePath{Path: source}
		return op, "", nil

	case manifest.TypeCopy:
		op.Type = transaction.OpTypeCopy
		if !asset.Encrypted {
			op.Source = transaction.SourcePath{Path: source}
			return op, "", nil
		}
		plaintext, err := h.decrypt(asset.ID, source)
		if err != nil {
			return op, "", err
		}
		op.Source = transaction.SourceBytes{Data: plaintext}
		return op, "", nil

	case manifest.TypeTemplate:
		rendered, err := h.render(asset, source)
		if err != nil {
			return op, "", err
		}
		op.Type = transaction.OpTypeCopy
		op.Source = transaction.SourceBytes{Data: rendered}
		sum := sha256.Sum256(rendered)
		return op, hex.EncodeToString(sum[:]), nil

	case manifest.TypeSecret:
		plaintext, err := h.decrypt(asset.ID, source)
		if err != nil {
			return op, "", err
		}
		op.Type = transaction.OpTypeCopy
		op.Source = transaction.SourceBytes{Data: plaintext}
		if op.Permissions == "" {
			op.Permissions = secretMode
		}
		return op, "", nil

	default:
		return op, "", fmt.Errorf("%w: %q for asset %q", ErrUnsupportedType, asset.Type, asset.ID)
	}
}

// decrypt reads a .age file and returns its plaintext. The bytes travel into the operation; the
// caller zeros them once the transaction is done with them.
func (h *Handler) decrypt(assetID, source string) ([]byte, error) {
	if h.Identity == "" {
		return nil, fmt.Errorf("%w: asset %q; pass --identity or create %s",
			crypto.ErrIdentityRequired, assetID, h.Paths.IdentityFile)
	}
	ciphertext, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("reading encrypted source for asset %q: %w", assetID, err)
	}
	plaintext, err := crypto.Decrypt(ciphertext, h.Identity)
	if err != nil {
		return nil, fmt.Errorf("decrypting asset %q: %w", assetID, err)
	}
	return plaintext, nil
}

// render builds the merged context and renders the template.
//
// Merge order, later winning: process environment, then built-ins, then the asset's
// template_vars.
func (h *Handler) render(asset manifest.Asset, source string) ([]byte, error) {
	raw, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("%w: reading template source for asset %q: %w", ErrTemplate, asset.ID, err)
	}

	ctx := map[string]any{}
	for _, kv := range os.Environ() {
		if k, v, ok := splitEnv(kv); ok {
			ctx[k] = v
		}
	}
	ctx["_hostname"] = h.Hostname
	ctx["_user"] = h.User
	ctx["_platform"] = h.Platform
	ctx["_arch"] = h.Arch
	ctx["_home"] = h.Home
	ctx["_repo_dir"] = h.RepoDir
	for k, v := range asset.TemplateVars {
		ctx[k] = v
	}

	return Render(asset.ID, raw, ctx)
}

func splitEnv(kv string) (string, string, bool) {
	for i := range len(kv) {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], i > 0
		}
	}
	return "", "", false
}

// planHooks turns an asset's hooks into planned operations. They are planned here and executed
// in the transaction's execute phase, never run during planning.
func planHooks(asset manifest.Asset, hooks []manifest.Hook, stage, target string) ([]transaction.Operation, error) {
	ops := make([]transaction.Operation, 0, len(hooks))
	for _, hook := range hooks {
		if hook.Plugin != "" {
			// Failing loudly is required: silently dropping the hook would break the rollback
			// guarantee, since the user believes something ran.
			return nil, fmt.Errorf("asset %q %s-hook references plugin %q: per-asset plugin hooks "+
				"are not supported, use a profile-level pre-restore/post-restore hook",
				asset.ID, stage, hook.Plugin)
		}
		// Word-split now, so a quoting error fails before anything is snapshotted.
		argv, err := splitWords(hook.Command)
		if err != nil {
			return nil, fmt.Errorf("asset %q %s-hook: %w", asset.ID, stage, err)
		}
		ops = append(ops, transaction.Operation{
			Type:   transaction.OpTypeHook,
			Target: target,
			Hook:   &transaction.HookOp{AssetID: asset.ID, Stage: stage, Command: argv},
		})
	}
	return ops, nil
}
