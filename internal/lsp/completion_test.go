package lsp

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

func TestHandleCompletion(t *testing.T) {
	setup := func() {
		tree = index.NewProjectTree()
		documents = make(map[string]string)

		projectRoot = "."
		globalSchema = schema.NewSchema()
	}

	uri := "file://test.marte"
	path := "test.marte"

	t.Run("Suggest Classes", func(t *testing.T) {
		setup()
		content := "+Obj = { Class = "
		documents[uri] = content

		params := CompletionParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
			Position:     Position{Line: 0, Character: len(content)},
		}

		list := handleCompletion(params)
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
		documents[uri] = content
		p := parser.NewParser(content)
		cfg, _ := p.Parse()
		tree.AddFile(path, cfg)

		// Position at line 3 (empty line inside MyApp)
		params := CompletionParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
			Position:     Position{Line: 3, Character: 4},
		}

		list := handleCompletion(params)
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
		documents[uri] = content
		p := parser.NewParser(content)
		cfg, _ := p.Parse()
		tree.AddFile(path, cfg)
		tree.ResolveReferences()

		// Position at end of "DataSource = "
		params := CompletionParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
			Position:     Position{Line: 14, Character: 28},
		}

		list := handleCompletion(params)
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
		documents[uri] = content
		p := parser.NewParser(content)
		cfg, _ := p.Parse()
		tree.AddFile(path, cfg)

		// Position at line 4
		params := CompletionParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
			Position:     Position{Line: 4, Character: 4},
		}

		list := handleCompletion(params)
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
		tree.AddFile("project_ds.marte", cfg1)

		// Define an isolated file
		contentIso := "+MyGAM = { Class = IOGAM +InputSignals = { S1 = { DataSource =  } } }"
		documents["file://iso.marte"] = contentIso
		cfg2, _ := parser.NewParser(contentIso).Parse()
		tree.AddFile("iso.marte", cfg2)

		tree.ResolveReferences()

		// Completion in isolated file
		params := CompletionParams{
			TextDocument: TextDocumentIdentifier{URI: "file://iso.marte"},
			Position:     Position{Line: 0, Character: strings.Index(contentIso, "DataSource = ") + len("DataSource = ") + 1},
		}

		list := handleCompletion(params)
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
			t.Error("Did not expect ProjectDS in isolated file suggestions")
		}

		// Completion in a project file
		lineContent := "+MyGAM = { Class = IOGAM +InputSignals = { S1 = { DataSource = Dummy } } }"
		contentPrj := "#package MYPROJ.App\n" + lineContent
		documents["file://prj.marte"] = contentPrj
		pPrj := parser.NewParser(contentPrj)
		cfg3, err := pPrj.Parse()
		if err != nil {
			t.Logf("Parser error in contentPrj: %v", err)
		}
		tree.AddFile("prj.marte", cfg3)
		tree.ResolveReferences()

		paramsPrj := CompletionParams{
			TextDocument: TextDocumentIdentifier{URI: "file://prj.marte"},
			Position:     Position{Line: 1, Character: strings.Index(lineContent, "Dummy")},
		}

		listPrj := handleCompletion(paramsPrj)
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
}
