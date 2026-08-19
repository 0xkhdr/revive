package transaction

// Operation types.
const (
	OpTypeCopy    = "copy"
	OpTypeSymlink = "symlink"
	OpTypeChmod   = "chmod"
	OpTypeDelete  = "delete"
	OpTypeHook    = "hook"
)

// Source is what a copy operation copies: a path on disk, or literal bytes. It is a sum type,
// not `any`, so an unhandled case is a compile error rather than a runtime surprise.
type Source interface{ isSource() }

// SourcePath copies from a file or directory in the repo.
type SourcePath struct{ Path string }

// SourceBytes copies literal content: decrypted secrets and rendered templates. Zero the slice
// once the transaction is done with it.
type SourceBytes struct{ Data []byte }

func (SourcePath) isSource()  {}
func (SourceBytes) isSource() {}

// HookOp is a per-asset hook, already word-split at plan time so a quoting error fails before
// anything is snapshotted.
type HookOp struct {
	AssetID string
	Stage   string // pre | post
	Command []string
}

// Operation is one planned filesystem mutation, or one planned hook.
type Operation struct {
	Type        string
	Target      string // absolute; for a hook, the asset target it brackets
	Source      Source // nil for chmod, delete and hook
	Permissions string
	Owner       *string
	Hook        *HookOp // non-nil iff Type == OpTypeHook
}
