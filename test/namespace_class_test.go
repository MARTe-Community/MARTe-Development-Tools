package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestNamespaceClassValidation(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		expectWarning bool
		warningText   string
	}{
		{
			name: "Valid Namespace Class",
			content: `
+Obj = {
    Class = "SDN::SDNSubscriber"
	Topic = "MyTopic"
	Interface = "eth0"
}
`,
			expectWarning: false,
		},
		{
			name: "Valid Unquoted Namespace Class",
			content: `
+Obj = {
    Class = SDN::SDNSubscriber
	Topic = "MyTopic"
	Interface = "eth0"
}
`,
			expectWarning: false,
		},
		{
			name: "Valid Class No Namespace",
			content: `
+Obj = {
    Class = "SDNSubscriber"
	Topic = "MyTopic"
	Interface = "eth0"
}
`,
			expectWarning: false,
		},
		{
			name: "Unknown Class with Namespace",
			content: `
+Obj = {
    Class = "SDN::UnknownClass"
}
`,
			expectWarning: true,
			warningText:   "Unknown Class",
		},
		{
			name: "Unknown Class No Namespace",
			content: `
+Obj = {
    Class = "UnknownClass"
}
`,
			expectWarning: true,
			warningText:   "Unknown Class",
		},
		{
			name: "Arbitrary Namespace Ignored",
			content: `
+Obj = {
    Class = "MyLib::SDNSubscriber"
	Topic = "MyTopic"
	Interface = "eth0"
}
`,
			expectWarning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := parser.NewParser(tt.content)
			config, err := p.Parse()
			if err != nil {
				t.Fatal(err)
			}

			idx := index.NewProjectTree()
			idx.AddFile("test.marte", config)

			v := validator.NewValidator(idx, ".", nil)
			v.ValidateProject(context.Background())

			found := false
			for _, d := range v.Diagnostics {
				if d.Level == validator.LevelWarning && tt.warningText != "" && strings.Contains(d.Message, tt.warningText) {
					found = true
				}
				if !tt.expectWarning && d.Level == validator.LevelError {
					t.Errorf("Unexpected error: %s", d.Message)
				}
			}

			if tt.expectWarning && !found {
				t.Errorf("Expected warning '%s' but got none", tt.warningText)
			}
			if !tt.expectWarning && found {
				t.Errorf("Unexpected warning '%s'", tt.warningText)
			}
		})
	}
}
