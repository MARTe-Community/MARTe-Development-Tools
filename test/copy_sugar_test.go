package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// buildSugar builds src with the experimental copy/wiring sugar.
func buildSugar(t *testing.T, src string, overrides map[string]string) (string, builder.LowerReport, error) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "main.marte")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	b := builder.NewBuilder([]string{file}, overrides)
	out := filepath.Join(dir, "out.marte")
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	buildErr := b.Build(f)
	f.Close()
	content, err := os.ReadFile(out)
	if err != nil {
		return "", b.LowerReport, buildErr
	}
	return string(content), b.LowerReport, buildErr
}

const copyAppHead = `#package T

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
        T2 = { Type = float32 }
        In1 = { Type = float32 }
        In2 = { Type = float32 }
        Z = { Type = float32 }
      }
    }
    +SINK = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = { Telemetry = { Type = float32 } }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Acq = {
      Class = EPICSPVAInput
      OutputSignals = {
        T1 = { DataSource = DDB  Type = float32 }
        T2 = { DataSource = DDB  Type = float32 }
      }
    }
`

const copyAppTail = `    +Filter = {
      Class = FilterGAM
      InputSignals = {
        In1 = { DataSource = DDB  Type = float32 }
      }
      OutputSignals = {
        Z = { DataSource = DDB  Type = float32 }
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
        Functions = { Acq Links Filter }
      }
    }
  }
}
`

func copyApp(group string) string {
	return copyAppHead + group + copyAppTail
}

// copyAppArray places the snippet directly inside the thread's Functions
// list (where inline copy groups belong).
func copyAppArray(snippet string) string {
	head := strings.Replace(copyAppHead, "  +Functions = {\n    Class = ReferenceContainer\n    +Acq = {",
		"  +Functions = {\n    Class = ReferenceContainer\n    +Acq = {", 1)
	tail := strings.Replace(copyAppTail, "Functions = { Acq Links Filter }", "Functions = { Acq "+snippet+" Filter }", 1)
	return head + tail
}

// A named copy group lowers to one IOGAM object referenced by name.
func TestCopyGroupNamed(t *testing.T) {
	src := copyApp(`    copy Links {
      Acq.T1 -> Filter.In1 as FIn1
      Acq.T1 -> SINK::Telemetry
    }
`)
	out, rep, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if rep.Copies != 2 {
		t.Errorf("Copies = %d, want 2 (%s)", rep.Copies, rep)
	}
	if !strings.Contains(out, "+Links = {") || !strings.Contains(out, "Class = IOGAM") {
		t.Fatalf("named copy group not lowered to an IOGAM:\n%s", out)
	}
	if !strings.Contains(out, "Functions = { Acq Links Filter }") {
		t.Errorf("Functions list not rewritten:\n%s", out)
	}
	if !strings.Contains(out, `Alias = "In1"`) {
		t.Errorf("destination alias missing:\n%s", out)
	}
	// The source is used twice (Filter and SINK): the second read must be
	// aliased rather than duplicated under the same name.
	if !strings.Contains(out, `Alias = "T1"`) {
		t.Errorf("duplicate source read not aliased:\n%s", out)
	}
	if strings.Contains(out, "copy ") {
		t.Errorf("sugar left in output:\n%s", out)
	}
}

// An inline group gets a generated name and stays at its position.
func TestCopyGroupInline(t *testing.T) {
	src := copyAppArray(`copy {
      Acq.T2 -> Filter.In2
    }`)
	out, rep, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if rep.Copies != 1 {
		t.Errorf("Copies = %d, want 1", rep.Copies)
	}
	if !strings.Contains(out, "+MdtCopy1 = {") {
		t.Errorf("inline group not lowered:\n%s", out)
	}
	if !strings.Contains(out, "Functions = { Acq MdtCopy1 Filter }") {
		t.Errorf("inline group position not preserved:\n%s", out)
	}
}

// `GAM[n]` selects the n-th signal of the GAM's signal list.
func TestCopyIndexSelection(t *testing.T) {
	src := copyAppArray(`copy { Acq[2] -> Filter.In2 }`)
	out, _, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	iogam := out[strings.Index(out, "+MdtCopy1 = {"):]
	iogam = iogam[:strings.Index(iogam, "}\n")]
	if !strings.Contains(iogam, "T2 = {") {
		t.Errorf("index selection did not pick the second output:\n%s", iogam)
	}
}

// `at N` places the generated destination declaration at position N.
func TestCopyIndexPlacement(t *testing.T) {
	src := copyAppArray(`copy {
      Acq.T2 -> SINK::Telemetry at 1
      Acq.T1 -> Filter.In3 at 1
    }`)
	out, _, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	block := out[strings.Index(out, "+Filter = {"):]
	inputs := block[strings.Index(block, "InputSignals = {"):]
	inputs = inputs[:strings.Index(inputs, "}\n      }")]
	posIn3 := strings.Index(inputs, "In3 = {")
	posIn1 := strings.Index(inputs, "In1 = {")
	if posIn3 < 0 || posIn1 < 0 || posIn3 > posIn1 {
		t.Errorf("`at 1` did not place In3 first (In3=%d In1=%d):\n%s", posIn3, posIn1, inputs)
	}
}

