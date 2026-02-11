package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

func TestLSPINOUTWarning(t *testing.T) {
	lsp.ResetTestServer()
	// Documents reset via ResetTestServer
	lsp.GlobalSchema = schema.LoadFullSchema(".")

	var buf bytes.Buffer
	lsp.Output = &buf

	content := `
+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +DDB = {
      Class = GAMDataSource
    }
  }
  +Functions = {
    Class = ReferenceContainer
    +Producer = {
      Class = IOGAM
      OutputSignals = {
        ProducedSig = {
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
          Functions = {Producer}
        }
      }
    }
  }
}
`
	uri := "file://warning.marte"
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, Text: content},
	})

	output := buf.String()
	if !strings.Contains(output, "produced in thread '+Th1' but never consumed") {
		t.Error("LSP did not report INOUT usage warning")
		t.Log(output)
	}
}
