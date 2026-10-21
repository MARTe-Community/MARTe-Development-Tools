package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

// buildPipeline builds src with pipeline/copy sugar and returns the output.
func buildPipeline(t *testing.T, src string) (string, builder.LowerReport, error) {
	t.Helper()
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
	defer f.Close()
	buildErr := b.Build(f)
	f.Close()
	content, _ := os.ReadFile(out)
	return string(content), b.LowerReport, buildErr
}

const pipelineHead = `#package T

gam Preprocess {
  in  Raw: float32
  out Clean: float32
  out Meter: float32

  +Scale = {
    Class = MathExpressionGAM
    InputSignals = { X = { DataSource = DDB  Type = float32 } }
    OutputSignals = { Scaled = { DataSource = DDB  Type = float32 } }
    Expression = "Scaled := X*0.5"
  }
  from Raw -> Scale.X

  +Limiter = {
    Class = FilterGAM
    InputSignals = { In = { DataSource = DDB  Type = float32 } }
    OutputSignals = { Out = { DataSource = DDB  Type = float32 } }
  }
  from Scale.Scaled -> Limiter.In
  from Limiter.Out -> Clean
  from Scale.Scaled -> Meter
}

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB
    +TDS = { Class = TimingDataSource }
    +EPICS = {
      Class = EPICSPVAInput
      Signals = { RawIn = { Type = float32 } }
    }
    +DDB = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        X = { Type = float32 }
        Scaled = { Type = float32 }
        In = { Type = float32 }
        Out = { Type = float32 }
        Clean = { Type = float32 }
      }
    }
    +SINK = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = { Meter = { Type = float32 } }
    }
  }
  +Functions = {
    Class = ReferenceContainer
`

const pipelineTail = `  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = TDS }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        CPUs = 0x1
        Functions = { Pre }
      }
    }
  }
}
`

func pipelineInstance() string {
	return `    +Pre = {
      Class = Preprocess
      Raw = EPICS::RawIn
      Clean = DDB::Clean
      Meter = SINK::Meter
    }
`
}

// A pipeline instance expands into prefixed GAMs and scheduled wiring.
func TestPipelineExpansion(t *testing.T) {
	src := pipelineHead + pipelineInstance() + pipelineTail
	out, rep, err := buildPipeline(t, src)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if rep.Pipelines != 1 {
		t.Fatalf("Pipelines = %d, want 1 (%s)", rep.Pipelines, rep)
	}
	if strings.Contains(out, "Class = Preprocess") {
		t.Errorf("pipeline instance left in output:\n%s", out)
	}
	for _, want := range []string{"+Pre_Scale = {", "+Pre_Limiter = {"} {
		if !strings.Contains(out, want) {
			t.Errorf("generated GAM %q missing:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "Functions = { MdtCopy1 Pre_Scale MdtCopy2 MdtCopy3 Pre_Limiter MdtCopy4 }") {
		t.Errorf("wiring not scheduled around its consumers:\n%s", out)
	}
	// Port bindings resolve to the external signals.
	if !strings.Contains(out, "DataSource = EPICS") || !strings.Contains(out, "Clean = {") {
		t.Errorf("port bindings not wired:\n%s", out)
	}
}

// A missing port binding is an error, not a partially expanded pipeline.
func TestPipelineUnboundPort(t *testing.T) {
	instance := `    +Pre = {
      Class = Preprocess
      Raw = EPICS::RawIn
      Clean = DDB::Clean
    }
`
	src := pipelineHead + instance + pipelineTail
	_, _, err := buildPipeline(t, src)
	if err == nil {
		t.Fatal("expected an error for an unbound output port")
	}
	if !strings.Contains(err.Error(), "not bound") {
		t.Errorf("unexpected error: %v", err)
	}
}

// The shipped complex example exercises pipelines, copy groups, from
// wiring and fusion, and must build without errors.
func TestComplexExampleBuilds(t *testing.T) {
	files, err := filepath.Glob("../examples/complex/src/*.marte")
	if err != nil || len(files) == 0 {
		t.Fatalf("no example files: %v", err)
	}
	b := builder.NewBuilder(files, nil)
	b.Optimize = true
	dir := t.TempDir()
	out := filepath.Join(dir, "out.marte")
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if err := b.Build(f); err != nil {
		t.Fatalf("build example: %v", err)
	}
	f.Close()
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if b.LowerReport.Pipelines != 1 || b.LowerReport.Fused != 1 {
		t.Errorf("example did not exercise pipeline+fusion: %s", b.LowerReport)
	}
	if !strings.Contains(string(content), "+Fanout = {") && !strings.Contains(string(content), "Fanout = {") {
		t.Errorf("copy group not emitted:\n%s", content)
	}
}
