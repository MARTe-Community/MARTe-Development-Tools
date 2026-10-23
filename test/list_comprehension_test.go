package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// TestListComprehension covers projecting a field out of a list of objects and
// the filter/index forms.
func TestListComprehension(t *testing.T) {
	out := buildTemp(t, `#package T

var nums: [int] = { 1, 2, 3 }

+App = {
  Class = ReferenceContainer
  Doubled = { @n * 2 for n in @nums }
  Big = { @n for n in @nums if @n > 1 }
  Indexed = { @i for i, n in @nums }
  Flagged = { @k for k, v in { a = true, b = false, c = true } if @v }
  Plain = { 1, 2 }
}
`)
	for _, want := range []string{
		"Doubled = { 2 4 6 }",
		"Big = { 2 3 }",
		"Indexed = { 0 1 2 }",
		`Flagged = { "a" "c" }`,
		"Plain = { 1 2 }",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("build output missing %q:\n%s", want, out)
		}
	}
}

// TestListComprehensionProjectField is the reported use case: build a list
// from a list of objects by taking one field of each.
func TestListComprehensionProjectField(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "signals.json",
		`{"signals":[{"name":"AI0","channel":1},{"name":"AI1","channel":4},{"name":"AI2","channel":8}]}`)
	src := `#package T

+App = {
  Class = ReferenceContainer
  with json("signals.json") as cfg begin
    Names = { @sig.name for sig in @cfg.signals }
    Channels = { @sig.channel for sig in @cfg.signals if @sig.channel > 1 }
    Indexed = { @i for i, sig in @cfg.signals }
  end
}
`
	file := writeFile(t, dir, "app.marte", src)
	out := buildTempWithDir(t, dir, file)
	for _, want := range []string{
		`Names = { "AI0" "AI1" "AI2" }`,
		"Channels = { 4 8 }",
		"Indexed = { 0 1 2 }",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("build output missing %q:\n%s", want, out)
		}
	}
}

// TestListComprehensionNoIterable keeps an unresolvable comprehension
// symbolic instead of silently producing an empty list.
func TestListComprehensionNoIterable(t *testing.T) {
	out := buildTemp(t, `#package T
+App = {
  Class = ReferenceContainer
  Missing = { @x for x in @nosuchlist }
}
`)
	if !strings.Contains(out, "@x for x in @nosuchlist") {
		t.Errorf("unresolvable comprehension was not kept symbolic:\n%s", out)
	}
}

// TestListComprehensionFormatRoundTrip keeps fmt lossless and idempotent.
func TestListComprehensionFormatRoundTrip(t *testing.T) {
	src := `#package T

+App = {
  Class = ReferenceContainer
  Names = { @sig.name for sig in @signals }
  Big = { @n for n in @nums if @n > 1 }
  Indexed = { @i for i, n in @nums }
  Plain = { 1, 2 }
}
`
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var sb strings.Builder
	formatter.Format(cfg, &sb)
	out := sb.String()

	for _, want := range []string{
		"{ @sig.name for sig in @signals }",
		"{ @n for n in @nums if (@n > 1) }",
		"{ @i for i, n in @nums }",
		"{ 1, 2 }",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatted output missing %q:\n%s", want, out)
		}
	}

	cfg2, err := parser.NewParser(out).Parse()
	if err != nil {
		t.Fatalf("re-parse formatted output: %v\n%s", err, out)
	}
	var sb2 strings.Builder
	formatter.Format(cfg2, &sb2)
	if sb2.String() != out {
		t.Errorf("formatting is not idempotent:\nfirst:\n%s\nsecond:\n%s", out, sb2.String())
	}
}

// writeFile writes name in dir and returns the full path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
