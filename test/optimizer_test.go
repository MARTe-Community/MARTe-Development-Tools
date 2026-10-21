package integration

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// buildWithOptimize writes src to a temp file, builds it, and returns the
// output text plus the optimization report.
func buildWithOptimize(t *testing.T, src string, optimize bool, overrides map[string]string) (string, builder.OptimizeReport) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "main.marte")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	b := builder.NewBuilder([]string{file}, overrides)
	b.Optimize = optimize
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
	return string(content), b.OptimizeReport
}

const optimizeFixture = `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +DDB = {
      Class = GAMDataSource
      Signals = {
        C = { Type = uint32 }
        A = { Type = uint32 }
        B = { Type = uint32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Copy1 = {
      Class = IOGAM
      InputSignals = { A = { DataSource = DDB } }
      OutputSignals = { A2 = { DataSource = DDB } }
    }
    +Copy2 = {
      Class = IOGAM
      InputSignals = { B = { DataSource = DDB } }
      OutputSignals = { B2 = { DataSource = DDB } }
    }
    +Copy3 = {
      Class = IOGAM
      InputSignals = { C = { DataSource = DDB } }
      OutputSignals = { C2 = { DataSource = DDB } }
    }
  }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        CPUs = 0x1
        Functions = { Copy1 Copy2 Copy3 }
      }
    }
  }
}
`

func TestOptimizeMergesAdjacentIOGAMs(t *testing.T) {
	out, rep := buildWithOptimize(t, optimizeFixture, true, nil)
	if rep.MergedIOGAMs != 2 {
		t.Errorf("MergedIOGAMs = %d, want 2\nreport: %s", rep.MergedIOGAMs, rep)
	}
	if got := strings.Count(out, "Class = IOGAM"); got != 1 {
		t.Errorf("output has %d IOGAM definitions, want 1:\n%s", got, out)
	}
	// Copy order is preserved: A->A2, B->B2, C->C2.
	idxA := strings.Index(out, "A2")
	idxB := strings.Index(out, "B2")
	idxC := strings.Index(out, "C2")
	if idxA < 0 || idxB < 0 || idxC < 0 || !(idxA < idxB && idxB < idxC) {
		t.Errorf("copy order not preserved (A2=%d B2=%d C2=%d):\n%s", idxA, idxB, idxC, out)
	}
	if strings.Contains(out, "Copy2") || strings.Contains(out, "Copy3") {
		t.Errorf("merged GAM definitions still present:\n%s", out)
	}
	if !strings.Contains(out, "Functions = { Copy1 }") {
		t.Errorf("Functions list not consolidated:\n%s", out)
	}
}

func TestOptimizeDisabledByDefault(t *testing.T) {
	out, rep := buildWithOptimize(t, optimizeFixture, false, nil)
	if !rep.Empty() {
		t.Errorf("report not empty without --optimize: %s", rep)
	}
	if got := strings.Count(out, "Class = IOGAM"); got != 3 {
		t.Errorf("output has %d IOGAM definitions, want 3", got)
	}
}

func TestOptimizeKeepsNonAdjacentIOGAMs(t *testing.T) {
	src := strings.Replace(optimizeFixture,
		"Functions = { Copy1 Copy2 Copy3 }",
		"Functions = { Copy1 Math Copy3 }", 1)
	src = strings.Replace(src, `    +Copy3 = {`, `    +Math = {
      Class = MathExpressionGAM
      InputSignals = { A = { DataSource = DDB } }
      OutputSignals = { B = { DataSource = DDB } }
    }
    +Copy3 = {`, 1)
	out, rep := buildWithOptimize(t, src, true, nil)
	if rep.MergedIOGAMs != 0 {
		t.Errorf("MergedIOGAMs = %d, want 0 (GAM separates the IOGAMs): %s", rep.MergedIOGAMs, rep)
	}
	if got := strings.Count(out, "Class = IOGAM"); got != 3 {
		t.Errorf("output has %d IOGAMs, want 3", got)
	}
}

