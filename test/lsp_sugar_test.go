package integration

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// sugarFixture exercises copy groups, pipelines and `from` wiring.
const sugarFixture = `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +TDS = { Class = TimingDataSource }
    +DDB = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        A = { Type = uint32 }
        B = { Type = float32 }
      }
    }
    +OUT = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        C = { Type = uint32 }
      }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    copy Links {
      DDB::A -> OUT::C
    }
    +G = {
      Class = FilterGAM
      InputSignals = { In = { DataSource = OUT } }
      OutputSignals = { Y = { DataSource = OUT } }
    }
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = TDS }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = { Class = RealTimeThread  CPUs = 0x1  Functions = { Links G } }
    }
  }
}

gam Preprocess {
  in Raw: uint32
  out Clean: uint32

  +S = {
    Class = FilterGAM
    InputSignals = { X = { DataSource = DDB } }
    OutputSignals = { Y = { DataSource = OUT } }
  }
  from Raw -> S.X
  from S.Y -> Clean
}

+Inst = {
  Class = Preprocess
  Raw = DDB::A
  Clean = OUT::C
}
`

func setupSugar(t *testing.T) string {
	t.Helper()
	lsp.ResetTestServer()
	uri := "file://sugar.marte"
	lsp.GetTestDocuments()[uri] = sugarFixture
	cfg, err := parser.NewParser(sugarFixture).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	lsp.GetTestTree().AddFile("sugar.marte", cfg)
	lsp.GetTestTree().ResolveReferences(nil)
	return uri
}

// lineOf returns the 0-based line containing substr.
func lineOf(t *testing.T, substr string) (int, int) {
	t.Helper()
	for i, line := range strings.Split(sugarFixture, "\n") {
		if idx := strings.Index(line, substr); idx >= 0 {
			return i, idx
		}
	}
	t.Fatalf("%q not found", substr)
	return 0, 0
}

// Hovering a copy endpoint shows the resolved DataSource/signal or GAM entry.
func TestLSPCopySugarHover(t *testing.T) {
	uri := setupSugar(t)

	line, col := lineOf(t, "DDB::A -> OUT::C")
	h := lsp.HandleHover(lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: line, Character: col + 1}, // inside DDB
	})
	if h == nil {
		t.Fatal("no hover for DS endpoint")
	}
	content := h.Contents.(lsp.MarkupContent).Value
	for _, want := range []string{"DataSource", "DDB", "GAMDataSource", "uint32"} {
		if !strings.Contains(content, want) {
			t.Errorf("hover %q lacks %q", content, want)
		}
	}

	// GAM endpoint inside a pipeline body (`S.X` is an input).
	line, col = lineOf(t, "Raw -> S.X")
	h = lsp.HandleHover(lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: line, Character: col + 7}, // on S.X
	})
	if h == nil {
		t.Fatal("no hover for GAM endpoint")
	}
	content = h.Contents.(lsp.MarkupContent).Value
	for _, want := range []string{"GAM", "S", "Input", "DDB"} {
		if !strings.Contains(content, want) {
			t.Errorf("hover %q lacks %q", content, want)
		}
	}
}

// Pipeline instance hover shows the macro and its ports.
func TestLSPPipelineHover(t *testing.T) {
	uri := setupSugar(t)
	line, col := lineOf(t, "Class = Preprocess")
	h := lsp.HandleHover(lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: line, Character: col + 8},
	})
	if h == nil {
		t.Fatal("no hover for pipeline instance")
	}
	content := h.Contents.(lsp.MarkupContent).Value
	for _, want := range []string{"Pipeline", "Preprocess", "Raw", "Clean"} {
		if !strings.Contains(content, want) {
			t.Errorf("hover %q lacks %q", content, want)
		}
	}
}

