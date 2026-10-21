package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// structFieldsFixture declares an inline struct variable and uses two of its
// fields. Lines (0-based):
//
//	0: #var hardware: {
//	1:     address: uint16
//	2:     port: uint16
//	3:     sample_rate: float64
//	4: }
//	5:
//	6: +Obj = {
//	7:     Field = @hardware.port
//	8:     Other = @hardware.address
//	9: }
const structFieldsFixture = `#var hardware: {
    address: uint16
    port: uint16
    sample_rate: float64
}

+Obj = {
    Field = @hardware.port
    Other = @hardware.address
}
`

func setupStructFields(t *testing.T) string {
	t.Helper()
	lsp.ResetTestServer()

	uri := "file://struct_fields.marte"
	lsp.GetTestDocuments()[uri] = structFieldsFixture

	p := parser.NewParser(structFieldsFixture)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	lsp.GetTestTree().AddFile("struct_fields.marte", cfg)
	lsp.GetTestTree().ResolveReferences(nil)
	return uri
}

func TestLSPStructFieldCompletion(t *testing.T) {
	uri := setupStructFields(t)

	// Cursor right after `@hardware.` on line 7 ("    Field = @hardware.port"):
	// the member starts at column 22, so the completion prefix is
	// "    Field = @hardware.".
	params := lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 7, Character: 22},
	}
	list := lsp.HandleCompletion(params)
	if list == nil {
		t.Fatal("Expected field completions for @hardware., got nil")
	}

	want := map[string]string{
		"address":     "uint16",
		"port":        "uint16",
		"sample_rate": "float64",
	}
	got := make(map[string]string, len(list.Items))
	for _, item := range list.Items {
		got[item.Label] = item.Detail
		if item.Kind != 5 {
			t.Errorf("Expected field kind (5) for %s, got %d", item.Label, item.Kind)
		}
	}
	for name, detail := range want {
		if got[name] != detail {
			t.Errorf("Expected field %s with detail %q, got %q", name, detail, got[name])
		}
	}
	if len(got) != len(want) {
		t.Errorf("Expected exactly %d fields, got %v", len(want), got)
	}
}

func TestLSPStructFieldCompletionUnknownVariable(t *testing.T) {
	setupStructFields(t)

	uri := "file://unknown_var.marte"
	// The dangling member access does not parse; the LSP still serves
	// completion on the raw text and must find no variable to resolve.
	lsp.GetTestDocuments()[uri] = "    X = @unknown.\n"

	params := lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 0, Character: 17}, // right after "@unknown."
	}
	if list := lsp.HandleCompletion(params); list != nil {
		t.Errorf("Expected no completions for unknown variable, got %v", list.Items)
	}
}

func TestLSPStructFieldDefinition(t *testing.T) {
	uri := setupStructFields(t)

	// Cursor on `port` in "    Field = @hardware.port" (columns 22-25).
	params := lsp.DefinitionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 7, Character: 23},
	}
	res := lsp.HandleDefinition(params)
	locs, ok := res.([]lsp.Location)
	if !ok || len(locs) != 1 {
		t.Fatalf("Expected 1 definition location, got %v", res)
	}
	// The `port` field is declared on line 2 (0-based) at column 4.
	if locs[0].Range.Start.Line != 2 {
		t.Errorf("Expected field definition on line 2, got %d", locs[0].Range.Start.Line)
	}
	if locs[0].Range.Start.Character != 4 {
		t.Errorf("Expected field definition at column 4, got %d", locs[0].Range.Start.Character)
	}
}

func TestLSPStructFieldReferencesFromDeclaration(t *testing.T) {
	uri := setupStructFields(t)

	// Cursor on the `port` field declaration ("    port: uint16", line 2).
	params := lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 2, Character: 5},
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	}
	locs := lsp.HandleReferences(params)
	if len(locs) != 1 {
		t.Fatalf("Expected 1 reference for field port, got %d: %v", len(locs), locs)
	}
	// The single usage is `@hardware.port` on line 7; the member starts at
	// column 22.
	if locs[0].Range.Start.Line != 7 {
		t.Errorf("Expected usage on line 7, got %d", locs[0].Range.Start.Line)
	}
	if locs[0].Range.Start.Character != 22 {
		t.Errorf("Expected usage at column 22, got %d", locs[0].Range.Start.Character)
	}
}