func TestOptimizeRemovesIdentityIOGAM(t *testing.T) {
	src := `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +DDB = {
      Class = GAMDataSource
      Signals = {
        A = { Type = uint32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Id = {
      Class = IOGAM
      InputSignals = { A = { DataSource = DDB } }
      OutputSignals = { A = { DataSource = DDB } }
    }
  }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        CPUs = 0x1
        Functions = { Id }
      }
    }
  }
}
`
	out, rep := buildWithOptimize(t, src, true, nil)
	if rep.RemovedIdentityGAMs != 1 {
		t.Errorf("RemovedIdentityGAMs = %d, want 1: %s", rep.RemovedIdentityGAMs, rep)
	}
	if strings.Contains(out, "Class = IOGAM") {
		t.Errorf("identity IOGAM still present:\n%s", out)
	}
	if strings.Contains(out, "Functions = { Id }") {
		t.Errorf("dangling Functions reference:\n%s", out)
	}
}

// DataSource signals are ordered by first consumer so signals used by the
// same GAM are adjacent.
func TestOptimizeOrdersDataSourceSignals(t *testing.T) {
	src := `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +DDB = {
      Class = GAMDataSource
      Signals = {
        Z = { Type = uint32 }
        A = { Type = uint32 }
        B = { Type = uint32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +First = {
      Class = MathExpressionGAM
      InputSignals = {
        B = { DataSource = DDB }
        A = { DataSource = DDB }
      }
      OutputSignals = { Z = { DataSource = DDB } }
    }
    +Second = {
      Class = MathExpressionGAM
      InputSignals = { Z = { DataSource = DDB } }
      OutputSignals = { B = { DataSource = DDB } }
    }
  }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        CPUs = 0x1
        Functions = { First Second }
      }
    }
  }
}
`
	out, rep := buildWithOptimize(t, src, true, nil)
	if rep.ReorderedSignals == 0 {
		t.Fatalf("no DataSource signals reordered: %s\n%s", rep, out)
	}
	// First reads B and A; both are consumed before Z (Second), so A and B
	// must precede Z in the DataSource signal list.
	signalsBlock := out[strings.Index(out, "Signals = {"):]
	signalsBlock = signalsBlock[:strings.Index(signalsBlock, "}\n      }")]
	posA := strings.Index(signalsBlock, "A = {")
	posB := strings.Index(signalsBlock, "B = {")
	posZ := strings.Index(signalsBlock, "Z = {")
	if posA < 0 || posB < 0 || posZ < 0 || !(posA < posZ && posB < posZ) {
		t.Errorf("expected A/B before Z (A=%d B=%d Z=%d):\n%s", posA, posB, posZ, signalsBlock)
	}
}

// IOGAMs referenced through a shared `let` list are merged once and every
// thread using the list is rewritten.
func TestOptimizeMergesSharedFunctionsList(t *testing.T) {
	src := `#package T

let functions: [&GAM] = { Copy1, Copy2 }

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
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
      InputSignals = { A = { DataSource = DDB } }
      OutputSignals = { A2 = { DataSource = DDB } }
    }
    +Copy2 = {
      Class = IOGAM
      InputSignals = { B = { DataSource = DDB } }
      OutputSignals = { B2 = { DataSource = DDB } }
    }
  }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        CPUs = 0x1
        Functions = @functions
      }
    }
    +Stop = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        CPUs = 0x1
        Functions = @functions
      }
    }
  }
}
`
	out, rep := buildWithOptimize(t, src, true, nil)
	if rep.MergedIOGAMs != 1 {
		t.Fatalf("MergedIOGAMs = %d, want 1: %s", rep.MergedIOGAMs, rep)
	}
	if got := strings.Count(out, "Functions = { Copy1 }"); got != 2 {
		t.Errorf("expected both thread lists to become { Copy1 }, got %d occurrences:\n%s", got, out)
	}
	if strings.Contains(out, "Function = { Copy1 Copy2 }") || strings.Contains(out, "Copy2") {
		t.Errorf("merged GAM still referenced:\n%s", out)
	}
	if got := strings.Count(out, "Class = IOGAM"); got != 1 {
		t.Errorf("expected a single IOGAM, got %d:\n%s", got, out)
	}
}

