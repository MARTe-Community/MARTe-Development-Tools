package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

func TestLSPValidationThreading(t *testing.T) {
	// Setup
	lsp.Tree = index.NewProjectTree()
	lsp.Documents = make(map[string]string)
	lsp.ProjectRoot = "."
	lsp.GlobalSchema = schema.NewSchema() // Empty schema but not nil

	// Capture Output
	var buf bytes.Buffer
	lsp.Output = &buf

	content := `
+Data = {
    Class = ReferenceContainer
    +SharedDS = {
        Class = GAMDataSource
        #meta = {
            direction = "INOUT"
            multithreaded = false
        }
        Signals = {
            Sig1 = { Type = uint32 }
        }
    }
}
+GAM1 = { Class = IOGAM InputSignals = { Sig1 = { DataSource = SharedDS Type = uint32 } } }
+GAM2 = { Class = IOGAM OutputSignals = { Sig1 = { DataSource = SharedDS Type = uint32 } } }
+App = {
    Class = RealTimeApplication
    +States = {
        Class = ReferenceContainer
        +State1 = {
            Class = RealTimeState
            +Thread1 = { Class = RealTimeThread Functions = { GAM1 } }
            +Thread2 = { Class = RealTimeThread Functions = { GAM2 } }
        }
    }
}
`
	uri := "file://threading.marte"

	// Call HandleDidOpen directly
	params := lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:  uri,
			Text: content,
		},
	}

	lsp.HandleDidOpen(params)

	// Check output
	output := buf.String()
	
	// We look for publishDiagnostics notification
	if !strings.Contains(output, "textDocument/publishDiagnostics") {
		t.Fatal("Did not receive publishDiagnostics")
	}

	// We look for the specific error message
	expectedError := "DataSource '+SharedDS' is not multithreaded but used in multiple threads"
	if !strings.Contains(output, expectedError) {
		t.Errorf("Expected error '%s' not found in LSP output. Output:\n%s", expectedError, output)
	}
}
