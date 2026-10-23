package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
	"github.com/marte-community/marte-dev-tools/internal/varsfile"
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

// Per-signal options are emitted verbatim into the generated IOGAM entries
// and feed type inference for synthesized destination signals.
func TestCopyPerSignalOptions(t *testing.T) {
	src := copyAppArray(`copy {
      Acq.T1 { Type = uint32  Samples = @Samples  Frequency = 2 } -> Filter.In2
      Acq.T2 { Ranges = { { 0, 0 } } } -> Filter.In3 { Trigger = 1 }
      Acq.T1 -> Filter.In4 { Type = float64 }
    }`)
	src = strings.Replace(src, "var on: bool = true", "", 1)
	src = strings.Replace(src, "    +Filter = {", `    +Filter = {
`, 1)
	out, rep, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if rep.Copies != 3 {
		t.Errorf("Copies = %d, want 3 (%s)", rep.Copies, rep)
	}
	for _, want := range []string{
		`Samples = @Samples`,
		"Frequency = 2",
		"Ranges = { { 0 0 } }",
		"Trigger = 1",
		"Type = uint32",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("option %q not emitted:\n%s", want, out)
		}
	}
	// The synthesized destination buffer uses the type from the
	// destination option.
	bufStart := strings.Index(out, "In4 = {")
	if bufStart < 0 {
		t.Fatalf("synthesized signal In4 missing:\n%s", out)
	}
	buf := out[bufStart:]
	buf = buf[:strings.Index(buf, "}\n")]
	if !strings.Contains(buf, "Type = float64") {
		t.Errorf("buffer signal type not inferred from the destination option:\n%s", buf)
	}
}

const groupFixture = `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = Src
    +TDS = { Class = TimingDataSource }
    +Src = {
      Class = EPICSPVAInput
      Signals = {
        A = { Type = uint32 }
        B = { Type = uint8 }
        C = { Type = uint8 }
      }
    }
    +Dst = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        D = { Type = uint32 }
        E = { Type = uint16 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    %s
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = TDS }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        CPUs = 0x1
        Functions = { Blocks }
      }
    }
  }
}
`

// blockOf returns the full `+name = { … }` block from a built output.
func blockOf(t *testing.T, out, name string) string {
	t.Helper()
	start := strings.Index(out, "+"+name+" = {")
	if start < 0 {
		t.Fatalf("%s not found:\n%s", name, out)
	}
	depth := 0
	for i := start; i < len(out); i++ {
		switch out[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return out[start : i+1]
			}
		}
	}
	t.Fatalf("unterminated block %s", name)
	return ""
}

// Packing groups: two uint8 inputs become one uint16 output through a single
// memory copy (IOGAM copies the whole input block to the output block).
func TestCopyGroupPacking(t *testing.T) {
	src := fmt.Sprintf(groupFixture, `    copy Blocks {
      (Src::A, Src::B, Src::C) -> (Dst::D, Dst::E)
    }`)
	out, _, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	block := blockOf(t, out, "Blocks")
	for _, want := range []string{"A = {", "B = {", "C = {", "D = {", "E = {"} {
		if !strings.Contains(block, want) {
			t.Errorf("entry %q missing:\n%s", want, block)
		}
	}
	// Input and output lists keep the declared order.
	if !(strings.Index(block, "A = {") < strings.Index(block, "B = {") &&
		strings.Index(block, "B = {") < strings.Index(block, "C = {")) {
		t.Errorf("input order changed:\n%s", block)
	}
	if !(strings.Index(block, "D = {") < strings.Index(block, "E = {")) {
		t.Errorf("output order changed:\n%s", block)
	}
}

