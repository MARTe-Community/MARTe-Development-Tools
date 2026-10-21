package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// mdt fmt writes the compact keyword spelling: `let`, `var`, `if`,
// `else`, `end`, `foreach`, `template`, `use` (the `#`-prefixed forms are
// accepted but normalized away).
func TestFormatterCompactKeywords(t *testing.T) {
	src := `template Chan(ID: int)
  ("+Channel" .. @ID) = {
    Class = ReferenceContainer
  }
end

#let N: int = 3
#var M: int = 4
#foreach i in { 1, 2 }
  #if (@i == 1)
    ("+A" .. @i) = { }
  #else
    ("+B" .. @i) = { }
  #end
end
use Chan Inst(ID = @N)
`
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var sb strings.Builder
	formatter.Format(cfg, &sb)
	out := sb.String()

	for _, want := range []string{
		"template Chan(ID: int)",
		"let N: int = 3",
		"var M: int = 4",
		"foreach i in { 1, 2 }",
		"if (@i == 1)",
		"else",
		"end",
		"use Chan Inst(ID = @N)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatted output missing %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"#let", "#var", "#if", "#else", "#end", "#foreach", "#template", "#use"} {
		if strings.Contains(out, banned) {
			t.Errorf("formatted output still contains %q:\n%s", banned, out)
		}
	}

	// The compact output must re-parse and format identically.
	cfg2, err := parser.NewParser(out).Parse()
	if err != nil {
		t.Fatalf("formatted output does not re-parse: %v\n%s", err, out)
	}
	var sb2 strings.Builder
	formatter.Format(cfg2, &sb2)
	if sb2.String() != out {
		t.Errorf("formatting is not idempotent:\nfirst:\n%s\nsecond:\n%s", out, sb2.String())
	}
}