// IOGAM copy pairs are grouped by source DataSource, keeping inputs and
// outputs paired positionally.
func TestOptimizeGroupsIOGAMCopies(t *testing.T) {
	src := `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +DDB = {
      Class = GAMDataSource
      Signals = {
        S1 = { Type = uint32 }
        S2 = { Type = uint32 }
        O1 = { Type = uint32 }
        O2 = { Type = uint32 }
      }
    }
    +EPICS = {
      Class = EPICSCAInput
      Signals = {
        P1 = { Type = float32 }
        P2 = { Type = float32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Copy = {
      Class = IOGAM
      InputSignals = {
        S1 = { DataSource = DDB }
        P1 = { DataSource = EPICS }
        S2 = { DataSource = DDB }
        P2 = { DataSource = EPICS }
      }
      OutputSignals = {
        O1 = { DataSource = DDB }
        Q1 = { DataSource = DDB }
        O2 = { DataSource = DDB }
        Q2 = { DataSource = DDB }
      }
    }
  }
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
	out, rep := buildWithOptimize(t, src, true, nil)
	if rep.ReorderedGAMSignals == 0 {
		t.Fatalf("IOGAM copies were not reordered: %s\n%s", rep, out)
	}

	iogam := out[strings.Index(out, "Class = IOGAM"):]
	split := strings.Index(iogam, "OutputSignals = {")
	if split < 0 {
		t.Fatalf("no OutputSignals block:\n%s", out)
	}
	inputs, outputs := iogam[:split], iogam[split:]

	order := func(block string, names ...string) []int {
		idx := make([]int, len(names))
		for i, n := range names {
			idx[i] = strings.Index(block, n+" = {")
		}
		return idx
	}
	// Inputs grouped: S1, S2 (DDB) before P1, P2 (EPICS).
	in := order(inputs, "S1", "S2", "P1", "P2")
	if !(in[0] < in[1] && in[1] < in[2] && in[2] < in[3]) {
		t.Errorf("inputs not grouped by DataSource: %v\n%s", in, inputs)
	}
	// Outputs permuted identically: O1, O2, Q1, Q2 (pairs S1->O1, S2->O2,
	// P1->Q1, P2->Q2 stay aligned).
	outIdx := order(outputs, "O1", "O2", "Q1", "Q2")
	if !(outIdx[0] < outIdx[1] && outIdx[1] < outIdx[2] && outIdx[2] < outIdx[3]) {
		t.Errorf("outputs not permuted with the inputs: %v\n%s", outIdx, outputs)
	}
}

// The optimized output must not introduce new validation errors compared
// with the plain build.
func TestOptimizeOutputValidates(t *testing.T) {
	countErrors := func(src string) map[string]int {
		t.Helper()
		dir := t.TempDir()
		file := filepath.Join(dir, "cfg.marte")
		if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		cfg, err := parser.NewParser(src).Parse()
		if err != nil {
			t.Fatalf("output does not parse: %v\n%s", err, src)
		}
		tree := index.NewProjectTree()
		tree.AddFile(file, cfg)
		tree.ResolveReferences(nil)
		v := validator.NewValidator(tree, dir, nil)
		v.ValidateProject(context.Background())
		out := map[string]int{}
		for _, d := range v.Diagnostics {
			if d.Level == validator.LevelError {
				out[normalizeMessage(d.Message)]++
			}
		}
		return out
	}

	plain, _ := buildWithOptimize(t, optimizeFixture, false, nil)
	optimized, rep := buildWithOptimize(t, optimizeFixture, true, nil)
	if rep.MergedIOGAMs == 0 {
		t.Fatalf("fixture did not exercise merging: %s", rep)
	}
	plainErrors := countErrors(plain)
	optErrors := countErrors(optimized)
	// Merging folds several GAMs into one, so message counts may drop
	// (fewer GAMs to report). No new *kind* of error may appear, and the
	// optimized config must not raise more errors than the plain one.
	total := func(m map[string]int) int {
		n := 0
		for _, c := range m {
			n += c
		}
		return n
	}
	if total(optErrors) > total(plainErrors) {
		t.Errorf("optimized output has more errors (%d) than plain (%d):\nplain: %v\noptimized: %v",
			total(optErrors), total(plainErrors), plainErrors, optErrors)
	}
	for msg := range optErrors {
		if _, known := plainErrors[msg]; !known {
			t.Errorf("optimized output introduced a new error kind: %q", msg)
		}
	}
}

// quotedName matches a single-quoted name inside a diagnostic message.
var quotedName = regexp.MustCompile(`'[^']*'`)

// normalizeMessage removes quoted names so messages from different GAM
// names compare equal.
func normalizeMessage(msg string) string {
	return quotedName.ReplaceAllString(msg, "''")
}