func TestLSPStructFieldReferencesFromUsage(t *testing.T) {
	uri := setupStructFields(t)

	// Cursor on `address` in "    Other = @hardware.address" (line 8,
	// member columns 21-27).
	params := lsp.ReferenceParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 8, Character: 23},
		Context:      lsp.ReferenceContext{IncludeDeclaration: true},
	}
	locs := lsp.HandleReferences(params)
	if len(locs) != 1 {
		t.Fatalf("Expected 1 reference for field address, got %d: %v", len(locs), locs)
	}
	if locs[0].Range.Start.Line != 8 {
		t.Errorf("Expected usage on line 8, got %d", locs[0].Range.Start.Line)
	}
}

// Completion items must carry a text edit covering only the member after
// the last dot, otherwise clients match candidates against "@var." and
// show nothing.
func TestLSPStructFieldCompletionEditRange(t *testing.T) {
	uri := setupStructFields(t)

	// Line 7 is "    Field = @hardware.port".
	line := "    Field = @hardware."
	text := structFieldsFixture
	text = strings.Replace(text, "    Field = @hardware.port", line, 1)
	lsp.GetTestDocuments()[uri] = text

	checkItems := func(t *testing.T, char int, wantStart int) {
		t.Helper()
		list := lsp.HandleCompletion(lsp.CompletionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
			Position:     lsp.Position{Line: 7, Character: char},
		})
		if list == nil || len(list.Items) == 0 {
			t.Fatalf("no completions at char %d", char)
		}
		for _, item := range list.Items {
			if item.TextEdit == nil {
				t.Fatalf("%s: no textEdit", item.Label)
			}
			if item.TextEdit.NewText != item.Label {
				t.Errorf("%s: newText = %q", item.Label, item.TextEdit.NewText)
			}
			if item.FilterText != item.Label {
				t.Errorf("%s: filterText = %q", item.Label, item.FilterText)
			}
			if got := item.TextEdit.Range.Start.Character; got != wantStart {
				t.Errorf("%s: edit starts at %d, want %d", item.Label, got, wantStart)
			}
			if got := item.TextEdit.Range.End.Character; got != char {
				t.Errorf("%s: edit ends at %d, want %d", item.Label, got, char)
			}
		}
	}

	// Empty member: insertion at the cursor, right after the dot.
	checkItems(t, len(line), len(line))

	// Partial member: the edit replaces "por" only.
	partial := strings.Replace(text, "@hardware.", "@hardware.por", 1)
	lsp.GetTestDocuments()[uri] = partial
	checkItems(t, len(line)+3, len(line))
}

// Nested member paths walk named types, and the trailing partial segment
// does not have to resolve.
func TestLSPStructFieldCompletionNested(t *testing.T) {
	lsp.ResetTestServer()
	src := `#package Demo

type Cfg_T {
  host: string
  port: uint16
}

var cfg: { net: Cfg_T }

+App = {
  Class = RealTimeApplication
  Host = @cfg.net.por
}
`
	uri := "file://nested_completion.marte"
	lsp.GetTestDocuments()[uri] = src
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	lsp.GetTestTree().AddFile("nested_completion.marte", cfg)
	lsp.GetTestTree().ResolveReferences(nil)

	target := "  Host = @cfg.net.por"
	list := lsp.HandleCompletion(lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 11, Character: len(target)},
	})
	if list == nil {
		t.Fatal("no completions for @cfg.net.por")
	}
	labels := make(map[string]bool, len(list.Items))
	for _, item := range list.Items {
		labels[item.Label] = true
		if item.TextEdit == nil || item.TextEdit.Range.Start.Character != len(target)-3 {
			t.Errorf("%s: edit range = %+v, want start %d", item.Label, item.TextEdit, len(target)-3)
		}
	}
	if !labels["host"] || !labels["port"] {
		t.Errorf("expected host and port from the named type, got %v", labels)
	}
}

// Positions are UTF-16 based: a non-ASCII prefix must not shift the edit
// range.
func TestLSPStructFieldCompletionUTF16(t *testing.T) {
	uri := setupStructFields(t)
	// Em dash (3 UTF-8 bytes, 1 UTF-16 unit) before the reference.
	line := "    Field = — @hardware."
	text := strings.Replace(structFieldsFixture, "    Field = @hardware.port", line, 1)
	lsp.GetTestDocuments()[uri] = text

	list := lsp.HandleCompletion(lsp.CompletionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     lsp.Position{Line: 7, Character: len([]rune(line))},
	})
	if list == nil || len(list.Items) != 3 {
		t.Fatalf("expected three fields, got %v", list)
	}
	for _, item := range list.Items {
		// UTF-16 character offset of the end of the line.
		want := len([]rune(line))
		if item.TextEdit == nil || item.TextEdit.Range.Start.Character != want {
			t.Errorf("%s: edit start = %+v, want %d", item.Label, item.TextEdit, want)
		}
	}
}
