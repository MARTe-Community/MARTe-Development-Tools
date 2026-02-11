package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestHandleDocumentSymbol(t *testing.T) {
	// Reset tree for test
	lsp.ResetTestServer()

	content := `
#let VAR : uint32 = 10
+MyClass = {
    Class = Type
    Field1 = 1
    $NestedInterface = {
        Class = Type
    }
}
`
	path := "/test.marte"
	uri := "file://" + path
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	lsp.GetTestTree().AddFile(path, config)

	params := lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
	}

	symbols := lsp.HandleDocumentSymbol(params)

	// Expected:
	// 1. VAR (Constant)
	// 2. +MyClass (Class)
	//    - $NestedInterface (Interface)

	if len(symbols) != 2 {
		t.Fatalf("Expected 2 top-level symbols (VAR and +MyClass), got %d", len(symbols))
	}

	// Find +MyClass
	var myClass *lsp.DocumentSymbol
	for i := range symbols {
		if symbols[i].Name == "+MyClass" {
			myClass = &symbols[i]
		}
	}

	if myClass == nil {
		t.Fatal("+MyClass symbol not found")
	}

	if myClass.Kind != lsp.SymbolKindClass {
		t.Errorf("Expected +MyClass to be Class, got %d", myClass.Kind)
	}

	if len(myClass.Children) != 1 {
		t.Fatalf("Expected 1 child ($NestedInterface) for +MyClass, got %d", len(myClass.Children))
	}

	var nested *lsp.DocumentSymbol
	for i := range myClass.Children {
		if myClass.Children[i].Name == "$NestedInterface" {
			nested = &myClass.Children[i]
		}
	}

	if nested == nil {
		t.Fatal("$NestedInterface not found as child of +MyClass")
	}

	if nested.Kind != lsp.SymbolKindInterface {
		t.Errorf("Expected $NestedInterface to be Interface, got %d", nested.Kind)
	}

	// Test Signals
	content2 := `
+GAM = {
    +InputSignals = {
        +Sig1 = {
            Type = uint32
        }
    }
}
`
	path2 := "/signals.marte"
	uri2 := "file://" + path2
	p2 := parser.NewParser(content2)
	config2, _ := p2.Parse()
	lsp.GetTestTree().AddFile(path2, config2)

	symbols2 := lsp.HandleDocumentSymbol(lsp.DocumentSymbolParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri2},
	})

	if len(symbols2) != 1 {
		t.Fatalf("Expected 1 top-level symbol (+GAM), got %d", len(symbols2))
	}

	gam := symbols2[0]
	if len(gam.Children) != 1 {
		t.Fatalf("Expected 1 child for +GAM (+InputSignals), got %d", len(gam.Children))
	}

	inSigs := gam.Children[0]
	if len(inSigs.Children) != 1 {
		t.Fatalf("Expected 1 child for +InputSignals (+Sig1), got %d", len(inSigs.Children))
	}

	sig1 := inSigs.Children[0]
	if sig1.Name != "+Sig1" {
		t.Errorf("Expected signal name +Sig1, got %s", sig1.Name)
	}
	if sig1.Kind != lsp.SymbolKindVariable {
		t.Errorf("Expected signal +Sig1 to be Variable kind, got %d", sig1.Kind)
	}
}

func TestHandleWorkspaceSymbol(t *testing.T) {
	// Reset tree
	lsp.ResetTestServer()

	files := map[string]string{
		"/a.marte": "+ObjectA = { Class = C }",
		"/b.marte": "+ObjectB = { Class = C }",
	}

	for path, content := range files {
		p := parser.NewParser(content)
		config, _ := p.Parse()
		lsp.GetTestTree().AddFile(path, config)
	}

	// Add a variable to one of the files
	pVar := parser.NewParser("#let GLOBAL_VAR : uint32 = 100")
	cVar, _ := pVar.Parse()
	lsp.GetTestTree().AddFile("/vars.marte", cVar)

	// 1. Search for "Object"
	res1 := lsp.HandleWorkspaceSymbol(lsp.WorkspaceSymbolParams{Query: "Object"})
	if len(res1) != 2 {
		t.Errorf("Expected 2 symbols for 'Object', got %d", len(res1))
	}

	// 2. Search for "A"
	res2 := lsp.HandleWorkspaceSymbol(lsp.WorkspaceSymbolParams{Query: "A"})
	// ObjectA AND GLOBAL_VAR (contains A)
	if len(res2) != 2 {
		t.Errorf("Expected 2 symbols for 'A' (ObjectA and GLOBAL_VAR), got %d", len(res2))
	}

	// 3. Search for "GLOBAL"
	resVar := lsp.HandleWorkspaceSymbol(lsp.WorkspaceSymbolParams{Query: "GLOBAL"})
	if len(resVar) != 1 {
		t.Fatalf("Expected 1 symbol for 'GLOBAL', got %d", len(resVar))
	}
	if resVar[0].Name != "GLOBAL_VAR" {
		t.Errorf("Expected GLOBAL_VAR, got %s", resVar[0].Name)
	}
	if resVar[0].Kind != lsp.SymbolKindConstant {
		t.Errorf("Expected GLOBAL_VAR to be Constant, got %d", resVar[0].Kind)
	}

	// 4. Empty query
	res3 := lsp.HandleWorkspaceSymbol(lsp.WorkspaceSymbolParams{Query: ""})
	if len(res3) != 3 { // 2 objects + 1 variable
		t.Errorf("Expected 3 symbols for empty query, got %d", len(res3))
	}
}
