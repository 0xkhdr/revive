package profile

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/manifest"
)

func load(t *testing.T, yaml string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(yaml))
	require.NoError(t, err)
	return m
}

const fixture = `
version: 2
assets:
  - id: zshrc
    source: assets/zshrc
    target: /tmp/zshrc
  - id: zshrc_work
    source: assets/zshrc_work
    target: /tmp/zshrc
  - id: gitconfig
    source: assets/gitconfig
    target: /tmp/gitconfig
  - id: compose
    source: assets/compose
    target: /tmp/compose
secrets:
  - id: app_env
    source: secrets/app_env
    target: /tmp/.env
packages:
  apt: [git, zsh]
  brew: [jq]
  docker: { images: [redis:7] }
  node: { version_file: .nvmrc }
profiles:
  base:
    assets: [zshrc, gitconfig]
    packages: [apt]
  work:
    extends: [base]
    assets: [compose]
    secrets: [app_env]
    packages: [apt, brew, docker, node]
  override:
    extends: [base]
    assets:
      - id: zshrc
        source: assets/zshrc_work
        target: /tmp/zshrc
`

// Phase 3: `work extends base` yields base's assets plus work's.
func TestInheritance(t *testing.T) {
	t.Parallel()
	r, err := Resolve(load(t, fixture), "work")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"zshrc", "gitconfig", "compose"}, r.AssetIDs())
	require.Equal(t, []string{"app_env"}, r.SecretIDs())
	require.Equal(t, []string{"git", "zsh"}, r.Packages["apt"], "a group pulled in twice is deduplicated")
	require.Equal(t, []string{"jq"}, r.Packages["brew"])
	require.Equal(t, []string{"redis:7"}, r.DockerImages)
	require.Equal(t, ".nvmrc", r.Node.VersionFile)

	// Parents resolve first, so the base assets come first in the plan.
	require.Equal(t, []string{"zshrc", "gitconfig", "compose"}, r.AssetIDs())
}

// Phase 3: a child's asset with the same ID as a parent's overrides it.
func TestChildOverridesParentByID(t *testing.T) {
	t.Parallel()
	r, err := Resolve(load(t, fixture), "override")
	require.NoError(t, err)
	require.Equal(t, "assets/zshrc_work", r.Assets["zshrc"].Source)
	require.Equal(t, []string{"zshrc", "gitconfig"}, r.AssetIDs(),
		"an override keeps the parent's position rather than re-appending")
}

// Phase 3: a cycle errors with the full chain in the message.
func TestCycleDetection(t *testing.T) {
	t.Parallel()
	m := load(t, `
version: 2
profiles:
  a: { extends: [b] }
  b: { extends: [a] }
`)
	_, err := Resolve(m, "a")
	require.ErrorIs(t, err, ErrCycle)
	require.Contains(t, err.Error(), "a -> b -> a")

	m = load(t, "version: 2\nprofiles:\n  self: { extends: [self] }\n")
	_, err = Resolve(m, "self")
	require.ErrorIs(t, err, ErrCycle)
	require.Contains(t, err.Error(), "self -> self")
}

// Phase 3: a diamond resolves without a false cycle error — `visited` is copied per branch.
func TestDiamondIsNotACycle(t *testing.T) {
	t.Parallel()
	m := load(t, `
version: 2
assets:
  - { id: shared, source: assets/shared, target: /tmp/shared }
  - { id: left,   source: assets/left,   target: /tmp/left }
  - { id: right,  source: assets/right,  target: /tmp/right }
profiles:
  base: { assets: [shared] }
  a: { extends: [base], assets: [left] }
  b: { extends: [base], assets: [right] }
  c: { extends: [a, b] }
`)
	r, err := Resolve(m, "c")
	require.NoError(t, err)
	require.Equal(t, []string{"shared", "left", "right"}, r.AssetIDs())
}

