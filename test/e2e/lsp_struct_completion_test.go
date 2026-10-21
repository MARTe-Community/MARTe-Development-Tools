package e2e

import (
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

// `@var.` over a real stdio LSP session must list the struct fields with a
// text edit covering only the member part; without it clients filter the
// candidates against "@var." and show nothing.
func TestLSPStructFieldCompletionOverTheWire(t *testing.T) {
	const base = `#package Demo

var hardware: {
  address: uint16
  port: uint16
  sample_rate: uint8
}

+App = {
  Class = RealTimeApplication
  Port = @hardware.
}
`
	const portLine = `  Port = @hardware.`

	complete := func(t *testing.T, content string, line, char int) []framework.CompletionItem {
		t.Helper()
		ctx := framework.NewTestContext(t)
		defer ctx.Cleanup()
		tf := framework.WrapT(t, ctx)

		path := tf.CreateFile("completion_struct.marte", content)
		client := tf.RunLSP()
		defer client.Close()
		client.OpenFile(path, content)

		items, err := client.Completion(path, line, char)
		if err != nil {
			t.Fatalf("completion: %v", err)
		}
		return items
	}

	t.Run("empty member", func(t *testing.T) {
		items := complete(t, base, 11, len(portLine)+1)
		want := map[string]string{"address": "uint16", "port": "uint16", "sample_rate": "uint8"}
		seen := make(map[string]bool, len(items))
		for _, item := range items {
			detail, ok := want[item.Label]
			if !ok {
				t.Errorf("unexpected completion %q", item.Label)
				continue
			}
			seen[item.Label] = true
			if item.Detail != detail {
				t.Errorf("%s detail = %q, want %q", item.Label, item.Detail, detail)
			}
			if item.TextEdit == nil {
				t.Fatalf("%s: expected a textEdit so clients filter on the member", item.Label)
			}
			if item.TextEdit.NewText != item.Label {
				t.Errorf("%s: textEdit newText = %q", item.Label, item.TextEdit.NewText)
			}
			if item.FilterText != item.Label {
				t.Errorf("%s: filterText = %q", item.Label, item.FilterText)
			}
			// Empty member: the edit is an insertion at the cursor.
			if got := item.TextEdit.Range.Start.Character; got != len(portLine) {
				t.Errorf("%s: textEdit starts at %d, want %d (after the dot)", item.Label, got, len(portLine))
			}
			if got := item.TextEdit.Range.End.Character; got != len(portLine) {
				t.Errorf("%s: textEdit ends at %d, want %d (cursor)", item.Label, got, len(portLine))
			}
		}
		for name := range want {
			if !seen[name] {
				t.Errorf("missing field completion %q", name)
			}
		}
	})

	t.Run("partial member", func(t *testing.T) {
		content := strings.Replace(base, "@hardware.", "@hardware.por", 1)
		items := complete(t, content, 11, len(portLine)+3+1)
		if len(items) != 3 {
			t.Fatalf("expected the three fields for `@hardware.por`, got %d", len(items))
		}
		for _, item := range items {
			if item.TextEdit == nil {
				t.Fatalf("%s: expected textEdit", item.Label)
			}
			// The range covers exactly "por".
			if got, want := item.TextEdit.Range.Start.Character, len(portLine); got != want {
				t.Errorf("%s: edit starts at %d, want %d", item.Label, got, want)
			}
			if got, want := item.TextEdit.Range.End.Character, len(portLine)+3; got != want {
				t.Errorf("%s: edit ends at %d, want %d", item.Label, got, want)
			}
		}
	})

	t.Run("nested through named type", func(t *testing.T) {
		content := `#package Demo

type Cfg_T {
  host: string
  port: uint16
}

var cfg: { net: Cfg_T }

+App = {
  Class = RealTimeApplication
  Host = @cfg.net.
}
`
		items := complete(t, content, 12, len("  Host = @cfg.net.")+1)
		labels := make(map[string]bool, len(items))
		for _, item := range items {
			labels[item.Label] = true
		}
		for _, want := range []string{"host", "port"} {
			if !labels[want] {
				t.Errorf("missing %q from @cfg.net. completion: %v", want, labels)
			}
		}
	})
}
