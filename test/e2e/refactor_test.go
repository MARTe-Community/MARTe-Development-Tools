package e2e

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

const legacyConfig = `#package T

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
        A = {
          Type = uint32
        }
        B = {
          Type = float32
        }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Copy = {
      Class = IOGAM
      InputSignals = {
        A = {
          DataSource = DDB
          Type = uint32
        }
      }
      OutputSignals = {
        B = {
          DataSource = DDB
          Type = float32
        }
      }
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
        Functions = { Copy }
      }
    }
  }
}
`

func runRefactorCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(framework.GetMDTPath(), append([]string{"refactor"}, args...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("refactor: %v", err)
		}
	}
	return string(out), code
}

// `mdt refactor` rewrites a legacy configuration, is idempotent and reports
// pending work with --check.
func TestRefactorCLI(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()
	tf := framework.WrapT(t, ctx)

	path := tf.CreateFile("legacy.marte", legacyConfig)

	if out, code := runRefactorCLI(t, "--check", path); code == 0 {
		t.Fatalf("--check reported no changes:\n%s", out)
	}
	if out, code := runRefactorCLI(t, path); code != 0 {
		t.Fatalf("refactor failed (%d):\n%s", code, out)
	}

	content, err := ctx.ReadFile("legacy.marte")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{"copy Copy {", "DDB::A { Type = uint32 } -> DDB::B { Type = float32 }", "A: uint32", "B: float32"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q after refactor:\n%s", want, content)
		}
	}
	if strings.Contains(content, "Class = IOGAM") {
		t.Errorf("legacy IOGAM left:\n%s", content)
	}

	if out, code := runRefactorCLI(t, "--check", path); code != 0 {
		t.Errorf("--check still reports changes:\n%s", out)
	}

	check := tf.RunCheck("legacy.marte")
	for _, d := range check.Diagnostics {
		if d.Severity == "ERROR" {
			t.Errorf("refactored file has an error: %s:%d %s", d.File, d.Line, d.Message)
		}
	}
}

// --no-verify skips the build comparison and still rewrites the file.
func TestRefactorCLINoVerify(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()
	tf := framework.WrapT(t, ctx)

	path := tf.CreateFile("legacy_noverify.marte", legacyConfig)
	if out, code := runRefactorCLI(t, "--no-verify", path); code != 0 {
		t.Fatalf("refactor --no-verify failed (%d):\n%s", code, out)
	}
	content, err := ctx.ReadFile("legacy_noverify.marte")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(content, "copy Copy {") {
		t.Errorf("file was not rewritten:\n%s", content)
	}
}
