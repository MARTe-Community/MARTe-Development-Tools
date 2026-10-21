package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

// buildProjectDir builds every .marte file under dir (one directory level of
// subprojects, like the CLI's -P) and returns the output.
func buildProjectDir(t *testing.T, dir string) string {
	t.Helper()
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			sub, err := os.ReadDir(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			for _, f := range sub {
				if filepath.Ext(f.Name()) == ".marte" {
					files = append(files, filepath.Join(dir, e.Name(), f.Name()))
				}
			}
			continue
		}
		if filepath.Ext(e.Name()) == ".marte" {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	b := builder.NewBuilder(files, nil)
	out := filepath.Join(t.TempDir(), "out.marte")
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := b.Build(f); err != nil {
		f.Close()
		t.Fatalf("build %s: %v", dir, err)
	}
	f.Close()
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(content)
}

// The refactored big project (copy groups instead of simple IOGAMs) must
// produce exactly the same MARTe configuration as the original.
func TestBigProjectRefactorEquivalence(t *testing.T) {
	orig := buildProjectDir(t, "../examples/big_project")
	refac := buildProjectDir(t, "../examples/big_project_sugar")
	if orig != refac {
		// Report the first differing line to keep failures readable.
		origLines := splitLines(orig)
		refLines := splitLines(refac)
		for i := 0; i < len(origLines) && i < len(refLines); i++ {
			if origLines[i] != refLines[i] {
				t.Fatalf("outputs differ at line %d:\n original: %q\nrefactored: %q", i+1, origLines[i], refLines[i])
			}
		}
		t.Fatalf("outputs differ in length: original %d lines, refactored %d lines", len(origLines), len(refLines))
	}
	if len(orig) == 0 {
		t.Fatal("empty output")
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}
