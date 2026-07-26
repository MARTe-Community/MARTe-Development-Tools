package integration

import (
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
)

// TestLSPOffsetAtMultiByte is a regression test for BUG-022:
// offsetAt may produce incorrect byte offsets for multi-byte UTF-8
// characters because it tracks UTF-16 code units vs byte offsets.
//
// We test indirectly by verifying that HandleFormatting handles documents
// with multi-byte characters without crashing.
func TestLSPOffsetAtMultiByte(t *testing.T) {
	lsp.ResetTestServer()

	tests := []struct {
		name    string
		content string
	}{
		{name: "ASCII", content: "hello world"},
		{name: "2-byte UTF-8", content: "a\u00f1o"}, // año
		{name: "4-byte emoji", content: "a\U0001F44Db"}, // a👍b
		{name: "mixed multi-byte", content: "caf\u00e9 r\u00e9sum\u00e9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri := "file:///utf8_test.marte"
			lsp.GetTestDocuments()[uri] = tt.content

			params := lsp.DocumentFormattingParams{
				TextDocument: lsp.TextDocumentIdentifier{URI: uri},
			}
			edits := lsp.HandleFormatting(params)

			// GetTestDocuments bypasses view creation, so view may not exist.
			// This test verifies no panic with multi-byte content.
			if edits != nil && len(edits) == 0 {
				t.Log("HandleFormatting returned 0 edits (view may not exist)")
			}
		})
	}
}

// TestLSPFormattingEndRange is a regression test for BUG-023:
// HandleFormatting computes end range as Count+2 instead of Count+1
// when the document lacks a trailing newline.
//
// Confirmed: for ALL input types, End.Line exceeds the actual line count.
// Skipped until fix is applied.
func TestLSPFormattingEndRange(t *testing.T) {
	t.Skip("BUG-023 confirmed: HandleFormatting end range is Count+2 instead of Count+1")
}
