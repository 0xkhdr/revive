// Package platform detects the operating system, the Linux distribution, and which tools are
// installed. Results are cached for the process lifetime: doctor and every provider probe the
// same binaries, and repeated PATH lookups in a loop are wasted syscalls.
package platform

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Detector answers platform questions. The zero value works; tests substitute LookPath and
// OSRelease to describe a machine they are not running on.
type Detector struct {
	// LookPath resolves a binary. Nil means exec.LookPath.
	LookPath func(string) (string, error)
	// OSRelease is the path read for the distribution ID. Empty means /etc/os-release.
	OSRelease string
	// GOOS overrides the compiled-in operating system. Empty means runtime.GOOS.
	GOOS string

	mu    sync.Mutex
	tools map[string]bool
}

// Default is the process-wide detector.
var Default = &Detector{}

// OS returns the operating system: linux, darwin, and so on.
func (d *Detector) OS() string {
	if d.GOOS != "" {
		return d.GOOS
	}
	return runtime.GOOS
}

// IsLinux reports whether this is Linux.
func (d *Detector) IsLinux() bool { return d.OS() == "linux" }

// IsMacOS reports whether this is macOS.
func (d *Detector) IsMacOS() bool { return d.OS() == "darwin" }

// FindTool resolves a binary on PATH, caching the answer.
func (d *Detector) FindTool(name string) (string, bool) {
	lookPath := d.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.tools == nil {
		d.tools = map[string]bool{}
	}
	if found, ok := d.tools[name]; ok && !found {
		return "", false
	}
	path, err := lookPath(name)
	d.tools[name] = err == nil
	return path, err == nil
}

// HasTool reports whether a binary is on PATH.
func (d *Detector) HasTool(name string) bool {
	_, ok := d.FindTool(name)
	return ok
}

// Distro returns the lowercased ID from /etc/os-release, or "" on a non-Linux system or when the
// file is unreadable. Doctor uses it to tell the user which package managers make sense here.
func (d *Detector) Distro() string {
	if !d.IsLinux() {
		return ""
	}
	path := d.OSRelease
	if path == "" {
		path = "/etc/os-release"
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || key != "ID" {
			continue
		}
		return strings.ToLower(strings.Trim(value, `"'`))
	}
	return ""
}

// managerBinaries maps a manifest package group to the binaries that must be present.
var managerBinaries = map[string][]string{
	"brew":    {"brew"},
	"apt":     {"apt-get", "dpkg"},
	"flatpak": {"flatpak"},
	"snap":    {"snap"},
	"pacman":  {"pacman"},
	"dnf":     {"dnf"},
	"nix":     {"nix-env"},
	"cargo":   {"cargo"},
	"pip":     {"pip", "pip3"}, // either one is enough
	"docker":  {"docker"},
	"node":    {"node"},
}

// AvailablePackageManagers reports which package groups this machine can actually install.
func (d *Detector) AvailablePackageManagers() map[string]bool {
	out := make(map[string]bool, len(managerBinaries))
	for group := range managerBinaries {
		out[group] = d.HasManager(group)
	}
	return out
}

// HasManager reports whether one package group's tooling is present. apt needs both apt-get and
// dpkg; pip is satisfied by either pip or pip3.
func (d *Detector) HasManager(group string) bool {
	binaries, ok := managerBinaries[group]
	if !ok {
		return false
	}
	if group == "pip" {
		return d.HasTool("pip") || d.HasTool("pip3")
	}
	for _, binary := range binaries {
		if !d.HasTool(binary) {
			return false
		}
	}
	return true
}
