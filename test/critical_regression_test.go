package integration

import (
	"context"
	"sync"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/lsp/cache"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

// TestViewSnapshotNoPanic is a regression test for BUG-010.
func TestViewSnapshotNoPanic(t *testing.T) {
	s := cache.NewSession("test-snap")
	v := s.CreateView("v1", "/test")
	snap := v.Snapshot()
	if snap == nil {
		t.Fatal("CreateView produced view with nil snapshot")
	}
	if snap.Tree() == nil {
		t.Fatal("Snapshot has nil tree")
	}
}

// TestSessionViewsIsMutable is a regression test for BUG-011.
func TestSessionViewsIsMutable(t *testing.T) {
	s := cache.NewSession("test-views")
	s.CreateView("v1", "/a")
	s.CreateView("v2", "/b")

	views1 := s.Views()
	n1 := len(views1)

	s.CreateView("v3", "/c")

	if len(views1) != n1 {
		t.Errorf("BUG-011 confirmed: Views() returned mutable slice (len %d -> %d after CreateView)", n1, len(views1))
	}
}

// TestLSPFunctionPointerConcurrentRead is a regression test for BUG-014 and BUG-015.
func TestLSPFunctionPointerConcurrentRead(t *testing.T) {
	lsp.ResetTestServer()

	uri := "file:///race_test.marte"
	content := "+Node = { Class = Test }\n"
	lsp.GetTestDocuments()[uri] = content

	var mu sync.Mutex
	var publishCalls, graphCalls int

	origPublish := lsp.PublishDiagnosticsFn
	origGraph := lsp.GraphNotifyFn
	defer func() {
		lsp.PublishDiagnosticsFn = origPublish
		lsp.GraphNotifyFn = origGraph
	}()

	lsp.PublishDiagnosticsFn = func(ctx context.Context, fileURI string, diags []lsp.LSPDiagnostic) {
		mu.Lock()
		publishCalls++
		mu.Unlock()
	}
	lsp.GraphNotifyFn = func(event, data string) {
		mu.Lock()
		graphCalls++
		mu.Unlock()
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lsp.HandleHover(lsp.HoverParams{
				TextDocument: lsp.TextDocumentIdentifier{URI: uri},
				Position:     lsp.Position{Line: 0, Character: 0},
			})
		}()
	}
	wg.Wait()

	t.Logf("publishCalls=%d graphCalls=%d", publishCalls, graphCalls)
}

// TestIndexRemoveFileDanglingPointer is a regression test for BUG-016.
func TestIndexRemoveFileDanglingPointer(t *testing.T) {
	tree := index.NewProjectTree()

	content := `
+Obj1 = { Class = TestClass }
+Obj2 = { Class = TestClass, Ref = Obj1 }
`
	p := parser.NewParser(content)
	cfg, _ := p.Parse()

	file := "remove_test.marte"
	tree.AddFile(file, cfg)
	tree.ResolveFields(nil)
	tree.ResolveReferences(nil)

	// Without #package, nodes go into IsolatedFiles, not under Root.
	// Search from the isolated file root.
	iso, ok := tree.IsolatedFiles[file]
	if !ok {
		t.Fatal("Isolated file not found")
	}
	node1 := tree.FindNode(iso, "Obj1", nil, true)
	if node1 == nil {
		t.Fatal("Obj1 not found before removal")
	}

	tree.RemoveFile(file)

	removed := tree.FindNode(iso, "Obj1", nil, true)
	if removed != nil {
		t.Errorf("BUG-016 confirmed: FindNode returned non-nil after RemoveFile: %s", removed.RealName)
	}
}

// TestIndexConcurrentAddFileNoRace is a regression test for BUG-002.
func TestIndexConcurrentAddFileNoRace(t *testing.T) {
	tree := index.NewProjectTree()

	names := []string{"a.marte", "b.marte", "c.marte", "d.marte", "e.marte"}
	contents := []string{
		"+NodeA = { Class = A }\n",
		"+NodeB = { Class = B }\n",
		"+NodeC = { Class = C }\n",
		"+NodeD = { Class = D }\n",
		"+NodeE = { Class = E }\n",
	}

	configs := make([]*parser.Configuration, len(names))
	for i, c := range contents {
		p := parser.NewParser(c)
		cfg, _ := p.Parse()
		configs[i] = cfg
	}

	var wg sync.WaitGroup
	for i := range names {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tree.AddFile(names[idx], configs[idx])
		}(i)
	}
	wg.Wait()

	tree.ResolveFields(nil)
	tree.ResolveReferences(nil)

	count := 0
	tree.Walk(func(n *index.ProjectNode) {
		if n.RealName != "" {
			count++
		}
	})

	if count < len(names) {
		t.Errorf("BUG-002: concurrent AddFile lost nodes: expected >=%d, got %d", len(names), count)
	}
	t.Logf("Concurrent AddFile: %d nodes", count)
}

// TestSchemaGlobalConcurrentRead is a regression test for BUG-003.
func TestSchemaGlobalConcurrentRead(t *testing.T) {
	lsp.ResetTestServer()
	lsp.GlobalSchema = schema.LoadFullSchema(".")

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if lsp.GlobalSchema != nil {
				_ = lsp.GlobalSchema.Value
			}
		}()
	}
	wg.Wait()
}

// TestValidatorWorkerPoolNoDeadlock is a regression test for BUG-005.
func TestValidatorWorkerPoolNoDeadlock(t *testing.T) {
	lsp.ResetTestServer()
	lsp.GlobalSchema = schema.LoadFullSchema(".")

	uri := "file:///worker_test.marte"
	content := `
#package Test.Worker
+App = {
    Class = RealTimeApplication
    +States = {
        +Run = {
            +Threads = {
                +T1 = {
                    Functions = { GAM1 GAM2 }
                }
            }
        }
    }
}
+GAM1 = {
    Class = IOGAM
    InputSignals = { S1 = { Type = int32 } }
    OutputSignals = { S2 = { Type = int32 } }
}
+GAM2 = {
    Class = IOGAM
    InputSignals = { S2 = { Type = int32 } }
    OutputSignals = { S3 = { Type = int32 } }
}
`
	lsp.GetTestDocuments()[uri] = content

	done := make(chan struct{})
	go func() {
		defer close(done)
		lsp.HandleDidOpen(lsp.DidOpenTextDocumentParams{
			TextDocument: lsp.TextDocumentItem{
				URI:     uri,
				Text:    content,
				Version: 1,
			},
		})
	}()
	<-done

	t.Log("Validator worker pool completed without deadlock")
}
