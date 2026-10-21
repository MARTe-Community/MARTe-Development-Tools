package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

// Two independent `#if` blocks in the same node must both be emitted when
// their conditions hold. The builder used to deactivate every conditional
// fragment but the last one processed, dropping whole branches.
func TestMultipleIfBlocksBothActive(t *testing.T) {
	src := `#package T

var on: bool = true

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +TDS = { Class = TimingDataSource }
    +D = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = { X = { Type = float32 }  Y = { Type = float32 } }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    #if @on
      +First = {
        Class = FilterGAM
        InputSignals = { X = { DataSource = D } }
        OutputSignals = { Y = { DataSource = D } }
      }
    #end
    #if @on
      +Second = {
        Class = FilterGAM
        InputSignals = { X = { DataSource = D } }
        OutputSignals = { Y = { DataSource = D } }
      }
    #end
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = TDS }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = { Class = RealTimeThread  CPUs = 0x1  Functions = { First Second } }
    }
  }
}
`
	dir := t.TempDir()
	file := filepath.Join(dir, "main.marte")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	b := builder.NewBuilder([]string{file}, nil)
	out := filepath.Join(dir, "out.marte")
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := b.Build(f); err != nil {
		f.Close()
		t.Fatalf("build: %v", err)
	}
	f.Close()
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{"+First = {", "+Second = {"} {
		if !strings.Contains(string(content), want) {
			t.Errorf("missing %q: a conditional branch was dropped\n%s", want, content)
		}
	}
}
