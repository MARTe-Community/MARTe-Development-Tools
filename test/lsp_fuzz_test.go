package integration

import (
	"math/rand"
	"testing"
	"time"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
)

func TestIncrementalFuzz(t *testing.T) {
	// Initialize
	lsp.Documents = make(map[string]string)
	uri := "file://fuzz.marte"
	currentText := ""
	lsp.Documents[uri] = currentText

	rand.Seed(time.Now().UnixNano())

	// Apply 1000 random edits
	for i := 0; i < 1000; i++ {
		// Randomly choose Insert or Delete
		isInsert := rand.Intn(2) == 0
		
		change := lsp.TextDocumentContentChangeEvent{}
		
		// Use simple ascii string
		length := len(currentText)
        
		if isInsert || length == 0 {
			// Insert
			pos := 0
			if length > 0 {
				pos = rand.Intn(length + 1)
			}
		
			insertStr := "X"
			if rand.Intn(5) == 0 { insertStr = "\n" }
			if rand.Intn(10) == 0 { insertStr = "longstring" }
		
            // Calculate Line/Char for pos
            line, char := offsetToLineChar(currentText, pos)
            
			change.Range = &lsp.Range{
				Start: lsp.Position{Line: line, Character: char},
				End:   lsp.Position{Line: line, Character: char},
			}
			change.Text = insertStr
			
			// Expected
			currentText = currentText[:pos] + insertStr + currentText[pos:]
		} else {
			// Delete
			start := rand.Intn(length)
			end := start + 1 + rand.Intn(length - start) // at least 1 char
			
			// Range
            l1, c1 := offsetToLineChar(currentText, start)
            l2, c2 := offsetToLineChar(currentText, end)
            
			change.Range = &lsp.Range{
				Start: lsp.Position{Line: l1, Character: c1},
				End:   lsp.Position{Line: l2, Character: c2},
			}
			change.Text = ""
			
			currentText = currentText[:start] + currentText[end:]
		}
		
		// Apply
		lsp.HandleDidChange(lsp.DidChangeTextDocumentParams{
			TextDocument:   lsp.VersionedTextDocumentIdentifier{URI: uri, Version: i},
			ContentChanges: []lsp.TextDocumentContentChangeEvent{change},
		})
		
		// Verify
		if lsp.Documents[uri] != currentText {
			t.Fatalf("Fuzz iteration %d failed.\nExpected len: %d\nGot len:      %d\nChange: %+v", i, len(currentText), len(lsp.Documents[uri]), change)
		}
	}
}

func offsetToLineChar(text string, offset int) (int, int) {
    line := 0
    char := 0
    for i, r := range text {
        if i == offset {
            return line, char
        }
        if r == '\n' {
            line++
            char = 0
        } else {
            char++
        }
    }
    if offset == len(text) {
        return line, char
    }
    return -1, -1
}
