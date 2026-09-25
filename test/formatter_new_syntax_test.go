package integration

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// `mdt fmt` must round-trip every construct added recently: with-blocks,
// member access, dict/list concatenation, escapes, bitwise operators,
// one-word #elseif, conditional array elements and the signal
// definition sugar.
func TestFormatterRoundTripsNewSyntax(t *testing.T) {
	src := `#package T
var mode: int = 2
var flag: bool = true

let Base: [int] = { 1, 2 }
let Merged: [int] = @Base .. { 3, 4 }

with json("cfg.json") as cfg begin
  +DS = {
    Class = GAMDataSource
    Signals = {
      Plain: uint32
      Vector: float32[4] = {
        Frequency = 100
      }
    }
    Host = @cfg.db.host
    Dict = { A = 1 } .. { B = 2 }
    Escaped = "quote:\" tab:\t"
    Bitwise = (0b1100 & 0x0A)
    Ticked = "v" .. @mode + 1
    #if (@mode == 1)
      P = "one"
    #elseif (@mode == 2)
      P = "two"
    #else
      P = "other"
    #end
    List = {
      A,
      #if @flag
        B,
      #else
        C,
      #end
      D,
    }
    #foreach k val in @cfg.items
      ("+Item" .. @k) = {
        V = @val
      }
    #end
  }
end
`
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	var sb strings.Builder
	formatter.Format(cfg, &sb)
	out := sb.String()

	// The formatted text must re-parse to the same shape.
	if _, err := parser.NewParser(out).Parse(); err != nil {
		t.Fatalf("formatted output does not re-parse: %v\n%s", err, out)
	}

	for _, want := range []string{
		"with json(\"cfg.json\") as cfg begin",
		"Plain: uint32",
		"Vector: float32[4] = {",
		"@cfg.db.host",
		"#else if",
		".. ",
		"#foreach k, val in @cfg.items",
		"(\"+Item\" .. @k)",
		`Escaped = "quote:\" tab:\t"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in formatted output, got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "begin") || !strings.Contains(out, "\nend") {
		t.Errorf("with-block delimiters missing:\n%s", out)
	}
}
