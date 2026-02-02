package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

func TestHandleCompletion(t *testing.T) {
	setup := func() {
		lsp.Tree = index.NewProjectTree()
		lsp.Documents = make(map[string]string)
		lsp.ProjectRoot = "."
		lsp.GlobalSchema = schema.NewSchema()
	}

	uri := "file://test.marte"
	path := "test.marte"

	t.Run("Suggest Classes", func(t *testing.T) {
		setup()
		content := "+Obj = { Class = "
		lsp.Documents[uri] = content

		params := lsp.CompletionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
			Position:     lsp.Position{Line: 0, Character: len(content)},
		}

		list := lsp.HandleCompletion(params)
		if list == nil || len(list.Items) == 0 {
			t.Fatal("Expected class suggestions, got none")
		}

		found := false
		for _, item := range list.Items {
			if item.Label == "RealTimeApplication" {
				found = true
				break
			}
		}
		if !found {
			t.Error("Expected RealTimeApplication in class suggestions")
		}
	})

	t.Run("Suggest Fields", func(t *testing.T) {
		setup()
		content := `
+MyApp = {
    Class = RealTimeApplication
    
}
`
		lsp.Documents[uri] = content
		p := parser.NewParser(content)
		cfg, _ := p.Parse()
		lsp.Tree.AddFile(path, cfg)

		// Position at line 3 (empty line inside MyApp)
		params := lsp.CompletionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
			Position:     lsp.Position{Line: 3, Character: 4},
		}

		list := lsp.HandleCompletion(params)
		if list == nil || len(list.Items) == 0 {
			t.Fatal("Expected field suggestions, got none")
		}

		foundData := false
		for _, item := range list.Items {
			if item.Label == "Data" {
				foundData = true
				if item.Detail != "Mandatory" {
					t.Errorf("Expected Data to be Mandatory, got %s", item.Detail)
				}
			}
		}
		if !foundData {
			t.Error("Expected 'Data' in field suggestions for RealTimeApplication")
		}
	})

	t.Run("Suggest References (DataSource)", func(t *testing.T) {
		setup()
		content := `
$App = {
    $Data = {
        +InDS = {
            Class = FileReader
            +Signals = {
                Sig1 = { Type = uint32 }
            }
        }
    }
}
+MyGAM = {
    Class = IOGAM
    +InputSignals = {
        S1 = { DataSource =  }
    }
}
`
		lsp.Documents[uri] = content
		p := parser.NewParser(content)
		cfg, _ := p.Parse()
		lsp.Tree.AddFile(path, cfg)
		lsp.Tree.ResolveReferences()

		// Position at end of "DataSource = "
		params := lsp.CompletionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
			Position:     lsp.Position{Line: 14, Character: 28},
		}

		list := lsp.HandleCompletion(params)
		if list == nil || len(list.Items) == 0 {
			t.Fatal("Expected DataSource suggestions, got none")
		}

		foundDS := false
		for _, item := range list.Items {
			if item.Label == "InDS" {
				foundDS = true
				break
			}
		}
		if !foundDS {
			t.Error("Expected 'InDS' in suggestions for DataSource field")
		}
	})

	t.Run("Filter Existing Fields", func(t *testing.T) {
		setup()
		content := `
+MyThread = {
    Class = RealTimeThread
    Functions = { }
    
}
`
		lsp.Documents[uri] = content
		p := parser.NewParser(content)
		cfg, _ := p.Parse()
		lsp.Tree.AddFile(path, cfg)

		// Position at line 4
		params := lsp.CompletionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
			Position:     lsp.Position{Line: 4, Character: 4},
		}

		list := lsp.HandleCompletion(params)
		for _, item := range list.Items {
			if item.Label == "Functions" || item.Label == "Class" {
				t.Errorf("Did not expect already defined field %s in suggestions", item.Label)
			}
		}
	})

		t.Run("Scope-aware suggestions", func(t *testing.T) {
		setup()
		// Define a project DataSource in one file
		cfg1, _ := parser.NewParser("#package MYPROJ.Data\n+ProjectDS = { Class = FileReader +Signals = { S1 = { Type = int32 } } }").Parse()
		lsp.Tree.AddFile("project_ds.marte", cfg1)

		// Define an isolated file
		contentIso := "+MyGAM = { Class = IOGAM +InputSignals = { S1 = { DataSource =  } } }"
		lsp.Documents["file://iso.marte"] = contentIso
		cfg2, _ := parser.NewParser(contentIso).Parse()
		lsp.Tree.AddFile("iso.marte", cfg2)

		lsp.Tree.ResolveReferences()

		// Completion in isolated file
		params := lsp.CompletionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: "file://iso.marte"},
			Position:     lsp.Position{Line: 0, Character: strings.Index(contentIso, "DataSource = ") + len("DataSource = ") + 1},
		}

		list := lsp.HandleCompletion(params)
		foundProjectDS := false
		if list != nil {
			for _, item := range list.Items {
				if item.Label == "ProjectDS" {
					foundProjectDS = true
					break
				}
			}
		}
		if foundProjectDS {
			t.Error("Did not expect ProjectDS in isolated file suggestions (isolation)")
		}

		// Completion in a project file
		lineContent := "+MyGAM = { Class = IOGAM +InputSignals = { S1 = { DataSource = Dummy } } }"
		contentPrj := "#package MYPROJ.App\n" + lineContent
		lsp.Documents["file://prj.marte"] = contentPrj
		pPrj := parser.NewParser(contentPrj)
		cfg3, err := pPrj.Parse()
		if err != nil {
			t.Logf("Parser error in contentPrj: %v", err)
		}
		lsp.Tree.AddFile("prj.marte", cfg3)
		lsp.Tree.ResolveReferences()

		paramsPrj := lsp.CompletionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: "file://prj.marte"},
			Position:     lsp.Position{Line: 1, Character: strings.Index(lineContent, "Dummy")},
		}

		listPrj := lsp.HandleCompletion(paramsPrj)
		foundProjectDS = false
		if listPrj != nil {
			for _, item := range listPrj.Items {
				if item.Label == "ProjectDS" {
					foundProjectDS = true
					break
				}
			}
		}
		if !foundProjectDS {
			t.Error("Expected ProjectDS in project file suggestions")
		}
	})

	t.Run("Suggest Signal Types", func(t *testing.T) {
		setup()
		content := `
+DS = {
    Class = FileReader
    Signals = {
        S1 = { Type =  }
    }
}
`
		lsp.Documents[uri] = content
		p := parser.NewParser(content)
		cfg, _ := p.Parse()
		lsp.Tree.AddFile(path, cfg)

		params := lsp.CompletionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
			Position:     lsp.Position{Line: 4, Character: strings.Index(content, "Type = ") + len("Type = ") + 1},
		}

		list := lsp.HandleCompletion(params)
		if list == nil {
			t.Fatal("Expected signal type suggestions")
		}

		foundUint32 := false
		for _, item := range list.Items {
			if item.Label == "uint32" {
				foundUint32 = true
				break
			}
		}
		if !foundUint32 {
			t.Error("Expected uint32 in suggestions")
		}
	})

	t.Run("Suggest CUE Enums", func(t *testing.T) {
		setup()
		// Inject custom schema with enum
		custom := []byte(`
package schema
#Classes: {
    TestEnumClass: {
        Mode: "Auto" | "Manual"
    }
}
`)
		val := lsp.GlobalSchema.Context.CompileBytes(custom)
		lsp.GlobalSchema.Value = lsp.GlobalSchema.Value.Unify(val)

		content := `
+Obj = {
    Class = TestEnumClass
    Mode =  
}
`
		lsp.Documents[uri] = content
		p := parser.NewParser(content)
		cfg, _ := p.Parse()
		lsp.Tree.AddFile(path, cfg)

		params := lsp.CompletionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
			Position:     lsp.Position{Line: 3, Character: strings.Index(content, "Mode = ") + len("Mode = ") + 1},
		}

		list := lsp.HandleCompletion(params)
		if list == nil {
			t.Fatal("Expected enum suggestions")
		}

		foundAuto := false
		for _, item := range list.Items {
			if item.Label == "\"Auto\"" { // CUE string value includes quotes
				foundAuto = true
				break
			}
		}
		if !foundAuto {
			// Check if it returned without quotes?
			// v.String() returns quoted for string.
			t.Error("Expected \"Auto\" in suggestions")
			for _, item := range list.Items {
				t.Logf("Suggestion: %s", item.Label)
			}
		}
	})
}
