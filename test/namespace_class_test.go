package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestNamespaceClassValidation(t *testing.T) {
	content := `
+Obj = {
    Class = "SDN::SDNSubscriber"
	Topic = "MyTopic"
	Interface = "eth0"
}
`
	// SDNSubscriber is defined in internal/schema/marte.cue so it should be valid if we strip SDN::
	// If we don't strip, it will be "Unknown Class".

	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}

	idx := index.NewProjectTree()
	idx.AddFile("namespace.marte", config)

	v := validator.NewValidator(idx, ".", nil)
	v.ValidateProject()

	for _, d := range v.Diagnostics {
		if d.Level == validator.LevelWarning && strings.Contains(d.Message, "Unknown Class") {
			t.Errorf("Unexpected Unknown Class warning: %s", d.Message)
		}
		// We might get other errors if SDNSubscriber validation fails due to missing fields, 
		// but here we provided mandatory ones (Topic, Interface).
		// Actually Address/Port are optional/mandatory depending on definition.
		// In marte.cue:
		/*
			SDNSubscriber: {
				Topic!:              string
				Address?:            string
				Interface!:          string
                ...
			}
		*/
		// So it should pass CUE validation too.
	}
}
