package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// The expression result hint must be placed at the end of the expression,
// not inside a member access: `@prefixes.fast` used to report its end after
// `@pref`, splitting the identifier in the editor.
func TestInlayHintExpressionEndPosition(t *testing.T) {
	lsp.ResetTestServer()

	lines := []string{
		`#let rfid: string = "R"`,
		`#let prefixes: { fast: string } = { fast = "F" }`,
		`#let fast_id: string = (@rfid .. @prefixes.fast)`,
		`+App = {`,
		`  Class = ReferenceContainer`,
		`  Id = @fast_id`,
		`}`,
	}
	content := strings.Join(lines, "\n") + "\n"
	uri := "file://inlay_end.marte"
	lsp.GetTestDocuments()[uri] = content
	cfg, err := parser.NewParser(content).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	lsp.GetTestTree().AddFile("inlay_end.marte", cfg)
	lsp.GetTestTree().ResolveReferences(nil)

	v := validator.NewValidator(lsp.GetTestTree(), ".", nil)
	v.ValidateProject(context.Background())

	res := lsp.HandleInlayHint(lsp.InlayHintParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range: lsp.Range{
			Start: lsp.Position{Line: 0, Character: 0},
			End:   lsp.Position{Line: len(lines), Character: 0},
		},
	})

	wantLine := 2
	wantChar := strings.Index(lines[wantLine], ")")
	var found bool
	for _, hint := range res {
		if hint.Position.Line != wantLine || !strings.HasPrefix(hint.Label, " => ") {
			continue
		}
		found = true
		if hint.Position.Character != wantChar {
			t.Errorf("hint %q at character %d, want %d (end of the expression, before ')'): the hint must not fall inside %q",
				hint.Label, hint.Position.Character, wantChar, "@prefixes")
		}
	}
	if !found {
		t.Fatalf("no expression hint on line %d; hints: %+v", wantLine, res)
	}
}
