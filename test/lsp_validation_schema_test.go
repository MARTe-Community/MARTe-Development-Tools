package integration

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
)

func TestLSPValidationSchema(t *testing.T) {
	// Setup LSP
	lsp.ResetTestServer()
	// Documents reset via ResetTestServer
	var buf bytes.Buffer
	lsp.Output = &buf

	// Create a test file with various validation errors
	content := `
#package Test
+Obj = {
    Class = "UnknownClass"   // Unknown class warning
    Type = "InvalidType"     // Invalid type error
    Ref = UnresolvedRef    // Unresolved reference error
    ValidRef = Obj           // Valid reference
}
`
	uri := "file://validation.marte"
	
	// Open document (triggers validation)
	params := lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:  uri,
			Text: content,
		},
	}
	
	lsp.HandleDidOpen(params)
	
	// Check output for diagnostics
	output := buf.String()
	
	// Parse JSON-RPC notifications
	lines := strings.Split(output, "Content-Length:")
	foundUnknownClass := false
	foundInvalidType := false
	foundUnresolvedRef := false
	
	for _, part := range lines {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Skip length number
		idx := strings.Index(part, "{")
		if idx == -1 {
			continue
		}
		jsonStr := part[idx:]
		
		var msg lsp.JsonRpcMessage
		if err := json.Unmarshal([]byte(jsonStr), &msg); err == nil {
			if msg.Method == "textDocument/publishDiagnostics" {
				var params lsp.PublishDiagnosticsParams
				if err := json.Unmarshal(msg.Params, &params); err == nil {
					if params.URI == uri {
						for _, d := range params.Diagnostics {
							if strings.Contains(d.Message, "Unknown Class 'UnknownClass'") {
								foundUnknownClass = true
								if d.Severity != 2 { // Warning
									t.Errorf("Expected Unknown Class to be Warning (2), got %d", d.Severity)
								}
							}
							if strings.Contains(d.Message, "Invalid Type 'InvalidType'") {
								foundInvalidType = true
								if d.Severity != 1 { // Error
									t.Errorf("Expected Invalid Type to be Error (1), got %d", d.Severity)
								}
							}
							if strings.Contains(d.Message, "Unknown reference 'UnresolvedRef'") {
								foundUnresolvedRef = true
								if d.Severity != 1 { // Error
									t.Errorf("Expected Unresolved Ref to be Error (1), got %d", d.Severity)
								}
							}
						}
					}
				}
			}
		}
	}
	
	if !foundUnknownClass {
		t.Error("Did not find diagnostic for 'Unknown Class'")
	}
	if !foundInvalidType {
		t.Error("Did not find diagnostic for 'Invalid Type'")
	}
	if !foundUnresolvedRef {
		t.Error("Did not find diagnostic for 'Unknown reference'")
	}
}