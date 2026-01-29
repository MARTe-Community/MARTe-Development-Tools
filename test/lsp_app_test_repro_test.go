package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

func TestLSPAppTestRepro(t *testing.T) {
	lsp.Tree = index.NewProjectTree()
	lsp.Documents = make(map[string]string)
	lsp.GlobalSchema = schema.LoadFullSchema(".")

	var buf bytes.Buffer
	lsp.Output = &buf

	content := `+App = {
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
    +FnA = {
      Class = IOGAM
      InputSignals = {
        A = {
          DataSource = DDB
          Type = uint32
          Value = $Value
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
      Class = RealTimeState
      Threads = {
        +Th1 = {
          Class = RealTimeThread
          Functions = { FnA }
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
	uri := "file://examples/app_test.marte"
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, Text: content},
	})

	output := buf.String()

	// Check Unresolved Variable
	if !strings.Contains(output, "Unresolved variable reference: '$Value'") {
		t.Error("LSP missing unresolved variable error")
	}

	// Check INOUT consumed but not produced
	if !strings.Contains(output, "consumed by GAM '+FnA'") {
		t.Error("LSP missing consumed but not produced error")
	}
    
    if t.Failed() {
        t.Log(output)
    }
}
