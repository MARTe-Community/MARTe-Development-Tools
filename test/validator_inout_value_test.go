package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestINOUTValueInitialization(t *testing.T) {
	content := `
+Data = {
    Class = ReferenceContainer
    +MyDS = {
        Class = GAMDataSource
        #meta = { multithreaded = false }
        Signals = { Sig1 = { Type = uint32 } }
    }
}
+GAM1 = {
    Class = IOGAM
    InputSignals = {
        Sig1 = { 
            DataSource = MyDS 
            Type = uint32 
            Value = 10 // Initialization
        }
    }
}
+GAM2 = {
    Class = IOGAM
    InputSignals = {
        Sig1 = { DataSource = MyDS Type = uint32 } // Consumes initialized signal
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
                Functions = { GAM1, GAM2 } // Should Pass
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

	v := validator.NewValidator(pt, ".", nil)
	v.ValidateProject()

	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "before being produced") {
			t.Errorf("Unexpected error: %s", d.Message)
		}
	}
}

func TestINOUTValueTypeMismatch(t *testing.T) {
	content := `
+Data = { Class = ReferenceContainer +DS = { Class = GAMDataSource #meta = { multithreaded = false } Signals = { S = { Type = uint8 } } } }
+GAM1 = {
    Class = IOGAM
    InputSignals = {
        S = { DataSource = DS Type = uint8 Value = 1024 }
    }
}
+App = { Class = RealTimeApplication +States = { Class = ReferenceContainer +S = { Class = RealTimeState Threads = { +T = { Class = RealTimeThread Functions = { GAM1 } } } } } }
`
	pt := index.NewProjectTree()
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}
	pt.AddFile("fail.marte", cfg)

	v := validator.NewValidator(pt, ".", nil)
	v.ValidateProject()

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Value initialization mismatch") {
			found = true
		}
	}
	if !found {
		t.Error("Expected Value initialization mismatch error")
	}
}
