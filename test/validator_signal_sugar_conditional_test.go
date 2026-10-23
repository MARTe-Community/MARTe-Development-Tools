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

// Signal shorthand (`Name: Type = { ... }`) inside an object that is defined
// in a file sharing a package with other files (and therefore visited under
// a loop/with expansion) must still report its synthesized Type field.
func TestSignalShorthandTypeInSharedPackage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "signals.json"),
		[]byte(`{"AnalogInputSignals":[{"name":"AI0"}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	dev := `#package T.RTApp

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +AO = {
      Class = "NI6368::NI6368DAC"
      DeviceName = "/dev/pxie-6368"
      Signals = {
        BPS_VREF: float32 = {
          ChannelId = 0
          OutputRange = 10
        }
      }
    }
  }
}
`
	gen := `#package T.RTApp

with json("signals.json") as cfg begin
  +Data = {
    Class = ReferenceContainer
    foreach s in @cfg.AnalogInputSignals
      ("+DS_" .. @s.name) = {
        Class = GAMDataSource
        AllowNoProducers = 1
      }
    end
  }
end
`
	devFile := filepath.Join(dir, "dev.marte")
	genFile := filepath.Join(dir, "gen.marte")
	if err := os.WriteFile(devFile, []byte(dev), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(genFile, []byte(gen), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := parser.NewParser(dev).Parse()
	if err != nil {
		t.Fatalf("parse dev: %v", err)
	}
	gcfg, err := parser.NewParser(gen).Parse()
	if err != nil {
		t.Fatalf("parse gen: %v", err)
	}
	tree := index.NewProjectTree()
	tree.AddFile(devFile, cfg)
	tree.AddFile(genFile, gcfg)
	tree.ResolveReferences(nil)
	tree.ResolveFields(nil)

	v := validator.NewValidator(tree, dir, nil)
	v.MissingFilesAreWarnings = true
	v.ValidateProject(context.Background())

	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError && strings.Contains(d.Message, "BPS_VREF") {
			t.Errorf("false diagnostic for signal shorthand: %s", d.Message)
		}
		if d.Level == validator.LevelError && strings.Contains(d.Message, "must contain a 'Type'") {
			t.Errorf("signal shorthand lost its Type: %s", d.Message)
		}
	}
}
