package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestSignalProperties(t *testing.T) {
	content := `
+Data = {
    Class = ReferenceContainer
    +MyDS = {
        Class = FileReader
        Filename = "test"
        Signals = {
            Correct = { Type = uint32 NumberOfElements = 10 }
        }
    }
}

+MyGAM = {
    Class = IOGAM
    InputSignals = {
        // Correct reference
        Correct = { DataSource = MyDS Type = uint32 NumberOfElements = 10 }
        
        // Mismatch Type
        BadType = { 
            Alias = Correct
            DataSource = MyDS 
            Type = float32 // Error
        }
        
        // Mismatch Elements
        BadElements = {
            Alias = Correct
            DataSource = MyDS
            Type = uint32
            NumberOfElements = 20 // Error
        }
        
        // Valid Cast
        //!cast(uint32, float32): Cast reason
        CastSig = {
            Alias = Correct
            DataSource = MyDS
            Type = float32 // OK
        }
        
        // Invalid Cast (Wrong definition type in pragma)
        //!cast(int32, float32): Wrong def type
        BadCast = {
            Alias = Correct
            DataSource = MyDS
            Type = float32 // Error because pragma mismatch
        }
    }
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("signal_props.marte", config)
	idx.ResolveReferences()

	v := validator.NewValidator(idx, ".")
	v.ValidateProject()

	foundBadType := false
	foundBadElements := false
	foundBadCast := false

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "property 'Type' mismatch") {
			if strings.Contains(d.Message, "'BadType'") {
				foundBadType = true
			}
			if strings.Contains(d.Message, "'BadCast'") {
				foundBadCast = true
			}
			if strings.Contains(d.Message, "'CastSig'") {
				t.Error("Unexpected error for CastSig (should be suppressed by pragma)")
			}
		}

		if strings.Contains(d.Message, "property 'NumberOfElements' mismatch") {
			foundBadElements = true
		}
	}

	if !foundBadType {
		t.Error("Expected error for BadType")
	}
	if !foundBadElements {
		t.Error("Expected error for BadElements")
	}
	if !foundBadCast {
		t.Error("Expected error for BadCast (pragma mismatch)")
	}
}
