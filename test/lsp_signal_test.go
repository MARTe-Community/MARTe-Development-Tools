package integration

import (
	"testing"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
)

func TestLSPSignalMetadata(t *testing.T) {
	content := `
+MySignal = {
    Class = Signal
    Type = uint32
    NumberOfElements = 10
    NumberOfDimensions = 1
    DataSource = DDB1
}
`
	p := parser.NewParser(content)
	config, err := p.Parse()
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	idx := index.NewProjectTree()
	file := "signal.marte"
	idx.AddFile(file, config)
	
	res := idx.Query(file, 2, 2) // Query +MySignal
	if res == nil || res.Node == nil {
		t.Fatal("Query failed for signal definition")
	}
	
	meta := res.Node.Metadata
	if meta["Class"] != "Signal" {
		t.Errorf("Expected Class Signal, got %s", meta["Class"])
	}
	if meta["Type"] != "uint32" {
		t.Errorf("Expected Type uint32, got %s", meta["Type"])
	}
	if meta["NumberOfElements"] != "10" {
		t.Errorf("Expected 10 elements, got %s", meta["NumberOfElements"])
	}
	
	// Since handleHover logic is in internal/lsp which we can't easily test directly without
	// exposing formatNodeInfo, we rely on the fact that Metadata is populated correctly.
	// If Metadata is correct, server.go logic (verified by code review) should display it.
}
