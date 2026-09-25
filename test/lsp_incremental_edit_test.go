package integration

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

// typeText applies one content change per rune, advancing the cursor the way
// an editor does after each keystroke (newline moves to the next line).
func typeText(t *testing.T, uri, insert string, line, col int) {
	t.Helper()
	for i := 0; i < len(insert); i++ {
		ch := insert[i]
		lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
			TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri},
			ContentChanges: []lsp.TextDocumentContentChangeEvent{{
				Range: &lsp.Range{
					Start: lsp.Position{Line: line, Character: col},
					End:   lsp.Position{Line: line, Character: col},
				},
				Text: string(ch),
			}},
		})
		if ch == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
}

func changeAt(uri string, line, col int, text string) lsp.DidChangeTextDocumentParams {
	return lsp.DidChangeTextDocumentParams{
		TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri},
		ContentChanges: []lsp.TextDocumentContentChangeEvent{{
			Range: &lsp.Range{
				Start: lsp.Position{Line: line, Character: col},
				End:   lsp.Position{Line: line, Character: col},
			},
			Text: text,
		}},
	}
}

// TestIncrementalEditPreservesDocumentText pins the incremental sync
// arithmetic: a wrong offset silently corrupts the document, which then makes
// the parser report errors for text the user never wrote (they vanish on
// reopen, when the editor resends the whole file).
func TestIncrementalEditPreservesDocumentText(t *testing.T) {
	const uri = "file://test/incremental.marte"

	t.Run("typing characters and newlines", func(t *testing.T) {
		lsp.ResetTestServer()
		initial := "#package T\n+App = {\n  Signals = {\n    T: uint32\n  }\n}\n"
		lsp.GetTestDocuments()[uri] = initial
		// Type "    F: float32" on the line holding "T: uint32".
		typeText(t, uri, "\n    F: float32", 3, len("    T: uint32"))
		want := "#package T\n+App = {\n  Signals = {\n    T: uint32\n    F: float32\n  }\n}\n"
		if got := lsp.GetTestDocuments()[uri]; got != want {
			t.Errorf("document text diverged\nwant:\n%q\ngot:\n%q", want, got)
		}
	})

	t.Run("columns after multibyte characters", func(t *testing.T) {
		lsp.ResetTestServer()
		// The em dash is 3 UTF-8 bytes but a single UTF-16 code unit, so a
		// byte-based column would insert in the wrong place.
		initial := "// header — dash\nX = 1\n"
		lsp.GetTestDocuments()[uri] = initial
		lsp.HandleDidChange(changeAt(uri, 1, 5, "2"))
		want := "// header — dash\nX = 12\n"
		if got := lsp.GetTestDocuments()[uri]; got != want {
			t.Errorf("want %q, got %q", want, got)
		}
	})

	t.Run("columns count surrogate pairs as two units", func(t *testing.T) {
		lsp.ResetTestServer()
		initial := "// emoji 😀 here\nX = 1\n"
		lsp.GetTestDocuments()[uri] = initial
		// Line 0 holds "// emoji 😀 here" = 16 UTF-16 units.
		lsp.HandleDidChange(changeAt(uri, 1, 0, "Y = 2\n"))
		want := "// emoji 😀 here\nY = 2\nX = 1\n"
		if got := lsp.GetTestDocuments()[uri]; got != want {
			t.Errorf("want %q, got %q", want, got)
		}
		// "// emoji " is 9 UTF-16 units, the emoji itself two, so column 11
		// is immediately after it. A byte- or rune-based column would land
		// inside the surrogate pair instead.
		lsp.HandleDidChange(changeAt(uri, 0, 11, "!"))
		want = "// emoji 😀! here\nY = 2\nX = 1\n"
		if got := lsp.GetTestDocuments()[uri]; got != want {
			t.Errorf("want %q, got %q", want, got)
		}
	})

	t.Run("CRLF line endings", func(t *testing.T) {
		lsp.ResetTestServer()
		initial := "a\r\nb\r\nc"
		lsp.GetTestDocuments()[uri] = initial
		lsp.HandleDidChange(changeAt(uri, 1, 1, "B"))
		want := "a\r\nbB\r\nc"
		if got := lsp.GetTestDocuments()[uri]; got != want {
			t.Errorf("want %q, got %q", want, got)
		}
	})

	t.Run("range replacement spanning lines", func(t *testing.T) {
		lsp.ResetTestServer()
		initial := "one\ntwo\nthree\n"
		lsp.GetTestDocuments()[uri] = initial
		lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
			TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri},
			ContentChanges: []lsp.TextDocumentContentChangeEvent{{
				Range: &lsp.Range{
					Start: lsp.Position{Line: 0, Character: 1},
					End:   lsp.Position{Line: 2, Character: 2},
				},
				Text: "X",
			}},
		})
		want := "oXree\n"
		if got := lsp.GetTestDocuments()[uri]; got != want {
			t.Errorf("want %q, got %q", want, got)
		}
	})

	t.Run("several changes applied in order", func(t *testing.T) {
		lsp.ResetTestServer()
		lsp.GetTestDocuments()[uri] = "abc\n"
		lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
			TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri},
			ContentChanges: []lsp.TextDocumentContentChangeEvent{
				{Range: &lsp.Range{Start: lsp.Position{Line: 0, Character: 3}, End: lsp.Position{Line: 0, Character: 3}}, Text: "d"},
				{Range: &lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 0}}, Text: "z"},
			},
		})
		want := "zabcd\n"
		if got := lsp.GetTestDocuments()[uri]; got != want {
			t.Errorf("want %q, got %q", want, got)
		}
	})

	t.Run("full-text change", func(t *testing.T) {
		lsp.ResetTestServer()
		lsp.GetTestDocuments()[uri] = "old\n"
		lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
			TextDocument: lsp.VersionedTextDocumentIdentifier{URI: uri},
			ContentChanges: []lsp.TextDocumentContentChangeEvent{{
				Text: "brand new\n",
			}},
		})
		if got := lsp.GetTestDocuments()[uri]; got != "brand new\n" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("out of range position is ignored", func(t *testing.T) {
		lsp.ResetTestServer()
		lsp.GetTestDocuments()[uri] = "abc\n"
		lsp.HandleDidChange(changeAt(uri, 99, 99, "XXX"))
		if got := lsp.GetTestDocuments()[uri]; got != "abc\n" {
			t.Errorf("out-of-range change altered the document: %q", got)
		}
	})
}

