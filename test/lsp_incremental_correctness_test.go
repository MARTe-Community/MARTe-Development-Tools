package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

func TestIncrementalCorrectness(t *testing.T) {
	// Documents reset via ResetTestServer
	uri := "file://test.txt"
	initial := "12345\n67890"
	lsp.GetTestDocuments()[uri] = initial

	// Edit 1: Insert "A" at 0:1 -> "1A2345\n67890"
	change1 := lsp.TextDocumentContentChangeEvent{
		Range: &lsp.Range{Start: lsp.Position{Line: 0, Character: 1}, End: lsp.Position{Line: 0, Character: 1}},
		Text:  "A",
	}
	lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{change1},
	})

	if lsp.GetTestDocuments()[uri] != "1A2345\n67890" {
		t.Errorf("Edit 1 failed: %q", lsp.GetTestDocuments()[uri])
	}

	// Edit 2: Delete newline (merge lines)
	// "1A2345\n67890" -> "1A234567890"
	// \n is at index 6. 
	// 0:6 points to \n? "1A2345" length is 6.
	// So 0:6 is AFTER '5', at '\n'.
	// 1:0 is AFTER '\n', at '6'.
	// Range 0:6 - 1:0 covers '\n'.
	change2 := lsp.TextDocumentContentChangeEvent{
		Range: &lsp.Range{Start: lsp.Position{Line: 0, Character: 6}, End: lsp.Position{Line: 1, Character: 0}},
		Text:  "",
	}
	lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{change2},
	})

	if lsp.GetTestDocuments()[uri] != "1A234567890" {
		t.Errorf("Edit 2 failed: %q", lsp.GetTestDocuments()[uri])
	}

	// Edit 3: Add newline at end
	// "1A234567890" len 11.
	// 0:11.
	change3 := lsp.TextDocumentContentChangeEvent{
		Range: &lsp.Range{Start: lsp.Position{Line: 0, Character: 11}, End: lsp.Position{Line: 0, Character: 11}},
		Text:  "\n",
	}
	lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{change3},
	})

	if lsp.GetTestDocuments()[uri] != "1A234567890\n" {
		t.Errorf("Edit 3 failed: %q", lsp.GetTestDocuments()[uri])
	}
}

func TestIncrementalAppValidation(t *testing.T) {
	// Setup
	lsp.ResetTestServer()
	// Documents reset via ResetTestServer
	lsp.GlobalSchema = schema.LoadFullSchema(".")
	var buf bytes.Buffer
	lsp.Output = &buf

	content := `// Test app
+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB
    +DDB = {
      Class = GAMDataSource
    }
    +TimingDataSource = {
      Class = TimingDataSource
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +A = {
      Class = IOGAM
      InputSignals = {
        A = {
          DataSource = DDB
          Type = uint32
          // Placeholder
        }
      }
      OutputSignals = {
        B = {
          DataSource = DDB
          Type = uint32
        }
      }
    }
  }
  +States = {
    Class = ReferenceContainer
    +State = {
      Class =RealTimeState
      Threads = {
        +Th1 = {
          Class = RealTimeThread
          Functions = {A}
        }
      }
    }
  }
  +Scheduler = {
    Class = GAMScheduler
    TimingDataSource = TimingDataSource
  }
}
`
	uri := "file://app_inc.marte"

	// 1. Open
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, Text: content},
	})

	out := buf.String()

	// Signal A is never produced. Should have consumed error.
	if !strings.Contains(out, "ERROR: INOUT Signal 'A'") {
		t.Error("Missing consumed error for A")
	}
	// Signal B is Output, never consumed.
	if !strings.Contains(out, "WARNING: INOUT Signal 'B'") {
		t.Error("Missing produced error for B")
	}

	buf.Reset()

	// 2. Insert comment at start
	// Expecting same errors
	change1 := lsp.TextDocumentContentChangeEvent{
		Range: &lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 0}},
		Text:  "// Comment\n",
	}
	lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{change1},
	})

	out = buf.String()
	// Signal A is never produced. Should have consumed error.
	if !strings.Contains(out, "ERROR: INOUT Signal 'A'") {
		t.Error("Missing consumed error for A")
	}
	// Signal B is Output, never consumed.
	if !strings.Contains(out, "WARNING: INOUT Signal 'B'") {
		t.Error("Missing produced error for B")
	}

	buf.Reset()

	// 3. Add Value to A
	currentText := lsp.GetTestDocuments()[uri]
	idx := strings.Index(currentText, "Placeholder")
	if idx == -1 {
		t.Fatal("Could not find anchor string")
	}

	idx = strings.Index(currentText[idx:], "\n") + idx
	insertPos := idx + 1

	line, char := offsetToLineChar(currentText, insertPos)

	change2 := lsp.TextDocumentContentChangeEvent{
		Range: &lsp.Range{Start: lsp.Position{Line: line, Character: char}, End: lsp.Position{Line: line, Character: char}},
		Text:  "Value = 10\n",
	}

	lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
		TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{change2},
	})

	out = buf.String()

	// Signal A has now a Value field and so it is produced. Should NOT have consumed error.
	if strings.Contains(out, "ERROR: INOUT Signal 'A'") {
		t.Error("Unexpected consumed error for A")
	}
	// Signal B is Output, never consumed.
	if !strings.Contains(out, "WARNING: INOUT Signal 'B'") {
		t.Error("Missing produced error for B")
	}

}