// A destination slot without a DataSource gets a synthesized buffer.
func TestCopyBufferDataSource(t *testing.T) {
	src := copyAppArray(`copy {
      Acq.T1 -> Filter.In3
    }`)
	out, rep, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if rep.Buffers != 1 {
		t.Errorf("Buffers = %d, want 1 (%s)", rep.Buffers, rep)
	}
	if !strings.Contains(out, "+MdtCopyBuf1 = {") || !strings.Contains(out, "Class = GAMDataSource") {
		t.Fatalf("buffer DataSource not synthesized:\n%s", out)
	}
	if !strings.Contains(out, "In3 = {") {
		t.Errorf("destination slot not pointed at the buffer:\n%s", out)
	}
}

// Ranges map signal lists element-wise.
func TestCopyRange(t *testing.T) {
	src := copyAppArray(`copy { Acq[1..2] -> Filter[1..1] }`)
	if _, _, err := buildSugar(t, src, nil); err == nil {
		t.Fatal("expected an error for mismatched range lengths")
	}

	src = copyAppArray(`copy { Acq[1..2] -> Filter.In1 }`)
	if _, _, err := buildSugar(t, src, nil); err == nil {
		t.Fatal("expected an error for a range against a single destination")
	}
	_ = src
}

// `from` wires a consumer input straight to the producer's DataSource
// signal through Alias, without a copy GAM.
func TestFromWiring(t *testing.T) {
	src := copyApp(`    copy Links { Acq.T1 -> Filter.In1 }
` + `    +Post = {
      Class = FilterGAM
      InputSignals = {
        P1 = from Acq.T1
        P2 = from Acq[2] as Slot2
      }
      OutputSignals = {
        Z = { DataSource = DDB  Type = float32 }
      }
    }
`)
	out, rep, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if rep.Wired != 2 {
		t.Errorf("Wired = %d, want 2 (%s)", rep.Wired, rep)
	}
	post := out[strings.Index(out, "+Post = {"):]
	post = post[:strings.Index(post, "OutputSignals = {")]
	if !strings.Contains(post, `P1 = {`) || !strings.Contains(post, `Alias = "T1"`) {
		t.Errorf("from wiring missing on P1:\n%s", post)
	}
	if !strings.Contains(post, `Slot2 = {`) || !strings.Contains(post, `Alias = "T2"`) {
		t.Errorf("from wiring with alias missing on Slot2:\n%s", post)
	}
	if strings.Contains(post, "DataSource = MdtCopyBuf") {
		t.Errorf("from wiring should not need a copy buffer:\n%s", post)
	}
}

// A copy that is already wired (same DataSource and signal) is dropped.
func TestCopyRedundant(t *testing.T) {
	src := copyAppArray(`copy { Acq.T1 -> DDB::T1 }`)
	out, rep, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if rep.Redundant != 1 {
		t.Errorf("Redundant = %d, want 1 (%s)", rep.Redundant, rep)
	}
	if strings.Contains(out, "Class = IOGAM") {
		t.Errorf("redundant copy emitted an IOGAM:\n%s", out)
	}
}

// Sugar errors are reported instead of producing a broken config.
func TestCopyErrors(t *testing.T) {
	cases := []struct{ name, group, want string }{
		{"unknown gam", `copy { Nope.X -> Filter.In1 }`, "unknown GAM"},
		{"unknown signal", `copy { Acq.Nope -> Filter.In1 }`, "no output signal"},
		{"index out of range", `copy { Acq[9] -> Filter.In1 }`, "out of bounds"},
		{"placement out of range", `copy { Acq.T1 -> Filter.In2 at 9 }`, "out of range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := buildSugar(t, copyAppArray(tc.group), nil)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

// Lowering the sugar yields a configuration that validates cleanly.
func TestCopySugarOutputValidates(t *testing.T) {
	src := copyApp(`    copy Links {
      Acq.T1 -> Filter.In1 as FIn1
      Acq[2] -> Filter.In2 at 1
      Acq.T1 -> SINK::Telemetry
    }
` + `    +Post = {
      Class = FilterGAM
      InputSignals = {
        P1 = from Acq.T1
      }
      OutputSignals = {
        Z = { DataSource = DDB  Type = float32 }
      }
    }
`)
	out, _, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "out.marte")
	if err := os.WriteFile(file, []byte(out), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := parser.NewParser(out).Parse()
	if err != nil {
		t.Fatalf("lowered output does not parse: %v\n%s", err, out)
	}
	tree := index.NewProjectTree()
	tree.AddFile(file, cfg)
	tree.ResolveReferences(nil)
	v := validator.NewValidator(tree, dir, nil)
	v.ValidateProject(context.Background())
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError {
			t.Errorf("lowered output error: %s:%d: %s", d.File, d.Position.Line, d.Message)
		}
	}
}

// `mdt check` accepts sugar by lowering before validation.
func TestCopySugarCheckLowers(t *testing.T) {
	src := copyAppArray(`copy { Acq.T1 -> Filter.In1 as FIn1 }`)
	dir := t.TempDir()
	file := filepath.Join(dir, "main.marte")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tree := index.NewProjectTree()
	tree.AddFile(file, cfg)
	tree.ResolveReferences(nil)
	if _, err := builder.Lower(tree); err != nil {
		t.Fatalf("Lower: %v", err)
	}
	v := validator.NewValidator(tree, dir, nil)
	v.ValidateProject(context.Background())
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError && strings.Contains(d.Message, "copy") {
			t.Errorf("check reported sugar error: %s", d.Message)
		}
	}
	// The copy must be visible as a producer: Filter.In1 (renamed FIn1)
	// reads the DDB signal produced by the generated IOGAM.
	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelError && strings.Contains(d.Message, "before being produced") {
			t.Errorf("ordering check ignored the lowered copy: %s", d.Message)
		}
	}
}

