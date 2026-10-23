package integration

import (
	"os"
	"strings"
	"testing"
)

// Two sibling foreach blocks each declare `let name`. The bindings are
// lexical to their own iteration, so the second declaration must not be
// reported as a duplicate and each expansion must use its own value (no
// cross-named signals such as DO_DI0 or DI_DO0).
const loopLetFixture = `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB5
    +DDB5 = { Class = GAMDataSource  AllowNoProducers = 1 }
    with json("signals.json") as signals begin
      foreach sig in @signals.DigitalInputSignals
        let name: string = "DI_" .. @sig.name
        +DDB5 = { Signals = { @name = { Type = uint8  NumberOfElements = 1 } } }
      end
      foreach sig in @signals.DigitalOutputSignals
        let name: string = "DO_" .. @sig.name
        +DDB5 = { Signals = { @name = { Type = uint8  NumberOfElements = 1 } } }
      end
    end
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = DDB5 }
  +States = { Class = ReferenceContainer }
}
`

const loopLetJSON = `{
  "DigitalInputSignals": [{"name": "DI0"}, {"name": "DI1"}],
  "DigitalOutputSignals": [{"name": "DO0"}]
}`

func TestLoopLetScoping(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/signals.json", []byte(loopLetJSON), 0644); err != nil {
		t.Fatal(err)
	}
	file := dir + "/app.marte"
	if err := os.WriteFile(file, []byte(loopLetFixture), 0644); err != nil {
		t.Fatal(err)
	}

	out := buildTempWithDir(t, dir, file)

	for _, want := range []string{"DI_DI0 = {", "DI_DI1 = {", "DO_DO0 = {"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing signal %q in build output:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"DO_DI0", "DO_DI1", "DI_DO0", "DO_ = {", "@name"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("unexpected %q (cross-scope expansion) in build output:\n%s", unwanted, out)
		}
	}
}
