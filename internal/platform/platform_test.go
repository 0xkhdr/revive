package platform

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOS(t *testing.T) {
	t.Parallel()
	linux := &Detector{GOOS: "linux"}
	require.Equal(t, "linux", linux.OS())
	require.True(t, linux.IsLinux())
	require.False(t, linux.IsMacOS())

	mac := &Detector{GOOS: "darwin"}
	require.True(t, mac.IsMacOS())
	require.False(t, mac.IsLinux())

	require.NotEmpty(t, (&Detector{}).OS(), "the zero value falls back to the compiled-in GOOS")
}

func TestFindToolCaches(t *testing.T) {
	t.Parallel()
	calls := 0
	d := &Detector{LookPath: func(name string) (string, error) {
		calls++
		if name == "present" {
			return "/usr/bin/present", nil
		}
		return "", os.ErrNotExist
	}}

	path, ok := d.FindTool("present")
	require.True(t, ok)
	require.Equal(t, "/usr/bin/present", path)
	require.True(t, d.HasTool("present"))

	_, ok = d.FindTool("absent")
	require.False(t, ok)
	before := calls
	require.False(t, d.HasTool("absent"))
	require.Equal(t, before, calls, "a negative answer is cached; doctor and every provider probe the same binaries")
}

func TestFindToolIsRaceFree(t *testing.T) {
	t.Parallel()
	d := &Detector{LookPath: func(string) (string, error) { return "/usr/bin/x", nil }}

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.HasTool(string(rune('a' + i%8)))
		}()
	}
	wg.Wait()
}

func TestDistro(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "os-release")
	require.NoError(t, os.WriteFile(path, []byte(
		"NAME=\"Ubuntu\"\nID=ubuntu\nVERSION_ID=\"24.04\"\n"), 0o644))

	d := &Detector{GOOS: "linux", OSRelease: path}
	require.Equal(t, "ubuntu", d.Distro())

	quoted := filepath.Join(t.TempDir(), "os-release")
	require.NoError(t, os.WriteFile(quoted, []byte("ID=\"Fedora\"\n"), 0o644))
	require.Equal(t, "fedora", (&Detector{GOOS: "linux", OSRelease: quoted}).Distro(),
		"the value is unquoted and lowercased")

	require.Empty(t, (&Detector{GOOS: "darwin", OSRelease: path}).Distro(),
		"there is no distribution on macOS")
	require.Empty(t, (&Detector{GOOS: "linux", OSRelease: filepath.Join(t.TempDir(), "absent")}).Distro())

	noID := filepath.Join(t.TempDir(), "os-release")
	require.NoError(t, os.WriteFile(noID, []byte("NAME=\"Something\"\n"), 0o644))
	require.Empty(t, (&Detector{GOOS: "linux", OSRelease: noID}).Distro())
}

func TestHasManager(t *testing.T) {
	t.Parallel()
	installed := func(names ...string) *Detector {
		set := map[string]bool{}
		for _, n := range names {
			set[n] = true
		}
		return &Detector{GOOS: "linux", LookPath: func(name string) (string, error) {
			if set[name] {
				return "/usr/bin/" + name, nil
			}
			return "", os.ErrNotExist
		}}
	}

	// apt needs both binaries: the install runs through one and the check through the other.
	require.True(t, installed("apt-get", "dpkg").HasManager("apt"))
	require.False(t, installed("apt-get").HasManager("apt"))
	require.False(t, installed("dpkg").HasManager("apt"))

	// pip is satisfied by either name.
	require.True(t, installed("pip").HasManager("pip"))
	require.True(t, installed("pip3").HasManager("pip"))
	require.False(t, installed().HasManager("pip"))

	require.True(t, installed("nix-env").HasManager("nix"))
	require.True(t, installed("brew").HasManager("brew"))
	require.False(t, installed().HasManager("brew"))
	require.False(t, installed("gem").HasManager("gem"), "an unknown group is never available")
}

func TestAvailablePackageManagers(t *testing.T) {
	t.Parallel()
	d := &Detector{GOOS: "linux", LookPath: func(name string) (string, error) {
		if name == "apt-get" || name == "dpkg" || name == "docker" {
			return "/usr/bin/" + name, nil
		}
		return "", os.ErrNotExist
	}}

	got := d.AvailablePackageManagers()
	require.True(t, got["apt"])
	require.True(t, got["docker"])
	require.False(t, got["brew"])
	require.False(t, got["pip"])
	require.Len(t, got, len(managerBinaries))
}

func TestDefaultDetectorWorks(t *testing.T) {
	t.Parallel()
	require.NotEmpty(t, Default.OS())
	require.True(t, Default.HasTool("sh"), "sh exists on every supported platform")
	require.False(t, Default.HasTool("rv-definitely-not-a-real-binary"))
}
