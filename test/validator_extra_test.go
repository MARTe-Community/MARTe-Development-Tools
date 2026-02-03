package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestSDNSubscriberValidation(t *testing.T) {
	// SDNSubscriber requires Address and Port
	content := `
+MySDN = {
    Class = SDNSubscriber
    Address = "239.0.0.1"
    // Missing Interface
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("sdn.marte", config)

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject()

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Interface: field is required but not present") {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected error for missing 'Port' in SDNSubscriber")
	}
}

func TestFileWriterValidation(t *testing.T) {
	// FileWriter requires Filename
	content := `
+MyWriter = {
    Class = FileWriter
    // Missing Filename
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("writer.marte", config)

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject()

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Filename: incomplete value") {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected error for missing 'Filename' in FileWriter")
	}
}
