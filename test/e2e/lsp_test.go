package e2e

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

func TestLSPDiagnostics(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	content := `
+Test = {
    Class = "GAM"
    MissingField = "value"
}
`

	path := tf.CreateFile("test.marte", content)

	client := tf.RunLSP()
	defer client.Close()

	client.OpenFile(path, content)

	diags := client.GetDiagnostics(path)

	t.Logf("Got %d diagnostics", len(diags))
	for _, d := range diags {
		t.Logf("  %s:%d:%d - %s", d.File, d.Line, d.Column, d.Message)
	}
}

func TestLSPHover(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	content := `
+MyObject = {
    Class = "Type"
    Description = "This is my object"
}

+Ref = {
    Class = "Type"
    Target = MyObject
}
`

	path := tf.CreateFile("hover.marte", content)

	client := tf.RunLSP()
	defer client.Close()

	client.OpenFile(path, content)

	hover, err := client.Hover(path, 9, 10)
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}

	t.Logf("Hover result: %s", hover)
}

func TestLSPCompletion(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	content := `
+Signal1 = {
    Type = "uint32"
}

+Signal2 = {
    Type = "float32"
}
`

	path := tf.CreateFile("completion.marte", content)

	client := tf.RunLSP()
	defer client.Close()

	client.OpenFile(path, content)

	items, err := client.Completion(path, 6, 5)
	if err != nil {
		t.Fatalf("Completion failed: %v", err)
	}

	t.Logf("Got %d completion items", len(items))
	for _, item := range items {
		t.Logf("  - %s: %s", item.Label, item.Detail)
	}
}

func TestLSPProgressiveEdit(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	content := `+Test = {
    Class = "GAM"
}
`

	path := tf.CreateFile("edit.marte", content)

	client := tf.RunLSP()
	defer client.Close()

	client.OpenFile(path, content)

	diags1 := client.GetDiagnostics(path)
	t.Logf("Initial diagnostics: %d", len(diags1))

	client.EditFile(path, []framework.TextEdit{
		{
			Range: framework.Range{
				Start: framework.Position{Line: 1, Character: 0},
				End:   framework.Position{Line: 1, Character: 0},
			},
			NewText: "+NewObject = {\n    Class = \"Type\"\n}\n\n",
		},
	})

	doc := client.Document(path)
	if !strings.Contains(doc, "NewObject") {
		t.Fatalf("Expected document to contain NewObject")
	}

	diags2 := client.GetDiagnostics(path)
	t.Logf("After edit diagnostics: %d", len(diags2))
}

func TestLSPSymbol(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	content := `
+Object1 = {
    Class = "Type"
}

+Object2 = {
    Class = "Type"
    Ref = Object1
}
`

	path := tf.CreateFile("symbols.marte", content)

	client := tf.RunLSP()
	defer client.Close()

	client.OpenFile(path, content)

	symbols, err := client.Symbol(path)
	if err != nil {
		t.Fatalf("Symbol failed: %v", err)
	}

	t.Logf("Got %d symbols", len(symbols))
	for _, sym := range symbols {
		t.Logf("  - %s (kind=%d)", sym.Name, sym.Kind)
	}

	if len(symbols) < 2 {
		t.Fatalf("Expected at least 2 symbols")
	}
}

func TestLSPDefinition(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	tf := framework.WrapT(t, ctx)

	content := `
+MySignal = {
    Type = "uint32"
}

+Config = {
    Class = "GAM"
    Signal = MySignal
}
`

	path := tf.CreateFile("definition.marte", content)

	client := tf.RunLSP()
	defer client.Close()

	client.OpenFile(path, content)

	locs, err := client.Definition(path, 8, 12)
	if err != nil {
		t.Fatalf("Definition failed: %v", err)
	}

	t.Logf("Got %d definition locations", len(locs))
	for _, loc := range locs {
		t.Logf("  - %s:%d:%d", loc.URI, loc.Range.Start.Line, loc.Range.Start.Character)
	}
}
