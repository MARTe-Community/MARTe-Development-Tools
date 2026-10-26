package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

// buildFiles writes the given sources into a temp dir (in order), builds them
// and returns the merged output.
func buildFiles(t *testing.T, sources ...string) string {
	t.Helper()
	dir := t.TempDir()
	files := make([]string, len(sources))
	for i, src := range sources {
		files[i] = filepath.Join(dir, "f"+string(rune('1'+i))+".marte")
		if err := os.WriteFile(files[i], []byte(src), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	out := filepath.Join(dir, "out.marte")
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if err := builder.NewBuilder(files, nil).Build(f); err != nil {
		t.Fatalf("build: %v", err)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(content)
}

func assertOrder(t *testing.T, content string, names ...string) {
	t.Helper()
	prev := -1
	prevName := ""
	for _, name := range names {
		idx := strings.Index(content, name+" = {")
		if idx < 0 {
			t.Fatalf("object %s missing from output:\n%s", name, content)
		}
		if idx < prev {
			t.Errorf("%s emitted before %s; expected source order %v:\n%s", name, prevName, names, content)
		}
		prev, prevName = idx, name
	}
}

// Top-level and nested conditional blocks are indexed as extra fragments of
// the enclosing node. The writer used to emit every definition of the base
// fragment first, pushing the conditional objects of the same file to the
// end; they must stay at their source position.
func TestBuildKeepsConditionalDefinitionOrder(t *testing.T) {
	src := `#package T

var web: bool = true
var debug: bool = true

if @web
  +WebRoot = { Class = A }
end

if @debug
  +DebugService = { Class = B }
end

+Main = {
  Class = C
  +First = { Class = D }
  #if @web
    +Nested = { Class = E }
  #end
  +Second = { Class = F }
}
`
	out := buildFiles(t, src)
	assertOrder(t, out, "+WebRoot", "+DebugService", "+Main", "+First", "+Nested", "+Second")
}

// Source order across files follows the file order handed to the builder,
// not the raw line number: a later file's first line must not jump ahead of
// an earlier file's later definition.
func TestBuildKeepsFileOrderAcrossFiles(t *testing.T) {
	first := `#package T

var unused: bool = true

+First = { Class = A }
`
	second := `#package T

+Second = { Class = B }
`
	out := buildFiles(t, first, second)
	assertOrder(t, out, "+First", "+Second")
}
