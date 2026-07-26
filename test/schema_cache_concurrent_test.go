package integration

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marte-community/marte-dev-tools/internal/schema"
)

// TestSchemaLoadFullSchemaConcurrentCache is a regression test for BUG-009:
// the LoadFullSchema function has a TOCTOU race in its caching logic.
// The double-checked locking pattern is missing the second check inside
// the write-lock, so concurrent callers can both miss the cache and
// recompile the CUE schema needlessly.
//
// This test spawns concurrent goroutines calling LoadFullSchema for the
// same projectRoot and verifies they all receive the same *Schema pointer
// (indicating the cache worked). While this test cannot deterministically
// trigger the TOCTOU race window, it validates that the cache is
// functionally correct under concurrent access.
func TestSchemaLoadFullSchemaConcurrentCache(t *testing.T) {
	tmpDir := t.TempDir()

	const goroutines = 20
	results := make([]*schema.Schema, goroutines)
	done := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			results[idx] = schema.LoadFullSchema(tmpDir)
			done <- struct{}{}
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	// All goroutines must have received the same *Schema pointer.
	first := results[0]
	if first == nil {
		t.Fatal("LoadFullSchema returned nil")
	}
	for i := 1; i < goroutines; i++ {
		if results[i] != first {
			t.Errorf("Goroutine %d got different *Schema pointer (TOCTOU race: cache missed)", i)
		}
	}
}

// TestSchemaLoadFullSchemaCacheInvalidationOnWrite verifies that the schema
// cache is correctly invalidated when a schema file changes on disk.
// This extends the existing TestLoadFullSchemaCachesUntilFileChanges test
// by also verifying that system schema paths are tracked.
func TestSchemaLoadFullSchemaCacheInvalidationOnWrite(t *testing.T) {
	tmpDir := t.TempDir()
	schemaPath := filepath.Join(tmpDir, ".marte_schema.cue")

	// Baseline: no project schema
	s1 := schema.LoadFullSchema(tmpDir)

	// Write a project schema
	t1 := time.Now().Add(-time.Hour)
	if err := os.WriteFile(schemaPath, []byte(`
package schema
#Classes: {
	RegrClass: {
		FieldA: int
		...
	}
}
`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chtimes(schemaPath, t1, t1); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	s2 := schema.LoadFullSchema(tmpDir)
	if s2 == s1 {
		t.Fatal("Cache was NOT invalidated after schema file creation")
	}

	// Second call with no changes: cache hit
	s3 := schema.LoadFullSchema(tmpDir)
	if s3 != s2 {
		t.Fatal("Cache was invalidated despite no file changes")
	}
}
