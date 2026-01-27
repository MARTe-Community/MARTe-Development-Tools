package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestDataSourceThreadingValidation(t *testing.T) {
	content := `
+Data = {
    Class = ReferenceContainer
    +SharedDS = {
        Class = GAMDataSource
        #direction = "INOUT"
        #multithreaded = false
        Signals = {
            Sig1 = { Type = uint32 }
        }
    }
    +MultiDS = {
        Class = GAMDataSource
        #direction = "INOUT"
        #multithreaded = true
        Signals = {
            Sig1 = { Type = uint32 }
        }
    }
}
+GAM1 = {
    Class = IOGAM
    InputSignals = {
        Sig1 = { DataSource = SharedDS Type = uint32 }
    }
}
+GAM2 = {
    Class = IOGAM
    OutputSignals = {
        Sig1 = { DataSource = SharedDS Type = uint32 }
    }
}
+GAM3 = {
    Class = IOGAM
    InputSignals = {
        Sig1 = { DataSource = MultiDS Type = uint32 }
    }
}
+GAM4 = {
    Class = IOGAM
    OutputSignals = {
        Sig1 = { DataSource = MultiDS Type = uint32 }
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
                Functions = { GAM1 }
            }
            +Thread2 = {
                Class = RealTimeThread
                Functions = { GAM2 }
            }
        }
        +State2 = {
            Class = RealTimeState
            +Thread1 = {
                Class = RealTimeThread
                Functions = { GAM3 }
            }
            +Thread2 = {
                Class = RealTimeThread
                Functions = { GAM4 }
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

	// Since we don't load schema here (empty path), it won't validate classes via CUE,
	// but CheckDataSourceThreading relies on parsing logic, not CUE schema unification.
	// So it should work.

	v := validator.NewValidator(pt, "")
	v.ValidateProject()

	foundError := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "not multithreaded but used in multiple threads") {
			if strings.Contains(d.Message, "SharedDS") {
				foundError = true
			}
			if strings.Contains(d.Message, "MultiDS") {
				t.Error("Unexpected threading error for MultiDS")
			}
		}
	}

	if !foundError {
		t.Error("Expected threading error for SharedDS")
		// Debug
		for _, d := range v.Diagnostics {
			t.Logf("Diag: %s", d.Message)
		}
	}
}
