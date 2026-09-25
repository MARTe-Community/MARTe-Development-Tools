package integration

import (
	"runtime/debug"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/version"
)

// vcsSettings returns the toolchain's VCS stamping for the running binary.
func vcsSettings() map[string]string {
	out := map[string]string{}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return out
	}
	for _, s := range bi.Settings {
		out[s.Key] = s.Value
	}
	return out
}

// TestVersionGetUsesBuildInfo covers the reported bug: a plain `go build` (no
// -ldflags) reported "commit: none, build date: unknown" even though the
// toolchain had stamped the revision into the binary.
func TestVersionGetUsesBuildInfo(t *testing.T) {
	info := version.Get()
	if info.Version == "" {
		t.Error("version is empty")
	}
	if strings.TrimSpace(info.Commit) == "" {
		t.Error("commit is empty")
	}
	if strings.TrimSpace(info.Date) == "" {
		t.Error("build date is empty")
	}

	vcs := vcsSettings()
	rev, ok := vcs["vcs.revision"]
	if !ok || rev == "" {
		// Binary built without VCS stamping (e.g. from a tarball): the
		// documented fallback applies.
		if info.Commit != "none" || info.Date != "unknown" {
			t.Errorf("expected none/unknown fallback, got %q / %q", info.Commit, info.Date)
		}
		return
	}
	want := rev
	if len(want) > 12 {
		want = want[:12]
	}
	if info.Commit != want {
		t.Errorf("commit = %q, want the VCS revision %q", info.Commit, want)
	}
	if ts, ok := vcs["vcs.time"]; ok && info.Date != ts {
		t.Errorf("date = %q, want %q", info.Date, ts)
	}
}

// TestVersionGetPrefersInjectedValues pins the ldflags override path.
func TestVersionGetPrefersInjectedValues(t *testing.T) {
	prevV, prevC, prevD := version.Version, version.Commit, version.Date
	defer func() { version.Version, version.Commit, version.Date = prevV, prevC, prevD }()

	version.Version = "v9.9.9"
	version.Commit = "deadbeefcafe"
	version.Date = "2026-01-02T03:04:05Z"

	info := version.Get()
	if info.Version != "v9.9.9" || info.Commit != "deadbeefcafe" || info.Date != "2026-01-02T03:04:05Z" {
		t.Errorf("injected values not honoured: %+v", info)
	}
}