// `fuse A with B` inlines B's expression into A when the intermediate
// signal has no other consumer, and removes B.
func TestFuseExpressionGAMs(t *testing.T) {
	src := copyAppArray(`fuse Pre with Post`) + ""
	src = strings.Replace(src, "    +Acq = {", `    +Pre = {
      Class = MathExpressionGAM
      InputSignals = { X = { DataSource = DDB  Type = float32 } }
      OutputSignals = {
        Y = { DataSource = DDB  Type = float32 }
        Z = { DataSource = DDB  Type = float32 }
      }
      Expression = "Y := X*2; Z := X+1"
    }
    +Post = {
      Class = MathExpressionGAM
      InputSignals = { Y = { DataSource = DDB  Type = float32 } }
      OutputSignals = { W = { DataSource = DDB  Type = float32 } }
      Expression = "W := Y+1"
    }
    +Acq = {`, 1)
	src = strings.Replace(src, "Functions = { Acq fuse Pre with Post Filter }",
		"Functions = { Pre fuse Pre with Post Post }", 1)
	out, rep, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if rep.Fused != 1 {
		t.Fatalf("Fused = %d, want 1 (%s)\n%s", rep.Fused, rep, out)
	}
	if strings.Contains(out, "+Post = {") || strings.Contains(out, "Functions = { Pre Post }") {
		t.Errorf("fused GAM not removed:\n%s", out)
	}
	if !strings.Contains(out, `Expression = "Z := X+1; W := (X*2)+1"`) {
		t.Errorf("expressions not fused:\n%s", out)
	}
	// The intermediate Y must disappear from Pre's outputs.
	pre := out[strings.Index(out, "+Pre = {"):]
	pre = pre[:strings.Index(pre, "Expression")]
	if strings.Contains(pre, "Y = {") {
		t.Errorf("intermediate signal kept after fusion:\n%s", pre)
	}
}

// Fusion is skipped when the intermediate signal has another consumer.
func TestFuseKeepsSharedSignal(t *testing.T) {
	src := copyAppArray(`fuse Pre with Post`)
	src = strings.Replace(src, "    +Acq = {", `    +Pre = {
      Class = MathExpressionGAM
      InputSignals = { X = { DataSource = DDB  Type = float32 } }
      OutputSignals = { Y = { DataSource = DDB  Type = float32 } }
      Expression = "Y := X*2"
    }
    +Post = {
      Class = MathExpressionGAM
      InputSignals = { Y = { DataSource = DDB  Type = float32 } }
      OutputSignals = { W = { DataSource = DDB  Type = float32 } }
      Expression = "W := Y+1"
    }
    +Other = {
      Class = FilterGAM
      InputSignals = { Y2 = { DataSource = DDB  Alias = Y  Type = float32 } }
      OutputSignals = { Z = { DataSource = DDB  Type = float32 } }
    }
    +Acq = {`, 1)
	src = strings.Replace(src, "Functions = { Acq fuse Pre with Post Filter }",
		"Functions = { Pre fuse Pre with Post Post Other }", 1)
	out, rep, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if rep.Fused != 0 {
		t.Fatalf("Fused = %d, want 0 (signal has two consumers)\n%s", rep.Fused, out)
	}
	if !strings.Contains(out, "+Post = {") {
		t.Errorf("shared-signal chain was fused:\n%s", out)
	}
}

// An anonymous group in an object body would never run; it is rejected.
func TestCopyAnonymousGroupOutsideFunctionsList(t *testing.T) {
	src := copyApp(`    copy {
      Acq.T1 -> Filter.In1
    }
`)
	_, _, err := buildSugar(t, src, nil)
	if err == nil {
		t.Fatal("expected an error for an anonymous group in an object body")
	}
	if !strings.Contains(err.Error(), "anonymous copy group") {
		t.Errorf("unexpected error: %v", err)
	}
}