// Go-to-definition resolves copy endpoints and pipeline classes.
func TestLSPCopySugarDefinition(t *testing.T) {
	uri := setupSugar(t)
	line, col := lineOf(t, "DDB::A -> OUT::C")

	locs, ok := lsp.HandleDefinition(lsp.DefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: line, Character: col + 1},
	}).([]lsp.Location)
	if !ok || len(locs) == 0 {
		t.Fatal("no definition for DS endpoint")
	}
	wantLine, _ := lineOf(t, "A = { Type = uint32 }")
	if locs[0].Range.Start.Line != wantLine {
		t.Errorf("definition line = %d, want %d (signal declaration)", locs[0].Range.Start.Line, wantLine)
	}

	// Pipeline class → gam definition.
	line, col = lineOf(t, "Class = Preprocess")
	locs, ok = lsp.HandleDefinition(lsp.DefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: line, Character: col + 8},
	}).([]lsp.Location)
	if !ok || len(locs) == 0 {
		t.Fatal("no definition for pipeline class")
	}
	startLine, _ := lineOf(t, "gam Preprocess {")
	if locs[0].Range.Start.Line != startLine {
		t.Errorf("definition line = %d, want %d (gam definition)", locs[0].Range.Start.Line, startLine)
	}
}

// Completion after `->` and `from ` suggests GAMs and DataSources.
func TestLSPCopySugarCompletion(t *testing.T) {
	uri := setupSugar(t)
	line, col := lineOf(t, "DDB::A -> OUT::C")

	list := lsp.HandleCompletion(lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: line, Character: col + len("DDB::A -> ")},
	})
	if list == nil {
		t.Fatal("no destination completions")
	}
	labels := map[string]bool{}
	for _, item := range list.Items {
		labels[item.Label] = true
	}
	if !labels["G"] || !labels["OUT"] || !labels["DDB"] {
		t.Errorf("destination completions missing GAM/DataSource names: %v", labels)
	}

	// `from ` inside the pipeline body.
	found := false
	for i, l := range strings.Split(sugarFixture, "\n") {
		if strings.Contains(l, "from Raw -> S.X") {
			list := lsp.HandleCompletion(lsp.CompletionParams{
				TextDocument: lsp.TextDocumentIdentifier{URI: uri},
				Position:     lsp.Position{Line: i, Character: len("  from ")},
			})
			if list == nil {
				t.Fatal("no source completions")
			}
			for _, item := range list.Items {
				if item.Label == "DDB" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("source completions missing DataSources")
	}
}

// Validation lowers the sugar: valid copies produce no ordering error, and a
// broken group reports a diagnostic.
func TestLSPSugarDiagnostics(t *testing.T) {
	lsp.ResetTestServer()
	lsp.GlobalSchema = nil

	var mu sync.Mutex
	diags := map[string][]lsp.LSPDiagnostic{}
	orig := lsp.PublishDiagnosticsFn
	defer func() { lsp.PublishDiagnosticsFn = orig }()
	lsp.PublishDiagnosticsFn = func(_ context.Context, uri string, ds []lsp.LSPDiagnostic) {
		mu.Lock()
		diags[uri] = append([]lsp.LSPDiagnostic{}, ds...)
		mu.Unlock()
	}

	content := `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +TDS = { Class = TimingDataSource }
    +DDB = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = {
        A = { Type = uint32 }
        B = { Type = float32 }
      }
    }
    +OUT = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = { C = { Type = uint32 } }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    copy Bad {
      (DDB::A, DDB::B) -> (OUT::C)
    }
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = TDS }
}
`
	uri := "file://sugar_diag.marte"
	lsp.GetTestDocuments()[uri] = content
	cfg, err := parser.NewParser(content).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	lsp.GetTestTree().AddFile("sugar_diag.marte", cfg)
	lsp.GetTestTree().ResolveReferences(nil)
	lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{
			{Text: content},
		},
	})

	mu.Lock()
	got := diags[uri]
	mu.Unlock()
	if len(got) == 0 {
		t.Fatal("no diagnostics published")
	}
	found := false
	for _, d := range got {
		if strings.Contains(d.Message, "moves") && strings.Contains(d.Message, "bytes") {
			found = true
		}
	}
	if !found {
		t.Errorf("size-mismatch diagnostic missing: %+v", got)
	}
}

var _ = bytes.MinRead
