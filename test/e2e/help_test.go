package e2e

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

// runHelp executes `mdt <cmd> -h` and returns combined output and the
// process exit code.
func runHelp(t *testing.T, cmd string) (string, int) {
	t.Helper()
	out, err := exec.Command(framework.GetMDTPath(), cmd, "-h").CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return string(out), exitErr.ExitCode()
		}
		return string(out), -1
	}
	return string(out), 0
}

// Every command that accepts `-j` documents the supported formats in its
// own help output.
func TestHelpDocumentsVarsFiles(t *testing.T) {
	for _, cmd := range []string{"build", "check", "graph"} {
		t.Run(cmd, func(t *testing.T) {
			out, code := runHelp(t, cmd)
			if code != 0 {
				t.Fatalf("mdt %s -h exited %d, want 0\n%s", cmd, code, out)
			}
			for _, want := range []string{"-j <file>", "yaml", "toml"} {
				if !strings.Contains(out, want) {
					t.Errorf("help for %s does not mention %q:\n%s", cmd, want, out)
				}
			}
		})
	}
}

// `mdt graph -j` feeds the same override pipeline as -v.
func TestGraphVarsFileOverride(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	ctx.CreateFile("app.marte", `#package T
var Streaming: bool = true
+App = {
  Class = ReferenceContainer
  +Main = {
    Class = RealTimeApplication
    +Functions = {
      Class = ReferenceContainer
      +GAM = {
        Class = IOGAM
        InputSignals = { A = { DataSource = DDB, Type = float32 } }
        OutputSignals = { A = { DataSource = DDB, Type = float32 } }
      }
    }
    +Data = {
      Class = ReferenceContainer
      +DDB = { Class = GAMDataSource }
    }
    +States = {
      Class = ReferenceContainer
      +Idle = {
        Class = RealTimeState
        +Timing = {
          Class = ReferenceContainer
          +Source = { Class = TimingDataSource }
        }
      }
    }
  }
}
`)
	ctx.CreateFile("vars.yaml", "Streaming: false\n")

	runGraph := func(args ...string) (string, int, string) {
		t.Helper()
		cmd := exec.Command(framework.GetMDTPath(), append([]string{"graph"}, args...)...)
		cmd.Dir = ctx.RootDir()
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
			} else {
				t.Fatalf("graph: %v", err)
			}
		}
		return string(out), code, ""
	}

	if out, code, _ := runGraph("-o", "fromfile.dot", "-j", "vars.yaml", "app.marte"); code != 0 {
		t.Fatalf("graph -j failed (%d):\n%s", code, out)
	}
	if out, code, _ := runGraph("-o", "fromflag.dot", "-vStreaming=false", "app.marte"); code != 0 {
		t.Fatalf("graph -v failed (%d):\n%s", code, out)
	}

	fromFile, err := ctx.ReadFile("fromfile.dot")
	if err != nil {
		t.Fatalf("read graph output: %v", err)
	}
	fromFlag, err := ctx.ReadFile("fromflag.dot")
	if err != nil {
		t.Fatalf("read graph output: %v", err)
	}
	if fromFile != fromFlag {
		t.Errorf("-j and -v produced different graphs:\n-j:\n%s\n-v:\n%s", fromFile, fromFlag)
	}

	// A malformed variables file fails loudly instead of being ignored.
	out, code, _ := runGraph("-o", "x.dot", "-j", "missing.yaml", "app.marte")
	if code == 0 {
		t.Fatalf("graph with missing vars file exited 0:\n%s", out)
	}
}
