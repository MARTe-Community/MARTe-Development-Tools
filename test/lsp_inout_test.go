package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

func TestLSPINOUTOrdering(t *testing.T) {
	lsp.Tree = index.NewProjectTree()
	lsp.Documents = make(map[string]string)
	// Mock schema if necessary, but we rely on internal schema
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
    +A = {
      Class = IOGAM
      InputSignals = {
        A = {
          DataSource = DDB
          Type = uint32
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
}
`
	uri := "file://app.marte"
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, Text: content},
	})

	output := buf.String()
	if !strings.Contains(output, "INOUT Signal 'A'") {
		t.Error("LSP did not report INOUT ordering error")
		t.Log(output)
	}
}
