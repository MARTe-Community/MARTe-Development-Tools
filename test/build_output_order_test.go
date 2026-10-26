package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Signals declared outside a `with`/`foreach` block must be written first,
// followed by the generated ones in iteration (JSON) order. Map iteration or
// fragment creation order used to scramble both.
func TestBuildSignalOrderStaticsThenIterations(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sigs.json"), []byte(`{
  "Signals": [{"name": "S0"}, {"name": "S1"}, {"name": "S2"}]
}`), 0644); err != nil {
		t.Fatal(err)
	}
	src := `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB
    +DDB = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        StaticA = { Type = uint8 }
        StaticB = { Type = uint8 }
      }
    }
    with json("sigs.json") as cfg begin
      +DDB = {
        Signals = {
          foreach s in @cfg.Signals
            ("G_" .. @s.name) = { Type = float32 }
          end
        }
      }
    end
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = DDB }
  +States = { Class = ReferenceContainer }
}
`
	file := filepath.Join(dir, "app.marte")
	if err := os.WriteFile(file, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	out := buildTempWithDir(t, dir, file)

	block := outputBlock(t, out, "+DDB")
	var order []string
	for _, m := range regexp.MustCompile(`(?m)^        ([A-Za-z0-9_]+) = \{$`).FindAllStringSubmatch(block, -1) {
		order = append(order, m[1])
	}
	want := []string{"StaticA", "StaticB", "G_S0", "G_S1", "G_S2"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("signal order = %v, want %v\n%s", order, want, block)
	}
}

// outputBlock returns the text of the first `name = { ... }` block.
func outputBlock(t *testing.T, text, name string) string {
	t.Helper()
	start := strings.Index(text, name+" = {")
	if start < 0 {
		t.Fatalf("%s not found in output:\n%s", name, text)
	}
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	t.Fatalf("unterminated block %s", name)
	return ""
}