// Unpacking: one uint16 input feeds two uint8 outputs.
func TestCopyGroupUnpacking(t *testing.T) {
	src := fmt.Sprintf(groupFixture, `    copy Blocks {
      Dst::E -> (Src::B, Src::C)
    }`)
	// E is read from a GAMDataSource this fixture does not produce; the
	// library build still lowers the group, which is what is checked here.
	out, _, err := buildSugar(t, src, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	block := blockOf(t, out, "Blocks")
	if !strings.Contains(block, "E = {") || !strings.Contains(block, "B = {") || !strings.Contains(block, "C = {") {
		t.Errorf("unpacking entries missing:\n%s", block)
	}
}

// A group whose byte sizes do not match is rejected.
func TestCopyGroupSizeMismatch(t *testing.T) {
	src := fmt.Sprintf(groupFixture, `    copy Blocks {
      (Src::A, Src::B) -> (Dst::E)
    }`)
	_, _, err := buildSugar(t, src, nil)
	if err == nil || !strings.Contains(err.Error(), "moves 5 input bytes into 2 output bytes") {
		t.Fatalf("expected a size mismatch error, got %v", err)
	}
}

// Group members without a known type cannot be size-checked.
func TestCopyGroupUnknownSize(t *testing.T) {
	src := strings.Replace(groupFixture, `A = { Type = uint32 }`, `A = { }`, 1)
	src = strings.Replace(src, `%s`, `    copy Blocks { (Src::A, Src::B, Src::C) -> (Dst::D, Dst::E) }`, 1)
	_, _, err := buildSugar(t, src, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot determine the byte size") {
		t.Fatalf("expected an unknown-size error, got %v", err)
	}
	// The position must be qualified with its source file: a bare
	// "line:col" is unattributable in a multi-file project.
	if !strings.Contains(err.Error(), ".marte:") {
		t.Errorf("copy error does not name its file: %v", err)
	}
}

// `-O` must not reorder the entries of a block IOGAM (the memory mapping is
// positional).
func TestOptimizerKeepsBlockOrder(t *testing.T) {
	src := fmt.Sprintf(groupFixture, `    copy Blocks {
      (Src::A, Src::B, Src::C) -> (Dst::D, Dst::E)
    }`)
	out, rep := buildWithOptimize(t, src, true, nil)
	if rep.MergedIOGAMs != 0 {
		t.Errorf("block IOGAM was merged: %s", rep)
	}
	block := blockOf(t, out, "Blocks")
	if !(strings.Index(block, "A = {") < strings.Index(block, "B = {") &&
		strings.Index(block, "B = {") < strings.Index(block, "C = {")) {
		t.Errorf("optimizer reordered a block IOGAM:\n%s", block)
	}
}

// A JSON variables file can override structured variables (nested objects
// become nested MARTe structs and merge field-by-field).
func TestJSONFileOverridesStructuredVariable(t *testing.T) {
	src := `#package T

var hardware: {
  address: uint32 = 1
  port: uint32 = 2
  name: string = "dev"
}

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
        A = { Type = uint32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Use = {
      Class = FilterGAM
      InputSignals = {
        A1 = { DataSource = DDB }
        A2 = { DataSource = DDB }
      }
      OutputSignals = {
        Z = { DataSource = DDB }
      }
      Address = @hardware.address
      Port = @hardware.port
      Name = @hardware.name
    }
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = TDS }
}
`
	dir := t.TempDir()
	varsPath := filepath.Join(dir, "rf01_cfg.json")
	varsJSON := `{
  "hardware": {
    "address": 4660,
    "port": 1234,
    "name": "rf01"
  }
}`
	if err := os.WriteFile(varsPath, []byte(varsJSON), 0o644); err != nil {
		t.Fatalf("write vars: %v", err)
	}
	appPath := filepath.Join(dir, "app.marte")
	if err := os.WriteFile(appPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}

	vars, err := varsfile.Load(varsPath)
	if err != nil {
		t.Fatalf("load vars: %v", err)
	}
	b := builder.NewBuilder([]string{appPath}, vars)
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
	for _, want := range []string{
		"Address = 4660",
		"Port = 1234",
		"Name = \"rf01\"",
	} {
		if !strings.Contains(string(content), want) {
			t.Errorf("structured override %q missing:\n%s", want, content)
		}
	}
}
