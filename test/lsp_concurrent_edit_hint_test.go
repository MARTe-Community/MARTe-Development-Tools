package integration

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

// concurrentFixture exercises the shorthand path that made inlay hints call
// back into the tree (ResolveName for the "DDB::T" datasource prefix).
const concurrentFixture = `#package Conc
+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB
    +DDB = {
      Class = GAMDataSource
      Signals = {
        T: uint32
        Data: float64
      }
    }
    +TimingDataSource = {
      Class = TimingDataSource
    }
  }
  +Functions = {
    Class = ReferenceContainer
    GAM = {
      Class = IOGAM
      InputSignals = {
        DDB::T
      }
      OutputSignals = {
        DDB::Data
      }
    }
  }
  +States = {
    Class = ReferenceContainer
  }
  +Scheduler = {
    Class = GAMScheduler
    TimingDataSource = TimingDataSource
  }
}
`

// TestConcurrentEditAndInlayHintDoNotDeadlock reproduces the reported hang: an
// editor requests inlay hints while an edit is in flight. The hint handler
// iterated the tree with the read lock held and then called a getter that
// takes the read lock again; Go's RWMutex is not reentrant once a writer is
// queued, so the inner acquisition waited for the writer, the writer waited
// for the iteration to finish, and every later request hung behind them. The
// hint requests never returned (the server logged only
// "request textDocument/inlayHint still running after 3s").
func TestConcurrentEditAndInlayHintDoNotDeadlock(t *testing.T) {
	lsp.ResetTestServer()
	lsp.GlobalSchema = schema.LoadFullSchema(".")

	const uri = "file:///tmp/concurrent.marte"
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, Text: concurrentFixture},
	})

	const rounds = 60
	var wg sync.WaitGroup
	wg.Add(2)

	// Reader: inlay hints over the whole document, the request that hung.
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			lsp.HandleInlayHint(lsp.InlayHintParams{
				TextDocument: lsp.TextDocumentIdentifier{URI: uri},
				Range: lsp.Range{
					Start: lsp.Position{Line: 0, Character: 0},
					End:   lsp.Position{Line: 40, Character: 0},
				},
			})
		}
	}()

	// Writer: edits, each one replacing the document text through the tree.
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
				TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri, Version: i + 2},
				ContentChanges: []lsp.TextDocumentContentChangeEvent{{
					Range: &lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 0}},
					Text:  strings.Repeat("", 1),
				}},
			})
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("edit and inlay hint deadlocked: a reader holding the tree lock called back into the tree while a writer was queued")
	}
}
