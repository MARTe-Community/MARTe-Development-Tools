package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/loader"
)

// Sources often live in src/ below the project root that holds
// marte_project.json and .marte_schema.cue; the schema cascade must anchor
// at that root so project classes are recognised.
func TestProjectRootForMarker(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "repo", "proj")
	src := filepath.Join(project, "src", "thread1")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "marte_project.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(src, "app.marte")
	if err := os.WriteFile(file, []byte("#package T\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := loader.ProjectRootFor([]string{file}, filepath.Join(src))
	if got != project {
		t.Fatalf("ProjectRootFor = %q, want %q", got, project)
	}
	if got := loader.ProjectRootFor([]string{file}, ""); got != project {
		t.Errorf("fallback should not win over the marker: %q", got)
	}
}
