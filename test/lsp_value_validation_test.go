package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

func TestLSPValueValidation(t *testing.T) {
	lsp.ResetTestServer()
	// Documents reset via ResetTestServer
	lsp.GlobalSchema = schema.LoadFullSchema(".")

	var buf bytes.Buffer
	lsp.Output = &buf

	content := `
+Data = {
    Class = ReferenceContainer
    +DS = { Class = GAMDataSource Signals = { S = { Type = uint8 } } }
}
+GAM = {
    Class = IOGAM
    InputSignals = {
        S = { DataSource = DS Type = uint8 Value = 1024 }
    }
}
+App = { Class = RealTimeApplication +States = { Class = ReferenceContainer +S = { Class = RealTimeState Threads = { +T = { Class = RealTimeThread Functions = { GAM } } } } } }
`
	uri := "file://value.marte"
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, Text: content},
	})

	output := buf.String()
	if !strings.Contains(output, "Value initialization mismatch") {
		t.Error("LSP did not report value validation error")
		t.Log(output)
	}
}
