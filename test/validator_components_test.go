package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestPIDGAMValidation(t *testing.T) {
	// PIDGAM requires Kp, Ki, Kd
	content := `
+MyPID = {
    Class = PIDGAM
    Kp = 1.0
    // Missing Ki
    // Missing Kd
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("pid.marte", config)

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject(context.Background())

	foundKi := false
	foundKd := false

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Ki: incomplete value") {
			foundKi = true
		}
		if strings.Contains(d.Message, "Kd: incomplete value") {
			foundKd = true
		}
	}

	if !foundKi {
		t.Error("Expected error for missing 'Ki' in PIDGAM")
	}
	if !foundKd {
		t.Error("Expected error for missing 'Kd' in PIDGAM")
	}
}

func TestFileDataSourceValidation(t *testing.T) {
	// FileDataSource requires Filename
	content := `
+MyFile = {
    Class = FileDataSource
    // Missing Filename
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("file.marte", config)

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject(context.Background())

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Filename: incomplete value") {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected error for missing 'Filename' in FileDataSource")
	}
}
