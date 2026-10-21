package e2e

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

const optimizeApp = `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB
    +TDS = {
      Class = TimingDataSource
    }
    +DDB = {
      Class = GAMDataSource
      Signals = {
        A = { Type = uint32 }
        B = { Type = uint32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Copy1 = {
      Class = IOGAM
      InputSignals = { Counter = { DataSource = TDS  Type = uint32 } }
      OutputSignals = { A = { DataSource = DDB } }
    }
    +Copy2 = {
      Class = IOGAM
      InputSignals = { A = { DataSource = DDB } }
      OutputSignals = { B = { DataSource = DDB } }
    }
  }
  Scheduler = {
    Class = GAMScheduler
    TimingDataSource = TDS
  }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        CPUs = 0x1
        Functions = { Copy1 Copy2 }
      }
    }
  }
}
`

// `mdt build -O` merges adjacent IOGAMs, reports what it did, and the
// result still validates.
func TestBuildOptimizeFlag(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()
	tf := framework.WrapT(t, ctx)

	tf.CreateFile("app.marte", optimizeApp)

	plain := tf.RunBuild("app.marte", "-o", "plain.marte")
	if plain.ExitCode != 0 {
		t.Fatalf("plain build failed (%d):\nstdout=%s\nstderr=%s", plain.ExitCode, plain.Output, plain.Stderr)
	}
	res := tf.RunBuild("app.marte", "-O", "-o", "opt.marte")
	if res.ExitCode != 0 {
		t.Fatalf("optimized build failed (%d):\n%s", res.ExitCode, res.Stderr)
	}
	if summary := res.Stderr + res.Output; !strings.Contains(summary, "merged 1 IOGAM") {
		t.Errorf("optimisation summary missing from output:\nstdout=%s\nstderr=%s", res.Output, res.Stderr)
	}

	plainOut, err := ctx.ReadFile("plain.marte")
	if err != nil {
		t.Fatalf("read plain: %v", err)
	}
	optOut, err := ctx.ReadFile("opt.marte")
	if err != nil {
		t.Fatalf("read optimized: %v", err)
	}
	if got, want := strings.Count(plainOut, "Class = IOGAM"), 2; got != want {
		t.Errorf("plain build has %d IOGAMs, want %d", got, want)
	}
	if got, want := strings.Count(optOut, "Class = IOGAM"), 1; got != want {
		t.Errorf("optimized build has %d IOGAMs, want %d", got, want)
	}
	if !strings.Contains(optOut, "Functions = { Copy1 }") {
		t.Errorf("thread function list not consolidated:\n%s", optOut)
	}

	// The optimized configuration must still be accepted by the checker.
	check := tf.RunCheck("opt.marte")
	for _, d := range check.Diagnostics {
		if d.Severity == "ERROR" {
			t.Errorf("optimized output has an error: %s:%d %s", d.File, d.Line, d.Message)
		}
	}
}

// The help output documents -O/--optimize.
func TestBuildHelpDocumentsOptimize(t *testing.T) {
	out, code := runHelp(t, "build")
	if code != 0 {
		t.Fatalf("build -h exited %d", code)
	}
	for _, want := range []string{"--optimize", "-O", "IOGAM"} {
		if !strings.Contains(out, want) {
			t.Errorf("build help does not mention %q:\n%s", want, out)
		}
	}
}
