package providers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// fakeRunner records every command and answers from a scripted table, so every provider is
// testable without touching the system.
type fakeRunner struct {
	mu   sync.Mutex
	Ran  [][]string
	Path map[string]bool // binaries that "exist"
	// Out maps a joined command to its output; Fail maps one to an error.
	Out  map[string]string
	Fail map[string]error
	// FailTimes lets a command fail a fixed number of times before succeeding.
	FailTimes map[string]int
}

func newFakeRunner(available ...string) *fakeRunner {
	path := map[string]bool{}
	for _, name := range available {
		path[name] = true
	}
	return &fakeRunner{
		Path:      path,
		Out:       map[string]string{},
		Fail:      map[string]error{},
		FailTimes: map[string]int{},
	}
}

func (f *fakeRunner) Run(_ context.Context, cmd []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := strings.Join(cmd, " ")
	f.Ran = append(f.Ran, cmd)

	if n := f.FailTimes[key]; n > 0 {
		f.FailTimes[key] = n - 1
		return nil, fmt.Errorf("scripted transient failure: %s", key)
	}
	if err, ok := f.Fail[key]; ok {
		return nil, err
	}
	return []byte(f.Out[key]), nil
}

func (f *fakeRunner) LookPath(name string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Path[name] {
		return "/usr/bin/" + name, true
	}
	return "", false
}

// commands returns every command run, joined, for readable assertions.
func (f *fakeRunner) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.Ran))
	for i, cmd := range f.Ran {
		out[i] = strings.Join(cmd, " ")
	}
	return out
}

func (f *fakeRunner) ran(cmd string) bool {
	for _, got := range f.commands() {
		if got == cmd {
			return true
		}
	}
	return false
}

// testDeps wires a fake runner to a cache and a no-op sleep, so a retry test is instant.
func testDeps(r *fakeRunner, cachePath string) (Deps, *[]time.Duration) {
	slept := &[]time.Duration{}
	now := time.Unix(1700000000, 0)
	return Deps{
		Runner: r,
		Cache:  NewCache(cachePath, func() time.Time { return now }),
		Sleep:  func(d time.Duration) { *slept = append(*slept, d) },
		Now:    func() time.Time { return now },
	}, slept
}
