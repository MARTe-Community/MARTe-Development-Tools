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
	"github.com/marte-community/marte-dev-tools/internal/varsfile"
)

const structOverrideSrc = `#package T

var hardware: {
  NI6528_ID: uint8 = 1
  TriggerSignal: string = "DEFAULT"
}

var prefixes: {
  fast: string = "-GAF"
}

+App = {
  Class = ReferenceContainer
  +Data = {
    Class = ReferenceContainer
    Trigger = @hardware.TriggerSignal
    Fast = @prefixes.fast
  }
}
`

func loadOverrideTree(t *testing.T, src, jsonText string) (*index.ProjectTree, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "app.marte")
	jsonFile := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(file, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonFile, []byte(jsonText), 0644); err != nil {
		t.Fatal(err)
	}
	overrides, err := varsfile.LoadJSON(jsonFile)
	if err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tree := index.NewProjectTree()
	tree.AddFile(file, cfg)
	tree.ResolveReferences(nil)
	tree.ResolveFields(nil)
	return tree, overrides
}

// JSON overrides of structured variables (objects with string fields) must be
// applied as struct literals, not auto-quoted as strings: quoting truncated
// the value at the first inner quote and produced "value mismatch" errors.
func TestJSONStructOverride(t *testing.T) {
	tree, overrides := loadOverrideTree(t, structOverrideSrc, `{
  "hardware": {"NI6528_ID": 2, "TriggerSignal": "GY_MHV_V_MEAS_PSU"},
  "prefixes": {"fast": "-XFA"}
}`)

	v := validator.NewValidator(tree, ".", overrides)
	v.ValidateProject(context.Background())
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError && strings.Contains(d.Message, "value mismatch") {
			t.Errorf("struct override rejected: %s", d.Message)
		}
	}
	hw, ok := v.Variables["hardware"].(*parser.MapValue)
	if !ok {
		t.Fatalf("hardware override is %T, want *parser.MapValue", v.Variables["hardware"])
	}
	if got := hw.Values["TriggerSignal"]; got == nil {
		t.Fatalf("TriggerSignal missing from merged override: %+v", hw.Values)
	} else if sv, ok := got.(*parser.StringValue); !ok || sv.Value != "GY_MHV_V_MEAS_PSU" {
		t.Errorf("TriggerSignal = %#v, want the JSON string", got)
	}
}

// Lowering must see the same overrides as validation, so sugar inside
// conditions guarded by an overridden variable is materialized correctly.
func TestLowerWithOverrides(t *testing.T) {
	src := `#package T

var variant: "A" | "B" = "A"

+App = {
  Class = ReferenceContainer
  if (@variant == "B")
    +OnlyB = { Class = ReferenceContainer }
  end
}
`
	tree, overrides := loadOverrideTree(t, src, `{"variant": "B"}`)
	if _, err := builder.LowerWithOverrides(tree, overrides); err != nil {
		t.Fatalf("lower: %v", err)
	}
	found := false
	tree.Walk(func(n *index.ProjectNode) {
		if n.RealName == "+OnlyB" {
			found = true
		}
	})
	if !found {
		t.Error("copy/wiring sugar guarded by an overridden variable was not materialized")
	}
}
