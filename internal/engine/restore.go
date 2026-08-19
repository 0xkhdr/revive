package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/lockfile"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/permissions"
	"github.com/0xkhdr/revive/internal/profile"
	"github.com/0xkhdr/revive/internal/providers"
	"github.com/0xkhdr/revive/internal/scrub"
	"github.com/0xkhdr/revive/internal/transaction"
)

// ErrIdentityRequired is returned when the profile contains encrypted material and no identity
// could be resolved.
var ErrIdentityRequired = crypto.ErrIdentityRequired

// maxPlanWorkers bounds parallel planning. Planning is I/O bound — stat, read, decrypt — so
// more workers than cores buys nothing.
const maxPlanWorkers = 8

// PluginStage names a profile-level plugin hook stage.
type PluginStage string

// Plugin hook stages.
const (
	PreRestore  PluginStage = "pre-restore"
	PostRestore PluginStage = "post-restore"
)

// PluginHook is what a plugin runner is told about the run. Nil PluginRunner means no plugins,
// which is also what --no-plugins and --dry-run produce.
type PluginHook struct {
	Stage    PluginStage
	TxID     string
	RepoDir  string
	Profiles []string
	// Targets is every absolute path the transaction plans to mutate.
	Targets []string
}

// PluginRunner dispatches profile-level plugin hooks.
type PluginRunner func(ctx context.Context, hook PluginHook) error

// Pruner deletes old backup snapshots. It is nil until stage 11 supplies one.
type Pruner func(ctx context.Context) error

// Options configures one restore run.
type Options struct {
	RepoDir      string
	ManifestPath string
	Profiles     []string
	Identity     string

	DryRun        bool
	NoPlugins     bool
	Sequential    bool
	ForcePackages bool
	Prune         bool
}

// Result summarizes a completed restore.
type Result struct {
	TxID         string
	Profiles     []string
	Assets       int
	Secrets      int
	Skipped      []string
	Packages     int
	LockfilePath string
	DryRun       bool
}

// Restorer drives the fourteen-step apply order. Every seam it needs is a field, so a test can
// run a whole restore inside t.TempDir() without touching the machine.
type Restorer struct {
	Paths    paths.Config
	Log      *slog.Logger
	Now      func() time.Time
	Runner   providers.Runner
	Hostname string
	Confirm  Confirm
	Scrubber *scrub.Scrubber

	// Plugins is nil until stage 12 supplies one; nil means no plugins.
	Plugins PluginRunner
	// Prune deletes old backup snapshots after a successful restore.
	Prune Pruner
	// RequireClean refuses to start when an interrupted transaction has not been recovered.
	// Restoring on top of one would snapshot the broken state as the pre-state, destroying the
	// ability to get back to the original. [DIVERGE]
	RequireClean func() error
}

func (r *Restorer) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (r *Restorer) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Restorer) scrubber() *scrub.Scrubber {
	if r.Scrubber != nil {
		return r.Scrubber
	}
	return scrub.Default
}

