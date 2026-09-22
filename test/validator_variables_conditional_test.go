package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

// Regression test: a typed #let whose list value contains a #if/#end block
// must flatten the active branch instead of yielding a CUE null element.
// Previously this produced: Variable 'X' value mismatch: N: conflicting
// values null and string (mismatched types null and string).
func TestTypedVariableConditionalArrayElements(t *testing.T) {
	const template = `#package T
var use_extra: bool = %s

#let thread_fns: [&GAM] = {
  A,
  #if @use_extra
    B,
  #end
  C,
}

+State = {
  Class = RealTimeState
  +Threads = {
    Class = ReferenceContainer
    +T = {
      Class = RealTimeThread
      Functions = @thread_fns
    }
  }
}`

	for _, enabled := range []bool{true, false} {
		t.Run("use_extra="+boolStr(enabled), func(t *testing.T) {
			src := strings.Replace(template, "%s", boolStr(enabled), 1)

			pt := index.NewProjectTree()
			p := parser.NewParser(src)
			cfg, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			pt.AddFile("main.marte", cfg)
			pt.ResolveReferences(nil)

			v := validator.NewValidator(pt, ".", nil)
			v.ValidateProject(context.Background())

			for _, d := range v.Diagnostics {
				if strings.Contains(d.Message, "value mismatch") || strings.Contains(d.Message, "null and string") {
					t.Errorf("Unexpected type-check diagnostic: %s", d.Message)
				}
			}
		})
	}
}

// Negative control: a typed variable whose value genuinely violates the
// declared type must still be reported (guards against a vacuous fix).
func TestTypedVariableTypeMismatchStillReported(t *testing.T) {
	src := `#package T

#let bad_fns: [&GAM] = {
  42,
}

+State = {
  Class = RealTimeState
  +Threads = {
    Class = ReferenceContainer
    +T = {
      Class = RealTimeThread
      Functions = @bad_fns
    }
  }
}`

	pt := index.NewProjectTree()
	p := parser.NewParser(src)
	cfg, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	pt.AddFile("main.marte", cfg)
	pt.ResolveReferences(nil)

	v := validator.NewValidator(pt, ".", nil)
	v.ValidateProject(context.Background())

	found := false
	for _, d := range v.Diagnostics {
		if strings.Contains(d.Message, "value mismatch") {
			found = true
		}
	}
	if !found {
		t.Error("Expected 'value mismatch' diagnostic for int value against [&GAM] type, got none")
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
