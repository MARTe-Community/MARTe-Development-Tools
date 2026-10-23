package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// validateLowered parses src, expands copy/wiring sugar (as mdt check does)
// and returns the validation diagnostics.
func validateLowered(t *testing.T, src string) []validator.Diagnostic {
	t.Helper()
	pt := index.NewProjectTree()
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pt.AddFile("main.marte", cfg)
	pt.ResolveReferences(nil)
	pt.ResolveFields(nil)
	if _, err := builder.Lower(pt); err != nil {
		t.Fatalf("lower: %v", err)
	}
	v := validator.NewValidator(pt, ".", nil)
	v.ValidateProject(context.Background())
	return v.Diagnostics
}

// sizeFixture builds a project with one copy pair whose byte sizes depend on
// the given signals and copy options.
func sizeFixture(srcSignal, dstSignal, srcOpts, dstOpts string) string {
	return fmt.Sprintf(`#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = SRC
    +SRC = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        AI0 = { %s }
      }
    }
    +DST = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        AI0 = { %s }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Gen = {
      Class = ConstantGAM
      OutputSignals = {
        AI0 = { DataSource = SRC  Type = float32 }
      }
    }
    copy Links {
      SRC::AI0 %s -> DST::AI0 %s
    }
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = SRC }
  +States = {
    Class = ReferenceContainer
    +Run = { Class = RealTimeState  +Th = { Class = RealTimeThread  CPUs = 0x1  Functions = { Gen Links } } }
  }
}
`, srcSignal, dstSignal, srcOpts, dstOpts)
}

// TestIOGAMSizeMismatchDiagnostic pins the detailed mismatch report: the
// generic CUE "conflicting values" message is replaced by one that names the
// per-signal derivation on both sides.
func TestIOGAMSizeMismatchDiagnostic(t *testing.T) {
	src := sizeFixture(
		"Type = float32  NumberOfElements = 100",
		"Type = float32",
		"", "",
	)
	diags := validateLowered(t, src)

	var sizeErr string
	for _, d := range diags {
		if strings.Contains(d.Message, "InputSize") || strings.Contains(d.Message, "OutputSize") {
			t.Errorf("generic CUE size message not suppressed: %s", d.Message)
		}
		if d.Level == validator.LevelError && strings.Contains(d.Message, "IOGAM") {
			sizeErr = d.Message
		}
	}
	if sizeErr == "" {
		t.Fatalf("no IOGAM size diagnostic, got %+v", diags)
	}
	for _, want := range []string{
		"input memory is 400 bytes",
		"output memory is 4 bytes",
		"AI0 float32×100=400",
		"AI0 float32×1=4",
	} {
		if !strings.Contains(sizeErr, want) {
			t.Errorf("size diagnostic missing %q:\n%s", want, sizeErr)
		}
	}
}

// TestIOGAMSizeMatchedIsSilent: equal memories on both sides report nothing,
// even with NumberOfElements and Samples in play.
func TestIOGAMSizeMatchedIsSilent(t *testing.T) {
	src := sizeFixture(
		"Type = float32  NumberOfElements = 100",
		"Type = float32  NumberOfElements = 100",
		"{ Samples = 10 }", "{ Samples = 10 }",
	)
	for _, d := range validateLowered(t, src) {
		if d.Level == validator.LevelError {
			t.Errorf("unexpected error for matched sizes: %s", d.Message)
		}
	}
}

// TestSamplesMultiplierWarning explains a doubled size: Samples on an entry
// whose DataSource signal already declares NumberOfElements.
func TestSamplesMultiplierWarning(t *testing.T) {
	src := sizeFixture(
		"Type = float32  NumberOfElements = 100",
		"Type = float32  NumberOfElements = 100",
		"", "",
	)
	// Add Samples to the source entry only: 100 elements × 100 samples.
	src = strings.Replace(src, "SRC::AI0", "SRC::AI0 { Samples = 100 }", 1)

	found := ""
	for _, d := range validateLowered(t, src) {
		if strings.Contains(d.Message, "Samples=100 multiplies") {
			found = d.Message
		}
	}
	if found == "" {
		t.Fatal("no Samples multiplier warning")
	}
	for _, want := range []string{"'SRC::AI0'", "100 elements", "100×100 elements", "40000 bytes"} {
		if !strings.Contains(found, want) {
			t.Errorf("warning missing %q:\n%s", want, found)
		}
	}
}

// TestIOGAMSizeMismatchFromTypo: a misspelled NumberOfElements leaves the
// destination without a size, which the diagnostic now spells out instead of
// only reporting the CUE conflict.
func TestIOGAMSizeMismatchFromTypo(t *testing.T) {
	src := sizeFixture(
		"Type = float32  NumberOfElements = 100",
		"Type = float32  NumberOfElememnts = 100", // typo
		"", "",
	)
	found := false
	for _, d := range validateLowered(t, src) {
		if strings.Contains(d.Message, "IOGAM") && strings.Contains(d.Message, "AI0 float32×1=4") {
			found = true
		}
	}
	if !found {
		t.Fatal("typo-induced size mismatch not explained")
	}
}
