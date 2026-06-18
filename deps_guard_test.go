package main

import (
	"os/exec"
	"strings"
	"testing"
)

// forbiddenDeps lists module/package import paths that must never re-enter the
// compiled dependency graph.
//
// github.com/shoenig/go-m1cpu has a cgo init() that calls IOKit and segfaults
// at process start on some Apple Silicon machines — before main() runs, so it
// cannot be recovered (see PR #129). It is pulled in transitively by
// github.com/shirou/gopsutil/v3. We migrated to gopsutil/v4, which dropped the
// dependency. This guard fails the build if a future change reintroduces either,
// turning a startup-crash regression into a deterministic, cross-platform test
// failure instead of a machine-specific SIGSEGV discovered at runtime.
var forbiddenDeps = []string{
	"github.com/shoenig/go-m1cpu",
	"github.com/shirou/gopsutil/v3",
}

// TestNoForbiddenDependencies asserts that no banned package is actually compiled
// into the module. It inspects `go list -deps ./...` (the real compiled set,
// not just go.mod) so an indirect reintroduction is caught too.
func TestNoForbiddenDependencies(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./...").Output()
	if err != nil {
		t.Fatalf("go list -deps ./... failed: %v", err)
	}

	deps := string(out)
	for _, banned := range forbiddenDeps {
		for _, line := range strings.Split(deps, "\n") {
			if line == banned || strings.HasPrefix(line, banned+"/") {
				t.Errorf("forbidden dependency %q is in the compiled graph (matched %q); "+
					"it must not be reintroduced — see deps_guard_test.go for why", banned, line)
				break
			}
		}
	}
}