// Restore applies the repository to the machine.
//
// The step order is the correctness argument, not a style: lock before load so two runs cannot
// race; resolve before override; decrypt before snapshot so a bad identity fails while the
// filesystem is untouched; snapshot before execute; packages after files so a post-install
// script reads the config rv just wrote; verify after packages so a package overwriting a
// managed file is caught; the lockfile last, because it records a confirmed good state.
func (r *Restorer) Restore(ctx context.Context, opts Options) (*Result, error) {
	// Step 0: the process lock, held for the whole run.
	lock, err := transaction.Acquire(r.Paths.LockFile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Release() }()

	if r.RequireClean != nil {
		if err := r.RequireClean(); err != nil {
			return nil, err
		}
	}

	// Step 1: load and validate the manifest.
	r.log().Info("step 1/14: loading manifest", "path", opts.ManifestPath)
	m, err := manifest.Load(opts.ManifestPath)
	if err != nil {
		return nil, err
	}

	// Step 2: flatten the profile hierarchy.
	r.log().Info("step 2/14: resolving profiles", "profiles", opts.Profiles)
	resolved, err := profile.Resolve(m, opts.Profiles...)
	if err != nil {
		return nil, err
	}

	// Step 3: layer the machine override over the resolved profile.
	r.log().Info("step 3/14: merging machine overrides", "hostname", r.Hostname)
	if err := profile.ApplyOverrides(m, resolved, opts.RepoDir, r.Hostname); err != nil {
		return nil, err
	}

	identity, err := r.resolveIdentity(opts, resolved)
	if err != nil {
		return nil, err
	}

	tx := transaction.New(transaction.Options{
		Paths: r.Paths,
		Now:   r.now,
		Log:   r.log(),
	})
	result := &Result{TxID: tx.TxID, Profiles: opts.Profiles, DryRun: opts.DryRun}

	// Steps 4 and 5: validate dependencies and decrypt, both inside planning.
	r.log().Info("steps 4-5/14: planning assets", "tx_id", tx.TxID, "sequential", opts.Sequential)
	handler := r.newHandler(opts, identity)
	plans, err := r.planAll(ctx, handler, resolved, opts.Sequential)
	if err != nil {
		return nil, err
	}
	// Decrypted plaintext lives in the planned operations until they are executed.
	defer zeroPlans(plans)

	for _, p := range plans {
		if p.Skipped {
			result.Skipped = append(result.Skipped, p.AssetID)
			continue
		}
		for _, op := range p.Ops {
			tx.Plan(op)
		}
		if p.RenderedChecksum != "" {
			tx.RenderedChecksums[p.AssetID] = p.RenderedChecksum
		}
	}
	result.Assets = len(resolved.Assets) - len(result.Skipped)
	result.Secrets = len(resolved.Secrets)

	if opts.DryRun {
		// A dry run stops before step 6. Nothing is snapshotted, nothing is mutated, no
		// lockfile is written, and no hook of any kind runs — they are logged, not executed.
		r.log().Info("dry run: stopping before any mutation", "operations", len(tx.Planned))
		r.logPlan(tx)
		return result, nil
	}

	// Step 6: validate the plan, then snapshot every existing target and write the journal.
	r.log().Info("step 6/14: snapshotting", "operations", len(tx.Planned))
	if err := tx.Validate(); err != nil {
		return nil, err
	}
	if err := tx.Snapshot(); err != nil {
		return nil, err
	}

	// pre-restore runs AFTER the snapshot, so a plugin failure here rolls back cleanly.
	if err := r.runPlugins(ctx, opts, PreRestore, tx); err != nil {
		return nil, err
	}

	// Steps 7 to 9 are one atomic execute: symlinks, copies, permissions, and the per-asset
	// hooks interleaved around their own asset.
	r.log().Info("steps 7-9/14: applying files")
	if err := tx.Execute(ctx); err != nil {
		return nil, r.withHookReport(tx, err)
	}

	// Step 10: packages, in a fixed provider order.
	r.log().Info("step 10/14: installing packages")
	installed, err := r.installPackages(ctx, opts, resolved)
	if err != nil {
		return nil, r.rollbackAfter(tx, 10, err)
	}
	result.Packages = installed

	// Step 11: post-restore plugin hooks, after packages and before verification.
	if err := r.runPlugins(ctx, opts, PostRestore, tx); err != nil {
		return nil, r.rollbackAfter(tx, 11, err)
	}

	// Step 12: verify every target exists with the mode the plan claimed.
	r.log().Info("step 12/14: verifying")
	if err := tx.Verify(); err != nil {
		return nil, r.withHookReport(tx, err)
	}

	// Step 13: record the confirmed good state.
	r.log().Info("step 13/14: updating lockfile")
	lockPath, err := r.updateLockfile(opts, resolved, plans, tx)
	if err != nil {
		return nil, r.rollbackAfter(tx, 13, err)
	}
	result.LockfilePath = lockPath

	// Step 14: commit, clean up, prune.
	r.log().Info("step 14/14: committing", "tx_id", tx.TxID)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx.Cleanup()
	r.prune(ctx)
	return result, nil
}

// resolveIdentity applies the documented resolution order and registers the private key with the
// scrubber before anything else can log it.
func (r *Restorer) resolveIdentity(opts Options, resolved *profile.Resolved) (string, error) {
	needsIdentity := len(resolved.Secrets) > 0
	for _, a := range resolved.Assets {
		if a.Encrypted {
			needsIdentity = true
			break
		}
	}

	candidates := r.Paths.IdentityCandidates()
	if opts.Identity != "" {
		// An explicitly requested identity MUST exist: falling back silently would decrypt with
		// a key the user did not ask for.
		if _, err := os.Stat(opts.Identity); err != nil {
			return "", fmt.Errorf("identity file %s: %w", opts.Identity, err)
		}
		candidates = []string{opts.Identity}
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		key, err := crypto.ResolveIdentity(candidate)
		if err != nil {
			return "", err
		}
		r.scrubber().RegisterSecret(key)
		return candidate, nil
	}

	if needsIdentity {
		return "", fmt.Errorf("%w: this profile has encrypted material; create %s or pass --identity",
			ErrIdentityRequired, r.Paths.IdentityFile)
	}
	// Nothing is encrypted, so no identity is needed.
	return "", nil
}

func (r *Restorer) newHandler(opts Options, identity string) *Handler {
	h := NewHandler(opts.RepoDir, r.Paths)
	h.Identity = identity
	h.Confirm = r.Confirm
	if r.Hostname != "" {
		h.Hostname = r.Hostname
	}
	return h
}