// TestIncrementalTypingDoesNotPublishTransientErrors covers the reported
// symptom: while typing a signal definition the intermediate states are
// invalid, and publishing those made the editor show errors that outlived the
// edit.
func TestIncrementalTypingDoesNotPublishTransientErrors(t *testing.T) {
	prevSync := lsp.SynchronousValidation
	lsp.SynchronousValidation = false
	defer func() { lsp.SynchronousValidation = prevSync }()

	lsp.ResetTestServer()
	lsp.GlobalSchema = schema.LoadFullSchema(".")
	var out bytes.Buffer
	prevOut := lsp.Output
	lsp.Output = &out
	defer func() { lsp.Output = prevOut }()

	const uri = "file:///tmp/incremental_typing.marte"
	initial := `#package T
+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB
    +DDB = {
      Class = GAMDataSource
      Signals = {
        T: uint32
      }
    }
    +TimingDataSource = {
      Class = TimingDataSource
    }
  }
  +Functions = {
    Class = ReferenceContainer
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
	lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{URI: uri, Text: initial},
	})
	out.Reset()

	// "    F: float32" typed after "T: uint32"; mid-typing text is broken.
	// The line holding "        T: uint32" is where the new signal is typed.
	targetLine := -1
	for i, l := range strings.Split(initial, "\n") {
		if strings.TrimSpace(l) == "T: uint32" {
			targetLine = i
		}
	}
	typeText(t, uri, "\n        F: float32", targetLine, len("        T: uint32"))

	want := strings.Replace(initial, "        T: uint32\n", "        T: uint32\n        F: float32\n", 1)
	if got := lsp.GetTestDocuments()[uri]; got != want {
		t.Fatalf("document text diverged during typing\nwant:\n%q\ngot:\n%q", want, got)
	}

	if diags := allDiagnostics(out.String(), uri); len(diags) > 0 {
		t.Errorf("transient diagnostics published while typing: %v", diags)
	}

	// Once typing pauses, validation must publish the clean state for the
	// completed edit (this is what makes the editor clear stale errors).
	// The completed edit must not leave errors behind (warnings about unused
	// signals are legitimate).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		publishes := publishedDiagnostics(out.String(), uri)
		if len(publishes) > 0 && !errorsIn(publishes[len(publishes)-1]) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("final diagnostics still contain errors: %v", publishedDiagnostics(out.String(), uri))
}

// publishedDiagnostics parses the captured LSP stream and returns the
// diagnostic messages of every publishDiagnostics notification for uri, in
// publish order.
func publishedDiagnostics(stream, uri string) [][]string {
	var publishes [][]string
	for _, chunk := range strings.Split(stream, "Content-Length") {
		if !strings.Contains(chunk, "publishDiagnostics") || !strings.Contains(chunk, uri) {
			continue
		}
		for _, line := range strings.Split(chunk, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "{") {
				continue
			}
			var msg struct {
				Params struct {
					URI         string `json:"uri"`
					Diagnostics []struct {
						Message  string `json:"message"`
						Severity int    `json:"severity"`
					} `json:"diagnostics"`
				} `json:"params"`
			}
			if err := json.Unmarshal([]byte(line), &msg); err != nil || msg.Params.URI != uri {
				continue
			}
			msgs := make([]string, 0, len(msg.Params.Diagnostics))
			for _, d := range msg.Params.Diagnostics {
				label := d.Message
				if d.Severity == 1 {
					label = "ERROR: " + label
				}
				msgs = append(msgs, label)
			}
			publishes = append(publishes, msgs)
		}
	}
	return publishes
}

// errorsIn reports whether any of the parsed diagnostic labels is an error.
func errorsIn(diags []string) bool {
	for _, d := range diags {
		if strings.HasPrefix(d, "ERROR: ") {
			return true
		}
	}
	return false
}

// allDiagnostics flattens every published diagnostic message for uri.
func allDiagnostics(stream, uri string) []string {
	var msgs []string
	for _, p := range publishedDiagnostics(stream, uri) {
		msgs = append(msgs, p...)
	}
	return msgs
}
