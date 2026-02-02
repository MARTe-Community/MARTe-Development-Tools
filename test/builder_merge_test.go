package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
)

func TestBuilderMergeNodes(t *testing.T) {
	// Two files without package, defining SAME root node +App.
	// This triggers merging logic in Builder.
	
	content1 := `
+App = {
    Field1 = 10
    +Sub = { Val = 1 }
}
`
	content2 := `
+App = {
    Field2 = 20
    +Sub = { Val2 = 2 }
}
`
	f1, _ := os.CreateTemp("", "merge1.marte")
	f1.WriteString(content1)
	f1.Close()
	defer os.Remove(f1.Name())

	f2, _ := os.CreateTemp("", "merge2.marte")
	f2.WriteString(content2)
	f2.Close()
	defer os.Remove(f2.Name())

	b := builder.NewBuilder([]string{f1.Name(), f2.Name()}, nil)

	outF, _ := os.CreateTemp("", "out_merge.marte")
	defer os.Remove(outF.Name())
	
	err := b.Build(outF)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	outF.Close()

	outContent, _ := os.ReadFile(outF.Name())
	outStr := string(outContent)

	if !strings.Contains(outStr, "Field1 = 10") { t.Error("Missing Field1") }
	if !strings.Contains(outStr, "Field2 = 20") { t.Error("Missing Field2") }
	if !strings.Contains(outStr, "+Sub = {") { t.Error("Missing Sub") }
	if !strings.Contains(outStr, "Val = 1") { t.Error("Missing Sub.Val") }
	if !strings.Contains(outStr, "Val2 = 2") { t.Error("Missing Sub.Val2") }
}
