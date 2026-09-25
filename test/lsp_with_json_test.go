package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

// The LSP must resolve names and types for signals whose definitions come
// from a document loaded with `with json(...)`:
//   - hover on `DS::Signal` shows the datasource-side type/PVName;
//   - go-to-definition jumps to the signal's definition site.
func TestLSPWithJSONSignalResolution(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "epics.json"), []byte(`{
		"signals": {
			"Stat": { "Type": "uint32", "PVName": "DEMO:STAT" }
		}
	}`), 0644)

	const src = `#package T
+RTApp = {
  Class = RealTimeApplication
  Data = {
    Class = ReferenceContainer
    with json("epics.json") as epics_cfg begin
      +EpicsSignals = {
        Class = EPICSCAInput
        Signals = {
          foreach name, object in @epics_cfg.signals
            @name = {
              Type = @object.Type
              PVName = @object.PVName
            }
          #end
        }
      }
    end
    DefaultDataSource = DDB
    DDB = {
      Class = GAMDataSource
    }
    Timing = {
      Class = TimingDataSource
    }
  }
  Functions = {
    Class = ReferenceContainer
    GAM = {
      Class = IOGAM
      InputSignals = {
        EpicsSignals::Stat
      }
      OutputSignals = {
        DDB::Test: uint32
      }
    }
  }
  States = {
    Class = ReferenceContainer
  }
  Scheduler = {
    Class = GAMScheduler
    TimingDataSource = Timing
  }
}`
	mainPath := filepath.Join(dir, "main.marte")
	if err := os.WriteFile(mainPath, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	lsp.SynchronousValidation = false
	lsp.ResetTestServer()
	lsp.GlobalSchema = schema.LoadFullSchema(dir)
	initParams, _ := json.Marshal(lsp.InitializeParams{RootURI: "file://" + dir})
	lsp.HandleMessage(&lsp.JsonRpcMessage{Method: "initialize", ID: 1, Params: initParams})

	uri := "file://" + mainPath
	openParams, _ := json.Marshal(lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, Text: src},
	})
	lsp.HandleMessage(&lsp.JsonRpcMessage{Method: "textDocument/didOpen", Params: openParams})

	// Locate the shorthand and the signal name inside it.
	lines := strings.Split(src, "\n")
	shLine := -1
	for i, l := range lines {
		if strings.Contains(l, "EpicsSignals::Stat") {
			shLine = i
		}
	}
	if shLine < 0 {
		t.Fatal("shorthand line not found")
	}
	sigCol := strings.Index(lines[shLine], "Stat")

	hover := lsp.HandleHover(lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: shLine, Character: sigCol + 1},
	})
	if hover == nil {
		t.Fatal("no hover for signal usage")
	}
	md, ok := hover.Contents.(lsp.MarkupContent)
	if !ok {
		t.Fatalf("unexpected hover contents: %#v", hover.Contents)
	}
	for _, want := range []string{"**Type**: `uint32`", "**DataSource**: `EpicsSignals`", "**PVName**: `\"DEMO:STAT\"`"} {
		if !strings.Contains(md.Value, want) {
			t.Errorf("hover missing %q, got:\n%s", want, md.Value)
		}
	}

	def := lsp.HandleDefinition(lsp.DefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: shLine, Character: sigCol + 1},
	})
	locs, ok := def.([]lsp.Location)
	if !ok || len(locs) == 0 {
		t.Fatalf("expected definition locations, got %#v", def)
	}
	if locs[0].Range.Start.Line == shLine {
		t.Errorf("definition should not point at the usage line (%d)", shLine)
	}

	// Inlay hints: the shorthand must show the type but not repeat the
	// datasource (it is already written as DS::Signal).
	hints := lsp.HandleInlayHint(lsp.InlayHintParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
	})
	var shorthandHintLabels []string
	for _, h := range hints {
		if h.Position.Line == shLine {
			shorthandHintLabels = append(shorthandHintLabels, h.Label)
		}
	}
	if len(shorthandHintLabels) == 0 {
		t.Error("expected an inlay hint on the shorthand line")
	}
	joined := strings.Join(shorthandHintLabels, " ")
	if !strings.Contains(joined, "uint32") {
		t.Errorf("shorthand hint should show the resolved type, got %q", joined)
	}
	if strings.Contains(joined, "EPICSCAInput") {
		t.Errorf("shorthand hint should not repeat the datasource class, got %q", joined)
	}

	// The datasource reference itself must resolve too.
	dsCol := strings.Index(lines[shLine], "EpicsSignals")
	dsHover := lsp.HandleHover(lsp.HoverParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: shLine, Character: dsCol + 2},
	})
	if dsHover == nil {
		t.Fatal("no hover for datasource reference")
	}
	if v, ok := dsHover.Contents.(lsp.MarkupContent); !ok || !strings.Contains(v.Value, "EPICSCAInput") {
		t.Errorf("datasource hover missing class: %#v", dsHover.Contents)
	}
}
