package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestNamespaceClassLookup(t *testing.T) {
	content := `
+App = {
    Class = "RealTimeApplication"
    +States = {
        Class = "ReferenceContainer"
        +State1 = {
            Class = "RealTimeState"
            +Thread1 = {
                Class = "RealTimeThread"
                Functions = {GAM1}
            }
        }
    }
    +Data = {
        Class = "ReferenceContainer"
        +DS1 = {
            Class = "SDN::SDNSubscriber" // Namespace! Direction IN
            Topic = "T"
            Interface = "I"
        }
    }
}

$GAM1 = {
    Class = "IOGAM"
    InputSignals = {
    }
    OutputSignals = {
        Sig1 = {
            DataSource = "DS1" // Used as Output! Should error.
            Type = "uint32"
        }
    }
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("main.marte", config)

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject(context.Background())

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "Input-only") {
			found = true
		}
	}

	if !found {
		t.Error("Expected error about Input-only DataSource, but got none. Namespace lookup failed?")
		for _, d := range v.Diagnostics {
			t.Logf("Diag: %s", d.Message)
		}
	}
}