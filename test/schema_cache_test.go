package integration

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cuelang.org/go/cue"

	"github.com/marte-community/marte-dev-tools/internal/schema"
)

// TestLoadFullSchemaCachesUntilFileChanges is a regression test for a
// performance bug where schema.LoadFullSchema (and therefore CUE schema
// parsing/compilation/unification) was re-executed from scratch on every
// single call, including from validator.NewValidator -- which the LSP calls
// on every debounced validation pass, i.e. on every keystroke. LoadFullSchema
// now caches its result per projectRoot and only reloads when the underlying
// schema file(s) actually change on disk (tracked via mtime).
func TestLoadFullSchemaCachesUntilFileChanges(t *testing.T) {
	tmpDir := t.TempDir()
	schemaPath := filepath.Join(tmpDir, ".marte_schema.cue")

	hasClass := func(s *schema.Schema, class string) bool {
		return s.Value.LookupPath(cue.ParsePath("#Classes." + class)).Exists()
	}

	// 1. No project schema file yet.
	s1 := schema.LoadFullSchema(tmpDir)
	// Repeated call with nothing on disk changed must hit the cache.
	s1b := schema.LoadFullSchema(tmpDir)
	if s1 != s1b {
		t.Fatal("Expected cached *Schema to be reused when nothing changed on disk")
	}

	// 2. Add a project schema defining ClassA, with an explicit mtime so this
	// is unambiguously "changed" relative to the prior (file-absent) state.
	writeSchema := func(content string, mtime time.Time) {
		if err := os.WriteFile(schemaPath, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		if err := os.Chtimes(schemaPath, mtime, mtime); err != nil {
			t.Fatalf("Chtimes failed: %v", err)
		}
	}

	t1 := time.Now().Add(-time.Hour)
	writeSchema(`
package schema

#Classes: {
	ClassA: {
		FieldA: int
		...
	}
}
`, t1)

	s2 := schema.LoadFullSchema(tmpDir)
	if s2 == s1 {
		t.Fatal("Expected a fresh *Schema after the project schema file was created")
	}
	if !hasClass(s2, "ClassA") {
		t.Fatal("Expected reloaded schema to contain ClassA")
	}

	// 3. Calling again with no changes must hit the cache (same pointer).
	s3 := schema.LoadFullSchema(tmpDir)
	if s3 != s2 {
		t.Fatal("Expected cached *Schema to be reused when the schema file did not change")
	}

	// 4. Change the file content with a distinctly later mtime -- must reload.
	t2 := t1.Add(time.Minute)
	writeSchema(`
package schema

#Classes: {
	ClassB: {
		FieldB: int
		...
	}
}
`, t2)

	s4 := schema.LoadFullSchema(tmpDir)
	if s4 == s3 {
		t.Fatal("Expected a fresh *Schema after the project schema file's content/mtime changed")
	}
	if !hasClass(s4, "ClassB") {
		t.Fatal("Expected reloaded schema to contain ClassB")
	}
	if hasClass(s4, "ClassA") {
		t.Fatal("Expected reloaded schema to no longer contain ClassA (file was overwritten, not merged)")
	}
}
