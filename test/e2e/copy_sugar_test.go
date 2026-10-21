package e2e

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

const copySugarApp = `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB
    +TDS = { Class = TimingDataSource }
    +DDB = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        T1 = { Type = float32 }
        In1 = { Type = float32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Acq = {
      Class = EPICSPVAInput
      OutputSignals = { T1 = { DataSource = DDB  Type = float32 } }
    }
    +Filter = {
      Class = FilterGAM
      InputSignals = { In1 = { DataSource = DDB  Type = float32 } }
      OutputSignals = { Z = { DataSource = DDB  Type = float32 } }
    }
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = TDS }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        CPUs = 0x1
        Functions = { Acq copy { Acq.T1 -> Filter.In1 as FIn1 } Filter }
      }
    }
  }
}
`

// `mdt check` accepts copy sugar (it is lowered before validation) and
// `mdt build` expands it to an IOGAM.
func TestCopySugarCLI(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()
	tf := framework.WrapT(t, ctx)

	tf.CreateFile("app.marte", copySugarApp)

	check := tf.RunCheck("app.marte")
	for _, d := range check.Diagnostics {
		if d.Severity == "ERROR" {
			t.Errorf("check rejected copy sugar: %s:%d %s", d.File, d.Line, d.Message)
		}
	}

	res := tf.RunBuild("app.marte", "-o", "out.marte")
	if res.ExitCode != 0 {
		t.Fatalf("build failed (%d):\nstdout=%s\nstderr=%s", res.ExitCode, res.Output, res.Stderr)
	}
	out, err := ctx.ReadFile("out.marte")
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if strings.Contains(out, "copy {") || strings.Contains(out, "as FIn1") {
		t.Errorf("sugar left in the built configuration:\n%s", out)
	}
	if !strings.Contains(out, "Class = IOGAM") {
		t.Errorf("copy was not lowered to an IOGAM:\n%s", out)
	}
	if !strings.Contains(out, `Alias = "In1"`) {
		t.Errorf("alias not emitted:\n%s", out)
	}
	if !strings.Contains(out, "Functions = { Acq MdtCopy1 Filter }") {
		t.Errorf("Functions list not rewritten:\n%s", out)
	}

	// The generated configuration validates like any other.
	again := tf.RunCheck("out.marte")
	for _, d := range again.Diagnostics {
		if d.Severity == "ERROR" {
			t.Errorf("lowered output has an error: %s:%d %s", d.File, d.Line, d.Message)
		}
	}
}
