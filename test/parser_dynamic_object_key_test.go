package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

// TestDynamicObjectKeyInFieldSubnode pins `@expr = { … }` as an object
// definition with a dynamic name inside a field's subnode. isSubnodeLookahead
// used to classify `{ @a … }` as a value block, so the parser reported
// "unexpected value token =" / "expected =, got }" for exactly this shape.
func TestDynamicObjectKeyInFieldSubnode(t *testing.T) {
	src := `#package T

var a: { b: string = "Node" }

+App = {
  Class = ReferenceContainer
  Container = {
    @a.b = {
      X = 1
    }
  }
}
`
	out := buildTemp(t, src)
	if !strings.Contains(out, "Node = {") {
		t.Errorf("dynamic object key did not materialize, output:\n%s", out)
	}
}

// TestForeachDynamicSignalNamesFromJSON reproduces the reported config shape:
// `with json(...) as x begin foreach s in @x.list ... @s.name = { ... } end end`
// inside a `Signals` block.
func TestForeachDynamicSignalNamesFromJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "signals.json")
	if err := os.WriteFile(jsonPath, []byte(`{"AnalogInputSignals":[{"name":"ADC1","channel":1},{"name":"ADC2","channel":2}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	src := `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DS
    with json("signals.json") as signals begin
      +DS = {
        Class = GAMDataSource
        Signals = {
          foreach signal in @signals.AnalogInputSignals
            @signal.name = {
              Type = float32
              ChannelId = @signal.channel
            }
          end
        }
      }
    end
  }
  +Functions = { Class = ReferenceContainer }
  Scheduler = { Class = GAMScheduler  TimingDataSource = DS }
  +States = { Class = ReferenceContainer }
}
`
	file := filepath.Join(dir, "app.marte")
	if err := os.WriteFile(file, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	out := buildTempWithDir(t, dir, file)
	for _, want := range []string{"ADC1 = {", "ADC2 = {", "ChannelId = 1", "ChannelId = 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("build output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "= {") && strings.Contains(out, "\n  = {") {
		t.Errorf("build output contains a nameless object:\n%s", out)
	}
}

// buildTempWithDir builds file inside dir (so relative `with json` paths
// resolve) and returns the generated output.
func buildTempWithDir(t *testing.T, dir, file string) string {
	t.Helper()
	out, err := os.CreateTemp("", "out*.marte")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(out.Name())

	b := builder.NewBuilder([]string{file}, nil)
	if err := b.Build(out); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
