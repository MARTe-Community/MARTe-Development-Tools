package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/logger"
)

func writeTreeFile(t *testing.T, root, rel, src string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func objectFile(name string) string {
	return "#package p\n+" + name + " = { Class = ReferenceContainer }\n"
}

// TestScanDirectorySkipsNonProjectTrees covers what the workspace scan must
// ignore: dependency trees, package caches and build outputs. Indexing them is
// pure cost, and a home directory (millions of entries) made initialize take
// minutes.
func TestScanDirectorySkipsNonProjectTrees(t *testing.T) {
	root := t.TempDir()
	writeTreeFile(t, root, "app.marte", objectFile("App"))
	writeTreeFile(t, root, "src/inner.marte", objectFile("Inner"))
	writeTreeFile(t, root, "node_modules/dep.marte", objectFile("Dep"))
	writeTreeFile(t, root, "vendor/lib.marte", objectFile("Vendored"))
	writeTreeFile(t, root, "target/gen.marte", objectFile("Generated"))
	writeTreeFile(t, root, ".cache/cached.marte", objectFile("Cached"))
	writeTreeFile(t, root, ".config/proj.marte", objectFile("Hidden"))

	pt := index.NewProjectTree()
	if err := pt.ScanDirectory(root); err != nil {
		t.Fatalf("scan: %v", err)
	}

	for _, want := range []string{"+App", "+Inner"} {
		if findRealName(pt, want) == nil {
			t.Errorf("expected %s to be indexed", want)
		}
	}
	for _, unwanted := range []string{"+Dep", "+Vendored", "+Generated", "+Cached", "+Hidden"} {
		if n := findRealName(pt, unwanted); n != nil {
			t.Errorf("%s should not be indexed (found %q)", unwanted, n.RealName)
		}
	}
}

// TestScanDirectoryAcceptsHiddenRoot documents the exception: pointing the
// workspace at a dot-directory is legitimate, so the root itself is entered
// even though hidden directories below it are skipped.
func TestScanDirectoryAcceptsHiddenRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".config", "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTreeFile(t, root, "app.marte", objectFile("App"))
	writeTreeFile(t, root, "nested/other.marte", objectFile("Other"))

	pt := index.NewProjectTree()
	if err := pt.ScanDirectory(root); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if findRealName(pt, "+App") == nil || findRealName(pt, "+Other") == nil {
		t.Error("a hidden workspace root must still be indexed")
	}
}

// TestScanDirectoryRespectsEntryLimit pins the safety valve: a workspace with
// millions of entries must not hang the server, and the truncation must be
// reported instead of silently indexing a partial tree.
func TestScanDirectoryRespectsEntryLimit(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		writeTreeFile(t, root, filepath.Join("mod", filePathForIndex(i)), objectFile("App"))
	}

	t.Setenv("MDT_MAX_SCAN_ENTRIES", "5")
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stderr)

	pt := index.NewProjectTree()
	if err := pt.ScanDirectory(root); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !strings.Contains(buf.String(), "stopped after 5 entries") {
		t.Errorf("expected a truncation warning, got %q", buf.String())
	}
}