// Phase 3: `base,work` merges both, last-write-wins on assets, deduplicated append on packages.
func TestMultiProfileMerge(t *testing.T) {
	t.Parallel()
	m := load(t, fixture)
	r, err := Resolve(m, "base,work")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"zshrc", "gitconfig", "compose"}, r.AssetIDs())
	require.Equal(t, []string{"git", "zsh"}, r.Packages["apt"])

	// `-p a -p b` and `-p a,b` are the same thing.
	repeated, err := Resolve(m, "base", "work")
	require.NoError(t, err)
	require.Equal(t, r.AssetIDs(), repeated.AssetIDs())
	require.Equal(t, r.Packages, repeated.Packages)

	// Last-write-wins across the merge.
	r, err = Resolve(m, "base,override")
	require.NoError(t, err)
	require.Equal(t, "assets/zshrc_work", r.Assets["zshrc"].Source)
	r, err = Resolve(m, "override,base")
	require.NoError(t, err)
	require.Equal(t, "assets/zshrc", r.Assets["zshrc"].Source)
}

func TestParseNames(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"base", "work"}, ParseNames("base,work"))
	require.Equal(t, []string{"base", "work"}, ParseNames("base", "work"))
	require.Equal(t, []string{"base", "work"}, ParseNames(" base , work "))
	require.Equal(t, []string{"base"}, ParseNames("base", "base"))
	require.Empty(t, ParseNames("", " , "))
}

// Phase 3: an unknown asset ID errors, naming both the ID and the profile.
func TestUnknownReferences(t *testing.T) {
	t.Parallel()
	m := load(t, "version: 2\nprofiles:\n  base: { assets: [ghost] }\n")
	_, err := Resolve(m, "base")
	require.ErrorIs(t, err, ErrNotFound)
	require.Contains(t, err.Error(), "ghost")
	require.Contains(t, err.Error(), "base")

	m = load(t, "version: 2\nprofiles:\n  base: { secrets: [ghost] }\n")
	_, err = Resolve(m, "base")
	require.ErrorIs(t, err, ErrNotFound)
	require.Contains(t, err.Error(), "ghost")

	m = load(t, "version: 2\nprofiles:\n  base: {}\n")
	_, err = Resolve(m, "nope")
	require.ErrorIs(t, err, ErrNotFound)
	require.Contains(t, err.Error(), "nope")

	_, err = Resolve(m)
	require.ErrorIs(t, err, ErrNotFound)

	m = load(t, "version: 2\nprofiles:\n  base: { extends: [ghost] }\n")
	_, err = Resolve(m, "base")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestInlineDefinitions(t *testing.T) {
	t.Parallel()
	m := load(t, `
version: 2
profiles:
  base:
    assets:
      - { id: inline, source: assets/inline, target: /tmp/inline }
    secrets:
      - { id: inline_secret, source: secrets/s.age, target: /tmp/s }
`)
	r, err := Resolve(m, "base")
	require.NoError(t, err)
	require.Equal(t, "assets/inline", r.Assets["inline"].Source)
	require.Equal(t, "0600", r.Secrets["inline_secret"].Permissions)
}

func TestResolvedHelpers(t *testing.T) {
	t.Parallel()
	r := New()
	r.AddPackages("apt", "git", "git", "zsh")
	require.Equal(t, []string{"git", "zsh"}, r.Packages["apt"])
	r.AddDockerImages("redis:7", "redis:7")
	require.Equal(t, []string{"redis:7"}, r.DockerImages)
	for _, name := range manifest.ListNames {
		require.NotNil(t, r.Packages[name], name)
	}
}

// A node version_file set by one profile survives a merge with a profile that sets neither.
func TestMergeKeepsNodeSettings(t *testing.T) {
	t.Parallel()
	m := load(t, `
version: 2
packages:
  node: { version_file: .nvmrc, version: "20.11.0" }
profiles:
  plain: {}
  withnode: { packages: [node] }
`)
	r, err := Resolve(m, "withnode,plain")
	require.NoError(t, err)
	require.Equal(t, ".nvmrc", r.Node.VersionFile)
	require.Equal(t, "20.11.0", r.Node.Version)
}
