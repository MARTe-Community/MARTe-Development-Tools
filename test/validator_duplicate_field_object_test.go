package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// A duplicate field must name the owning object, so errors inside
// data-driven generated objects can be traced to the responsible node.
func TestDuplicateFieldNamesObject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cfg.json"), []byte(
		`{"signals": [{"name": "AI0"}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	src := `#package T

+App = {
  Class = ReferenceContainer
  +Data = {
    Class = ReferenceContainer
    with json("cfg.json") as cfg begin
      foreach s in @cfg.signals
        ("+DS_" .. @s.name) = {
          Class = GAMDataSource
          Signals = {
            AI0 = { Type = float32 }
            AI0 = { Type = float64 }
          }
        }
      end
    end
  }
}
`
	file := filepath.Join(dir, "main.marte")
	if err := os.WriteFile(file, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tree := index.NewProjectTree()
	tree.AddFile(file, cfg)
	tree.ResolveReferences(nil)

	v := validator.NewValidator(tree, dir, nil)
	v.ValidateProject(context.Background())

	var msg string
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Duplicate Field Definition") {
			msg = d.Message
			break
		}
	}
	if msg == "" {
		t.Fatal("expected a duplicate field diagnostic")
	}
	// The generated object name and the signal containing the field are
	// both part of the message.
	for _, want := range []string{"'Type'", "+DS_AI0", "Signals/AI0", "main.marte"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic %q does not mention %q", msg, want)
		}
	}
}
