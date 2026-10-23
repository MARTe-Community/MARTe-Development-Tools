package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

const aliasSrc = `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB3
    +DDB3 = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = { X = { Type = uint32 } }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +G = {
      Class = IOGAM
      OutputSignals = { DDB3::X: uint32 as Y }
    }
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = DDB3 }
  +States = { Class = ReferenceContainer }
}
`

// `DDB3::X as Y` means: the GAM-local signal is Y and it aliases the
// DataSource signal X. The build must emit
//
//	Y = { DataSource = DDB3  Alias = X }
//
// and validation must not treat the alias value as a global reference.
func TestAliasDirectionAndResolution(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.marte")
	if err := os.WriteFile(file, []byte(aliasSrc), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := os.CreateTemp("", "alias-out*.marte")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(out.Name())
	if err := builder.NewBuilder([]string{file}, nil).Build(out); err != nil {
		t.Fatalf("build: %v", err)
	}
	built, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	text := string(built)
	if !strings.Contains(text, "Y = {") || !strings.Contains(text, "Alias = X") || !strings.Contains(text, "DataSource = DDB3") {
		t.Fatalf("alias not emitted as Y={DataSource=DDB3, Alias=X}:\n%s", text)
	}

	// Validating the built configuration must not report the alias as an
	// unknown reference.
	cfg, err := parser.NewParser(text).Parse()
	if err != nil {
		t.Fatalf("re-parse built config: %v", err)
	}
	tree := index.NewProjectTree()
	tree.AddFile("built.marte", cfg)
	tree.ResolveReferences(nil)
	tree.ResolveFields(nil)
	v := validator.NewValidator(tree, dir, nil)
	v.MissingFilesAreWarnings = true
	v.ValidateProject(context.Background())
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError && (strings.Contains(d.Message, "Alias") || strings.Contains(d.Message, "Unknown reference")) {
			t.Errorf("alias mis-resolved in built config: %s", d.Message)
		}
	}
}