// planAll plans every asset and secret. The item order is fixed up front and results are written
// by index, so --parallel and --sequential produce identical operation ordering.
func (r *Restorer) planAll(ctx context.Context, h *Handler, resolved *profile.Resolved, sequential bool) ([]Plan, error) {
	items := make([]manifest.Asset, 0, len(resolved.Assets)+len(resolved.Secrets))
	for _, id := range resolved.AssetIDs() {
		items = append(items, resolved.Assets[id])
	}
	for _, id := range resolved.SecretIDs() {
		items = append(items, resolved.Secrets[id].Asset())
	}

	plans := make([]Plan, len(items))
	if sequential || len(items) < 2 {
		for i, item := range items {
			p, err := h.PlanAsset(item)
			if err != nil {
				return nil, fmt.Errorf("planning asset %q: %w", item.ID, err)
			}
			plans[i] = p
		}
		return plans, nil
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(min(runtime.NumCPU(), maxPlanWorkers))
	for i, item := range items {
		g.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			p, err := h.PlanAsset(item)
			if err != nil {
				return fmt.Errorf("planning asset %q: %w", item.ID, err)
			}
			// Written by index, never appended: completion order is not plan order, and the
			// planned list feeds execution directly.
			plans[i] = p
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return plans, nil
}

// installPackages walks the providers with non-empty lists, in the fixed order.
func (r *Restorer) installPackages(ctx context.Context, opts Options, resolved *profile.Resolved) (int, error) {
	cache := providers.NewCache(r.Paths.PackageCache, r.now)
	if opts.ForcePackages {
		if err := cache.InvalidateAll(); err != nil {
			r.log().Warn("invalidating package cache", "error", err)
		}
	}
	deps := providers.Deps{Runner: r.Runner, Cache: cache, Log: r.log(), Now: r.now}
	registry := providers.NewRegistry(deps, opts.RepoDir, r.Paths.Home)
	installOpts := providers.InstallOptions{DryRun: opts.DryRun, UseCache: !opts.ForcePackages}

	count := 0
	for _, entry := range registry.Ordered() {
		pkgs := resolved.Packages[entry.Group]
		if len(pkgs) == 0 {
			continue
		}
		if err := entry.Provider.Install(ctx, pkgs, installOpts); err != nil {
			return count, err
		}
		count += len(pkgs)
	}

	if len(resolved.DockerImages) > 0 {
		if err := registry.Docker().Install(ctx, resolved.DockerImages, installOpts); err != nil {
			return count, err
		}
		count += len(resolved.DockerImages)
	}
	if resolved.Node.Version != "" || resolved.Node.VersionFile != "" {
		if err := registry.Node().InstallVersion(ctx,
			resolved.Node.Version, resolved.Node.VersionFile, installOpts); err != nil {
			return count, err
		}
	}

	// The cache is an optimization; failing to persist it must not fail a successful restore.
	if err := cache.Save(); err != nil {
		r.log().Warn("saving package cache", "error", err)
	}
	return count, nil
}

// updateLockfile records the confirmed state. An unreadable existing lockfile is replaced with a
// warning rather than failing the run.
func (r *Restorer) updateLockfile(opts Options, resolved *profile.Resolved, plans []Plan, tx *transaction.Transaction) (string, error) {
	path := lockfile.PathFor(opts.ManifestPath)
	lf, err := lockfile.LoadOrEmpty(path)
	if err != nil {
		r.log().Warn("replacing unreadable lockfile", "path", path, "error", err)
	}

	byID := make(map[string]Plan, len(plans))
	for _, p := range plans {
		byID[p.AssetID] = p
	}

	record := func(id, source string, scalar bool, declared *string, secret bool) error {
		p, ok := byID[id]
		if !ok || p.Skipped {
			return nil
		}
		var (
			targets []string
			perms   []string
			mtimes  []float64
		)
		for _, target := range p.Targets {
			fi, err := os.Lstat(target)
			if err != nil {
				continue
			}
			targets = append(targets, target)
			mtimes = append(mtimes, float64(fi.ModTime().UnixNano())/float64(time.Second))
			switch {
			case declared != nil:
				perms = append(perms, *declared)
			case secret:
				perms = append(perms, secretMode)
			default:
				perms = append(perms, permissions.FormatManifest(fi.Mode().Perm()))
			}
		}
		if len(targets) == 0 {
			return nil
		}

		sum, err := lockfile.SHA256(source)
		if err != nil {
			return fmt.Errorf("hashing source for %q: %w", id, err)
		}
		entry := lockfile.Entry{SHA256OfSource: sum}
		if scalar {
			entry.TargetPath = manifest.Scalar(targets[0])
			entry.Permissions = manifest.Scalar(perms[0])
			entry.MTime = lockfile.ScalarFloat(mtimes[0])
		} else {
			entry.TargetPath = manifest.Slice(targets...)
			entry.Permissions = manifest.Slice(perms...)
			entry.MTime = lockfile.SliceFloat(mtimes...)
		}
		lf.Entries[id] = entry
		return nil
	}

	for _, id := range resolved.AssetIDs() {
		a := resolved.Assets[id]
		if err := record(id, r.sourcePath(opts, a.Source), a.Target.IsScalar(), a.Permissions, false); err != nil {
			return path, err
		}
	}
	for _, id := range resolved.SecretIDs() {
		s := resolved.Secrets[id]
		perms := s.Permissions
		if err := record(id, r.sourcePath(opts, s.Source), s.Target.IsScalar(), &perms, true); err != nil {
			return path, err
		}
	}

	for id, sum := range tx.RenderedChecksums {
		lf.RenderedChecksums[id] = sum
	}
	return path, lockfile.Save(path, lf)
}

func (r *Restorer) sourcePath(opts Options, source string) string {
	return r.Paths.Canonicalize(opts.RepoDir + string(os.PathSeparator) + source)
}

func (r *Restorer) runPlugins(ctx context.Context, opts Options, stage PluginStage, tx *transaction.Transaction) error {
	if r.Plugins == nil || opts.NoPlugins || opts.DryRun {
		return nil
	}
	hook := PluginHook{
		Stage:    stage,
		TxID:     tx.TxID,
		RepoDir:  opts.RepoDir,
		Profiles: opts.Profiles,
		Targets:  plannedTargets(tx),
	}
	if err := r.Plugins(ctx, hook); err != nil {
		return r.rollbackAfter(tx, 11, fmt.Errorf("%s plugin hook: %w", stage, err))
	}
	return nil
}

// plannedTargets lists every absolute path the transaction will mutate, deduplicated and in plan
// order. Hook operations share their asset's target, so they contribute nothing new.
func plannedTargets(tx *transaction.Transaction) []string {
	var out []string
	seen := map[string]bool{}
	for _, op := range tx.Planned {
		if op.Type == transaction.OpTypeHook || seen[op.Target] {
			continue
		}
		seen[op.Target] = true
		out = append(out, op.Target)
	}
	return out
}

// prune runs after a successful restore. Its failure is logged, never escalated: the restore
// already succeeded, and a leftover snapshot is harmless.
func (r *Restorer) prune(ctx context.Context) {
	if r.Prune == nil {
		return
	}
	if err := r.Prune(ctx); err != nil {
		r.log().Warn("pruning old backups failed; the restore itself succeeded", "error", err)
	}
}

// rollbackAfter rolls the file transaction back when a later step fails, and reports which
// hooks already ran.
func (r *Restorer) rollbackAfter(tx *transaction.Transaction, step int, cause error) error {
	err := fmt.Errorf("restore failed at step %d: %w", step, cause)
	if rbErr := tx.Rollback(); rbErr != nil {
		return fmt.Errorf("%w; rollback was incomplete: %w", err, rbErr)
	}
	return r.withHookReport(tx, fmt.Errorf("%w; the transaction was rolled back and your files are unchanged", err))
}

// withHookReport appends the hooks that already ran. Rollback restores files exactly; it cannot
// un-run `systemctl restart`, and this report is the user's only record of what survived.
func (r *Restorer) withHookReport(tx *transaction.Transaction, cause error) error {
	if len(tx.ExecutedHooks) == 0 {
		return cause
	}
	// This report is not a warning to be suppressed: rollback restores files exactly, but
	// `systemctl restart` has no inverse, and this is the user's only record of what survived.
	msg := "\nThese hooks already ran and were NOT reversed; review them if they had lasting effects:"
	for _, h := range tx.ExecutedHooks {
		msg += fmt.Sprintf("\n  - %s (%s): %v [%s]", h.AssetID, h.Stage, h.Command, h.Result)
	}
	//nolint:staticcheck // ST1005: this error is a multi-line user-facing report, not a clause
	// another error wraps.
	return fmt.Errorf("%w%s", cause, msg)
}

// logPlan reports what a dry run would do, including the hooks it is deliberately not running.
func (r *Restorer) logPlan(tx *transaction.Transaction) {
	for _, op := range tx.Planned {
		if op.Type == transaction.OpTypeHook {
			r.log().Info("dry run: would run hook",
				"asset_id", op.Hook.AssetID, "stage", op.Hook.Stage, "command", op.Hook.Command)
			continue
		}
		r.log().Info("dry run: would apply", "op", op.Type, "target", op.Target)
	}
}

// zeroPlans wipes decrypted plaintext once the operations carrying it are done.
func zeroPlans(plans []Plan) {
	for _, p := range plans {
		for _, op := range p.Ops {
			if b, ok := op.Source.(transaction.SourceBytes); ok {
				crypto.Zero(b.Data)
			}
		}
	}
}
