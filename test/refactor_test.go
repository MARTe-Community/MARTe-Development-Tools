package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/refactor"
)

// refactorSource rewrites one source string and returns the formatted result.
func refactorSource(t *testing.T, src string, types map[string]refactor.SignalType) string {
	t.Helper()
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	refactor.Config(cfg, types, refactor.Options{
		CompactSignals: true,
		CopyGroups:     true,
		Pragmas:        refactor.SourcePragmas(src),
	})
	var buf bytes.Buffer
	formatter.Format(cfg, &buf)
	return buf.String()
}

func TestRefactorCompactsSignals(t *testing.T) {
	src := `#package T

+DDB = {
  Class = GAMDataSource
  Signals = {
    A = {
      Type = uint32
    }
    B = {
      Type = uint8
      NumberOfElements = 4
      Value = 1
    }
  }
}

+App = {
  Class = RealTimeApplication
  +Functions = {
    Class = ReferenceContainer
    +G = {
      Class = FilterGAM
      InputSignals = {
        Local = {
          DataSource = DDB
          Alias = Remote
          Type = float32
          Samples = 2
        }
      }
      OutputSignals = {
        Y = {
          DataSource = DDB
          Type = float32
        }
      }
    }
  }
}
`
	out := refactorSource(t, src, nil)
	for _, want := range []string{
		"A: uint32",
		"B: uint8[4] = {",
		"Value = 1",
		"DDB::Remote: float32 as Local = {",
		"Samples = 2",
		"DDB::Y: float32",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in refactored output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "DataSource = DDB") {
		t.Errorf("object-form signal entries left behind:\n%s", out)
	}

	// Idempotent.
	again := refactorSource(t, out, nil)
	if again != out {
		t.Errorf("refactoring is not idempotent:\nfirst:\n%s\nsecond:\n%s", out, again)
	}
}

func TestRefactorIOGAMToCopyGroup(t *testing.T) {
	src := `#package T

+DDB = {
  Class = GAMDataSource
  Signals = {
    A = { Type = uint32 }
    B = { Type = uint8 }
    C = { Type = uint8 }
    D = { Type = uint32 }
    E = { Type = uint16 }
  }
}

+OUT = {
  Class = GAMDataSource
  Signals = {
    D = { Type = uint32 }
    E = { Type = uint16 }
  }
}

+App = {
  Class = RealTimeApplication
  +Functions = {
    Class = ReferenceContainer
    +Simple = {
      Class = IOGAM
      InputSignals = {
        A = {
          DataSource = DDB
          Type = uint32
        }
      }
      OutputSignals = {
        D = {
          DataSource = OUT
          Type = uint32
        }
      }
    }
    +Packed = {
      Class = IOGAM
      InputSignals = {
        A = {
          DataSource = DDB
          Type = uint32
        }
        B = {
          DataSource = DDB
          Type = uint8
        }
        C = {
          DataSource = DDB
          Type = uint8
        }
      }
      OutputSignals = {
        D = {
          DataSource = OUT
          Type = uint32
        }
        E = {
          DataSource = OUT
          Type = uint16
        }
      }
    }
  }
}
`
	types := map[string]refactor.SignalType{}
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	types = refactor.CollectTypes([]*parser.Configuration{cfg})
	out := refactorSource(t, src, types)

	if strings.Contains(out, "Class = IOGAM") {
		t.Errorf("IOGAMs were not converted:\n%s", out)
	}
	if !strings.Contains(out, "copy Simple {") || !strings.Contains(out, "DDB::A { Type = uint32 } -> OUT::D { Type = uint32 }") {
		t.Errorf("1:1 copy group missing:\n%s", out)
	}
	if !strings.Contains(out, "copy Packed {") {
		t.Fatalf("packed copy group missing:\n%s", out)
	}
	group := copyBlock(t, out, "Packed")
	// A(uint32) pairs with D(uint32); the two uint8 signals are grouped
	// into the uint16 output.
	if !strings.Contains(group, "(DDB::B { Type = uint8 }, DDB::C { Type = uint8 }) -> (OUT::E { Type = uint16 })") {
		t.Errorf("grouped endpoints missing:\n%s", group)
	}
}

// copyBlock returns the `copy Name { … }` block.
func copyBlock(t *testing.T, out, name string) string {
	t.Helper()
	start := strings.Index(out, "copy "+name+" {")
	if start < 0 {
		t.Fatalf("copy %s not found:\n%s", name, out)
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
	t.Fatalf("unterminated copy block %s", name)
	return ""
}

// refactorTree mirrors a project into a temp directory using the refactor
// tool, preserving relative paths.
func refactorTree(t *testing.T, srcDir string) string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".marte" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	configs := make([]*parser.Configuration, 0, len(files))
	sources := make([]string, 0, len(files))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		cfg, err := parser.NewParser(string(b)).Parse()
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		configs = append(configs, cfg)
		sources = append(sources, string(b))
	}
	types := refactor.CollectTypes(configs)

	outDir := t.TempDir()
	for i, cfg := range configs {
		refactor.Config(cfg, types, refactor.Options{
			CompactSignals: true,
			CopyGroups:     true,
			Pragmas:        refactor.SourcePragmas(sources[i]),
		})
		var buf bytes.Buffer
		formatter.Format(cfg, &buf)
		rel, err := filepath.Rel(srcDir, files[i])
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		outPath := filepath.Join(outDir, rel)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return outDir
}

// The refactor tool must turn the real project into an equivalent
// configuration (same semantics as the hand-written one).
func TestRefactorBigProjectEquivalence(t *testing.T) {
	orig := buildProjectDir(t, "../examples/big_project")
	refDir := refactorTree(t, "../examples/big_project")
	ref := buildProjectDir(t, refDir)

	origLines := canonicalConfig(t, orig)
	refLines := canonicalConfig(t, ref)
	if strings.Join(origLines, "\n") != strings.Join(refLines, "\n") {
		max := len(origLines)
		if len(refLines) < max {
			max = len(refLines)
		}
		for i := 0; i < max; i++ {
			if origLines[i] != refLines[i] {
				t.Fatalf("refactored config differs at line %d:\n original: %s\nrefactored: %s", i+1, origLines[i], refLines[i])
			}
		}
		t.Fatalf("refactored config differs in size: %d vs %d entries", len(origLines), len(refLines))
	}

	// No IOGAM objects remain, and the tool is idempotent on its own output.
	var iogamCount int
	err := filepath.WalkDir(refDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(path)
		iogamCount += strings.Count(string(b), "Class = IOGAM")
		return nil
	})
	if err != nil {
		t.Fatalf("walk refactored: %v", err)
	}
	if iogamCount != 0 {
		t.Errorf("%d IOGAM definitions remain after refactoring", iogamCount)
	}

	again := refactorTree(t, refDir)
	compareTrees(t, refDir, again)
}

// compareTrees fails when two mirrored trees differ byte-wise.
func compareTrees(t *testing.T, a, b string) {
	t.Helper()
	err := filepath.WalkDir(a, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(a, path)
		want, _ := os.ReadFile(path)
		got, rerr := os.ReadFile(filepath.Join(b, rel))
		if rerr != nil {
			t.Errorf("missing %s in second pass: %v", rel, rerr)
			return nil
		}
		if !bytes.Equal(want, got) {
			t.Errorf("refactoring is not idempotent for %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
