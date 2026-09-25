package e2e

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

// TestVersionCommand checks that `mdt version` reports a real build identity.
// Plain `go build`/`go install` used to print "commit: none, build date:
// unknown" because only -ldflags injection set those fields.
func TestVersionCommand(t *testing.T) {
	out, err := exec.Command(framework.GetMDTPath(), "version").CombinedOutput()
	if err != nil {
		t.Fatalf("mdt version failed: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^mdt \S+$`),
		regexp.MustCompile(`^commit: \S+$`),
		regexp.MustCompile(`^build date: \S+$`),
	}
	for i, p := range patterns {
		if !p.MatchString(lines[i]) {
			t.Errorf("line %d = %q, want match for %s", i+1, lines[i], p)
		}
	}
	// The binary under test is built from this repository by the test
	// framework/Makefile, so the toolchain stamps VCS information into it.
	if strings.Contains(string(out), "commit: none") || strings.Contains(string(out), "build date: unknown") {
		t.Errorf("build identity not reported:\n%s", out)
	}
}
