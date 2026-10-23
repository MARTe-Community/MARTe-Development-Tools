package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// dynamicCopyFixture is the reported shape: a copy group inside a
// with/foreach scope whose endpoints name the signal through the loop
// variable.
const dynamicCopyFixture = `#package T

var samples_adc: uint32 = 100

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB4
    +FastAdcDataSource = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        AI0 = { Type = float32 }
        AI1 = { Type = float32 }
      }
    }
    +DDB4 = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        AI0 = { Type = float32 }
        AI1 = { Type = float32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Gen = {
      Class = ConstantGAM
      OutputSignals = {
        AI0 = { DataSource = FastAdcDataSource  Type = float32 }
        AI1 = { DataSource = FastAdcDataSource  Type = float32 }
      }
    }
    with json("signals.json") as signals begin
      foreach signal in @signals.AnalogInputSignals
        copy ReadADC {
          FastAdcDataSource::@signal.name { Samples = @samples_adc } -> DDB4::@signal.name { Samples = @samples_adc }
        }
      end
    end
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = DDB4 }
  +States = {
    Class = ReferenceContainer
    +Run = { Class = RealTimeState  +Th = { Class = RealTimeThread  CPUs = 0x1  Functions = { Gen ReadADC } } }
  }
}
`

const dynamicCopyJSON = `{"AnalogInputSignals":[{"name":"AI0","channel":1},{"name":"AI1","channel":4}]}`

// TestDynamicCopyEndpoints covers `DS::@var.field` endpoints inside a
// with/foreach scope: every iteration folds into one IOGAM, with the option
// values evaluated in the loop scope.
func TestDynamicCopyEndpoints(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/signals.json", []byte(dynamicCopyJSON), 0644); err != nil {
		t.Fatal(err)
	}
	file := dir + "/app.marte"
	if err := os.WriteFile(file, []byte(dynamicCopyFixture), 0644); err != nil {
		t.Fatal(err)
	}

	// builder.Lower is the path `mdt check` uses: no prior activation.
	cfg, err := parser.NewParser(dynamicCopyFixture).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tree := index.NewProjectTree()
	tree.AddFile(file, cfg)
	tree.ResolveReferences(nil)
	tree.ResolveFields(nil)
	rep, err := builder.Lower(tree)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	if rep.Copies != 2 {
		t.Errorf("lowered %d copies, want 2", rep.Copies)
	}

	// One concrete group per source group, carrying every iteration.
	out := buildTempWithDir(t, dir, file)
	if n := strings.Count(out, "+ReadADC = {"); n != 1 {
		t.Fatalf("expected exactly one ReadADC IOGAM, found %d:\n%s", n, out)
	}
	for _, want := range []string{
		"FastAdcDataSource\n          Samples = 100", // AI0 input
		"AI1 = {", // both signals present
		"DataSource = DDB4",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("build output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "@signal") {
		t.Errorf("dynamic name leaked into the output:\n%s", out)
	}
}

// TestDynamicCopyEndpointsSingleScope: without a loop the name is evaluated
// once from the surrounding variables.
func TestDynamicCopyEndpointsSingleScope(t *testing.T) {
	src := `#package T

var target: string = "AI0"

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = SRC
    +SRC = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = { AI0 = { Type = float32 } }
    }
    +DST = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = { AI0 = { Type = float32 } }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    copy One {
      SRC::@target -> DST::@target
    }
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = SRC }
  +States = { Class = ReferenceContainer }
}
`
	out := buildTemp(t, src)
	if !strings.Contains(out, "AI0 = {") || strings.Contains(out, "@target") {
		t.Errorf("dynamic endpoint did not resolve:\n%s", out)
	}
}

// TestDynamicCopyEndpointsFormatRoundTrip keeps the sugar when formatting.
func TestDynamicCopyEndpointsFormatRoundTrip(t *testing.T) {
	src := `#package T
+App = {
  Class = ReferenceContainer
  copy ReadADC {
    SRC::@signal.name { Samples = @n } -> DST::@signal.name
  }
}
`
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var sb strings.Builder
	formatter.Format(cfg, &sb)
	out := sb.String()
	if !strings.Contains(out, "SRC::@signal.name { Samples = @n } -> DST::@signal.name") {
		t.Errorf("dynamic endpoints lost by the formatter:\n%s", out)
	}
	if _, err := parser.NewParser(out).Parse(); err != nil {
		t.Fatalf("re-parse formatted output: %v\n%s", err, out)
	}
}