// DataSources whose layout is fixed by an external API or hardware keep
// their signal order: FileReader/Writer, SDN and hardware classes.
func TestOptimizeSkipsFixedOrderDataSources(t *testing.T) {
	src := `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +F = {
      Class = FileReader
      Signals = {
        Z = { Type = uint32 }
        A = { Type = uint32 }
        B = { Type = uint32 }
      }
    }
    +S = {
      Class = SDNSubscriber
      Signals = {
        Z = { Type = uint32 }
        A = { Type = uint32 }
        B = { Type = uint32 }
      }
    }
    +HW = {
      Class = NI6259ADC
      Signals = {
        Z = { Type = uint32 }
        A = { Type = uint32 }
        B = { Type = uint32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Use = {
      Class = MathExpressionGAM
      InputSignals = {
        B = { DataSource = F }
        A = { DataSource = F }
      }
      OutputSignals = { Z = { DataSource = S } }
    }
  }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        CPUs = 0x1
        Functions = { Use }
      }
    }
  }
}
`
	out, rep := buildWithOptimize(t, src, true, nil)
	if rep.ReorderedDataSources != 0 {
		t.Errorf("reordered %d fixed-layout data source(s): %s", rep.ReorderedDataSources, rep)
	}
	if rep.FixedOrderSources != 3 {
		t.Errorf("FixedOrderSources = %d, want 3: %s", rep.FixedOrderSources, rep)
	}
	for _, block := range []string{"+F", "+S", "+HW"} {
		start := strings.Index(out, block+" = {")
		if start < 0 {
			t.Fatalf("%s not found in output:\n%s", block, out)
		}
		signals := out[start:]
		signals = signals[:strings.Index(signals, "\n      }")]
		posZ := strings.Index(signals, "Z = {")
		posA := strings.Index(signals, "A = {")
		posB := strings.Index(signals, "B = {")
		if !(posZ < posA && posA < posB) {
			t.Errorf("%s: signal order changed (Z=%d A=%d B=%d):\n%s", block, posZ, posA, posB, signals)
		}
	}
}

// EPICS and GAM data sources are reordered: their storage is internal and
// signals are addressed by name.
func TestOptimizeReordersEPICSDataSource(t *testing.T) {
	src := `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +E = {
      Class = EPICSCAInput
      Signals = {
        Z = { Type = uint32 }
        A = { Type = uint32 }
        B = { Type = uint32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Use = {
      Class = MathExpressionGAM
      InputSignals = {
        B = { DataSource = E }
        A = { DataSource = E }
      }
      OutputSignals = { Z = { DataSource = E } }
    }
  }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = {
        Class = RealTimeThread
        CPUs = 0x1
        Functions = { Use }
      }
    }
  }
}
`
	out, rep := buildWithOptimize(t, src, true, nil)
	if rep.ReorderedDataSources != 1 {
		t.Fatalf("EPICS data source not reordered: %s\n%s", rep, out)
	}
	start := strings.Index(out, "+E = {")
	signals := out[start:]
	signals = signals[:strings.Index(signals, "\n      }")]
	posZ := strings.Index(signals, "Z = {")
	posA := strings.Index(signals, "A = {")
	posB := strings.Index(signals, "B = {")
	if !(posA < posZ && posB < posZ) {
		t.Errorf("consumed signals not moved before the unconsumed one (A=%d B=%d Z=%d):\n%s", posA, posB, posZ, signals)
	}
}

// Copy pairs with intra-GAM dependencies (A->B and B->C written by the same
// IOGAM) must not be reordered.
func TestOptimizeKeepsIOGAMCopyWithDependencies(t *testing.T) {
	src := `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +E = {
      Class = EPICSCAInput
      Signals = {
        A = { Type = uint32 }
        B = { Type = uint32 }
        C = { Type = uint32 }
      }
    }
    +G = {
      Class = GAMDataSource
      Signals = {
        C = { Type = uint32 }
        A = { Type = uint32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Copy = {
      Class = IOGAM
      InputSignals = {
        A = { DataSource = E }
        B = { DataSource = G }
      }
      OutputSignals = {
        B = { DataSource = G }
        C = { DataSource = E }
      }
    }
  }
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
	out, rep := buildWithOptimize(t, src, true, nil)
	if rep.ReorderedGAMSignals != 0 {
		t.Errorf("IOGAM with dependent copies was reordered: %s\n%s", rep, out)
	}
	iogam := out[strings.Index(out, "Class = IOGAM"):]
	if strings.Index(iogam, "A = {") > strings.Index(iogam, "B = {") {
		t.Errorf("input order changed despite the A->B, B->C dependency:\n%s", iogam)
	}
}
