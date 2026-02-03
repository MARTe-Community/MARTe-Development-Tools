package integration

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

func TestFormatterWhitespace(t *testing.T) {
	content := `
+Obj1 = {
    F1 = 1
}


+Obj2 = {
    F2 = 2


    F3 = 3
}
`
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	formatter.Format(cfg, &buf)
	formatted := buf.String()

	if strings.Contains(formatted, "\n\n\n") {
		t.Errorf("Multiple empty lines not collapsed:\n%s", formatted)
	}

	if !strings.Contains(formatted, "}\n\n+Obj2") {
		t.Errorf("Empty line between objects missing or incorrect:\n%s", formatted)
	}
	if !strings.Contains(formatted, "F2 = 2\n\n  F3 = 3") {
		t.Errorf("Empty line between fields missing or incorrect:\n%s", formatted)
	}
}

func TestFormatterDocstringWhitespace(t *testing.T) {
	content := `
+Obj1 = {}


//# Doc
+Obj2 = {}
`
	p := parser.NewParser(content)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	formatter.Format(cfg, &buf)
	formatted := buf.String()

	if !strings.Contains(formatted, "}\n\n//# Doc\n+Obj2") {
		t.Errorf("Empty line before docstring missing or incorrect:\n%s", formatted)
	}
}
