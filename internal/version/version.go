// Package version reports the mdt build identity.
//
// Values may be injected at link time:
//
//	go build -ldflags "-X github.com/.../internal/version.Version=v1.2.3 ..."
//
// When they are not, the Go build info recorded by the toolchain (VCS
// revision and commit time) is used instead, so a plain `go build` or
// `go install` still reports a meaningful commit and date.
package version

import (
	"runtime/debug"
	"strings"
)

var (
	// Version is the semantic version, injected via ldflags.
	Version = "v0.1.0"
	// Commit is the VCS revision, injected via ldflags.
	Commit = ""
	// Date is the build timestamp (RFC3339), injected via ldflags.
	Date = ""
)

// Info is the resolved build identity.
type Info struct {
	Version string
	Commit  string
	Date    string
}

// Get resolves the build identity, preferring ldflags values and
// falling back to the toolchain's VCS stamping.
func Get() Info {
	info := Info{Version: Version, Commit: Commit, Date: Date}
	if info.Version == "" {
		info.Version = "dev"
	}

	bi, ok := debug.ReadBuildInfo()
	if ok {
		if info.Version == "v0.1.0" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = shortRevision(s.Value)
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			}
		}
	}

	if info.Commit == "" {
		info.Commit = "none"
	}
	if info.Date == "" {
		info.Date = "unknown"
	}
	return info
}

func shortRevision(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return strings.TrimSpace(rev)
}
