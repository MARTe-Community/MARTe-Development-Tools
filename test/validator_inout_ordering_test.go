package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestINOUTOrdering(t *testing.T) {
	content := `
+Data = {
    Class = ReferenceContainer
    +MyDS = {
        Class = GAMDataSource
        #meta = { multithreaded = false } // Explicitly false
        Signals = { Sig1 = { Type = uint32 } }
    }
}
+GAM_Consumer = {
    Class = IOGAM
    InputSignals = {
        Sig1 = { DataSource = MyDS Type = uint32 }
    }
}
+GAM_Producer = {
    Class = IOGAM
    OutputSignals = {
        Sig1 = { DataSource = MyDS Type = uint32 }
    }
}
+App = {
    Class = RealTimeApplication
    +States = {
        Class = ReferenceContainer
        +State1 = {
            Class = RealTimeState
            +Thread1 = {
                Class = RealTimeThread
                Functions = { GAM_Consumer, GAM_Producer } // Fail
            }
        }
        +State2 = {
            Class = RealTimeState
            +Thread2 = {
                Class = RealTimeThread
                Functions = { GAM_Producer, GAM_Consumer } // Pass
            }
        }
    }
}
`
	pt := index.NewProjectTree()
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	pt.AddFile("main.marte", cfg)

	// Use validator with default schema (embedded)
	// We pass "." but it shouldn't matter if no .marte_schema.cue exists
	v := validator.NewValidator(pt, ".")
	v.ValidateProject()

	foundError := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "consumed by GAM '+GAM_Consumer'") &&
			strings.Contains(d.Message, "before being produced") {
			foundError = true
		}
	}

	if !foundError {
		t.Error("Expected INOUT ordering error for State1")
		for _, d := range v.Diagnostics {
			t.Logf("Diag: %s", d.Message)
		}
	}

	foundErrorState2 := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "State '+State2'") && strings.Contains(d.Message, "before being produced") {
			foundErrorState2 = true
		}
	}

	if foundErrorState2 {
		t.Error("Unexpected INOUT ordering error for State2 (Correct order)")
	}
}
