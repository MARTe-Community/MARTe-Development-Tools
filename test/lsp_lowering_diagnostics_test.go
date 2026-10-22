package integration

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// loweringFixture uses copy sugar whose generated GAM is referenced from a
// thread. Validation runs on a lowered clone of the snapshot tree, so the
// clone's references have to be resolved again: otherwise the copy group (and
// every GAM it generates) looks unreferenced and the editor shows stale
// "Unused GAM"/ordering warnings that a reopen does not remove.
const loweringFixture = `#package T

+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +TDS = { Class = TimingDataSource }
    +DDB = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = { A = { Type = uint32 } }
    }
    +OUT = {
      Class = GAMDataSource
      AllowNoProducers = 1
      Signals = { C = { Type = uint32 } }
    }
  }
  +Functions = {
    Class = ReferenceContainer
    copy Links {
      DDB::A -> OUT::C
    }
    +Use = {
      Class = FilterGAM
      InputSignals = { C = { DataSource = OUT  Type = uint32 } }
      OutputSignals = { Y = { DataSource = OUT  Type = uint32 } }
    }
  }
  Scheduler = { Class = GAMScheduler  TimingDataSource = TDS }
  +States = {
    Class = ReferenceContainer
    +Run = {
      Class = RealTimeState
      +Th = { Class = RealTimeThread  CPUs = 0x1  Functions = { Links Use } }
    }
  }
}
`

func TestLSPDiagnosticsResolveLoweredSugar(t *testing.T) {
	lsp.ResetTestServer()
	lsp.GlobalSchema = nil

	var mu sync.Mutex
	published := map[string][]lsp.LSPDiagnostic{}
	orig := lsp.PublishDiagnosticsFn
	defer func() { lsp.PublishDiagnosticsFn = orig }()
	lsp.PublishDiagnosticsFn = func(_ context.Context, uri string, ds []lsp.LSPDiagnostic) {
		mu.Lock()
		published[uri] = append([]lsp.LSPDiagnostic{}, ds...)
		mu.Unlock()
	}

	const uri = "file://lowering.marte"
	lsp.GetTestDocuments()[uri] = loweringFixture
	cfg, err := parser.NewParser(loweringFixture).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	lsp.GetTestTree().AddFile("lowering.marte", cfg)
	lsp.GetTestTree().ResolveReferences(nil)
	lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{Text: loweringFixture}},
	})

	mu.Lock()
	got := published[uri]
	mu.Unlock()

	for _, d := range got {
		if strings.Contains(d.Message, "Unused GAM") {
			t.Errorf("lowered sugar reported as unused: %s", d.Message)
		}
	}
}
