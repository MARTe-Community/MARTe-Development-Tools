package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestLSPInlayHint(t *testing.T) {
	// Setup
	lsp.Tree = index.NewProjectTree()
	lsp.Documents = make(map[string]string)

	content := `
#let N : int= 10 + 5
+DS = {
    Class = FileReader
    Signals = {
        Sig1 = { Type = uint32 NumberOfElements = 10 }
    }
}
+GAM = {
    Class = IOGAM
    Expr = 10 + 20
    InputSignals = {
        Sig1 = { DataSource = DS }
    }
}
+Other = {
    Class = Controller
    Ref = DS
    VarRef = @N + 1
}
`
	uri := "file://inlay.marte"
	lsp.Documents[uri] = content
	p := parser.NewParser(content)
	cfg, _ := p.Parse()
	lsp.Tree.AddFile("inlay.marte", cfg)
	lsp.Tree.ResolveReferences()

	v := validator.NewValidator(lsp.Tree, ".")
	v.ValidateProject()

	params := lsp.InlayHintParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range: lsp.Range{
			Start: lsp.Position{Line: 0, Character: 0},
			End:   lsp.Position{Line: 20, Character: 0},
		},
	}

	res := lsp.HandleInlayHint(params)
	if len(res) == 0 {
		t.Fatal("Expected inlay hints, got 0")
	}

	foundTypeHint := false
	foundDSClassHint := false
	foundGeneralRefHint := false
	foundExprHint := false
	foundVarRefHint := false
	foundLetHint := false

	for _, hint := range res {
		t.Logf("Hint: '%s' at Line %d, Col %d", hint.Label, hint.Position.Line, hint.Position.Character)
		if hint.Label == "::uint32[1x10]" {
			foundTypeHint = true
		}
		if hint.Label == "FileReader::" && hint.Position.Line == 12 { // Sig1 line (DS)
			foundDSClassHint = true
		}
		if hint.Label == "FileReader::" && hint.Position.Line == 17 { // Ref = DS line
			foundGeneralRefHint = true
		}
		if hint.Label == " => 30" {
			foundExprHint = true
		}
		if hint.Label == "(=> 15)" {
			foundVarRefHint = true
		}
		if hint.Label == " => 15" && hint.Position.Line == 1 { // #let N line
			foundLetHint = true
		}
	}

	if !foundTypeHint {
		t.Error("Did not find signal type/size hint")
	}
	if !foundDSClassHint {
		t.Error("Did not find DataSource class hint")
	}
	if !foundGeneralRefHint {
		t.Error("Did not find general object reference hint")
	}
	if !foundExprHint {
		t.Error("Did not find expression evaluation hint")
	}
	if !foundVarRefHint {
		t.Error("Did not find variable reference evaluation hint")
	}
	if !foundLetHint {
		t.Error("Did not find #let expression evaluation hint")
	}
}
