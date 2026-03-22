package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

func TestFixtures(t *testing.T) {
	// Find project root by looking for go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("Could not find project root")
		}
		dir = parent
	}

	fixturePath := filepath.Join(dir, "test", "e2e", "fixtures")
	framework.RunFixtureTests(t, fixturePath)
}
