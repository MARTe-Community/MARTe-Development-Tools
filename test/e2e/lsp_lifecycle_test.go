package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

// lifecycleFixture is a project with a package fragment and with/foreach
// expansion, i.e. entities whose source position is 0.
const lifecycleFixture = `#package Probe
+App = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    DefaultDataSource = DDB
    +DDB = {
      Class = GAMDataSource
      Signals = {
        T: uint32
        Data: float64
      }
    }
    +TimingDataSource = {
      Class = TimingDataSource
    }
  }
  +Functions = {
    Class = ReferenceContainer
    GAM = {
      Class = IOGAM
      InputSignals = {
        DDB::T
      }
      OutputSignals = {
        DDB::Data
      }
    }
  }
  +States = {
    Class = ReferenceContainer
  }
  +Scheduler = {
    Class = GAMScheduler
    TimingDataSource = TimingDataSource
  }
}
`

// TestLSPShutdownIsGraceful pins the LSP lifecycle contract: the shutdown
// request must be answered (a response carrying "result"), and the exit
// notification must terminate the process. Editors report "language server
// failed to terminate gracefully" when either is missing, and a server that
// keeps running after exit would leave stale state behind on a restart.
func TestLSPShutdownIsGraceful(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()
	ctx.CreateFile("app.marte", lifecycleFixture)

	client := ctx.RunLSP()

	raw, err := client.Shutdown()
	if err != nil {
		t.Fatalf("shutdown request failed: %v", err)
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("shutdown response is not valid JSON: %v\n%s", err, raw)
	}
	if resp.Error != nil {
		t.Errorf("shutdown returned an error: %s", resp.Error)
	}
	// JSON-RPC 2.0 requires result or error to be present; omitting both (the
	// go-lsp default for a nil result) makes clients reject the response.
	if resp.Result == nil && resp.Error == nil {
		t.Errorf("shutdown response carries neither result nor error: %s", raw)
	}

	if err := client.Exit(5 * time.Second); err != nil {
		t.Errorf("server did not terminate after exit: %v", err)
	}
}

// TestLSPResponsesUseValidPositions guards the reported protocol error
// "invalid value: integer `-1`, expected u32": LSP positions are unsigned, and
// a single negative coordinate makes clients discard the entire message. The
// package fragment and generated nodes have no source position (0), which
// turns into -1 when converted naively.
func TestLSPResponsesUseValidPositions(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()
	ctx.CreateFile("app.marte", lifecycleFixture)

	client := ctx.RunLSP()
	client.OpenFile("app.marte", lifecycleFixture)

	requests := []struct {
		method string
		params map[string]interface{}
	}{
		{"textDocument/documentSymbol", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + ctx.RootDir() + "/app.marte"},
		}},
		{"textDocument/inlayHint", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + ctx.RootDir() + "/app.marte"},
			"range": map[string]interface{}{
				"start": map[string]interface{}{"line": 0, "character": 0},
				"end":   map[string]interface{}{"line": 40, "character": 0},
			},
		}},
		{"textDocument/references", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + ctx.RootDir() + "/app.marte"},
			"position":     map[string]interface{}{"line": 9, "character": 8},
			"context":      map[string]interface{}{"includeDeclaration": true},
		}},
		{"textDocument/definition", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + ctx.RootDir() + "/app.marte"},
			"position":     map[string]interface{}{"line": 26, "character": 10},
		}},
		{"textDocument/formatting", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + ctx.RootDir() + "/app.marte"},
			"options":      map[string]interface{}{"tabSize": 2, "insertSpaces": true},
		}},
		{"textDocument/typeDefinition", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + ctx.RootDir() + "/app.marte"},
			"position":     map[string]interface{}{"line": 9, "character": 8},
		}},
		{"textDocument/prepareCallHierarchy", map[string]interface{}{
			"textDocument": map[string]interface{}{"uri": "file://" + ctx.RootDir() + "/app.marte"},
			"position":     map[string]interface{}{"line": 21, "character": 6},
		}},
		{"workspace/symbol", map[string]interface{}{"query": "DDB"}},
	}

	// Diagnostics arrive as notifications, not responses -- the same u32
	// validation applies to their ranges.
	var diagPayloads []json.RawMessage
	var diagMu sync.Mutex
	client.OnNotification("textDocument/publishDiagnostics", func(params json.RawMessage) {
		diagMu.Lock()
		diagPayloads = append(diagPayloads, params)
		diagMu.Unlock()
	})

	for _, req := range requests {
		t.Run(req.method, func(t *testing.T) {
			raw, err := client.RawRequest(req.method, req.params)
			if err != nil {
				t.Fatalf("%s failed: %v", req.method, err)
			}
			if bad := negativePositions(raw); len(bad) > 0 {
				t.Errorf("%s returned negative positions %v\n%s", req.method, bad, raw)
			}
		})
	}

	time.Sleep(500 * time.Millisecond)
	diagMu.Lock()
	defer diagMu.Unlock()
	if len(diagPayloads) == 0 {
		t.Error("no diagnostics notification received")
	}
	for _, params := range diagPayloads {
		if bad := negativePositions(params); len(bad) > 0 {
			t.Errorf("diagnostics notification has negative positions %v\n%s", bad, params)
		}
	}
}

// TestNegativePositionDetector keeps the scanner above honest: it must flag a
// payload the server would be rejected for.
func TestNegativePositionDetector(t *testing.T) {
	bad := negativePositions(json.RawMessage(`{"range":{"start":{"line":-1,"character":2}}}`))
	if len(bad) != 1 {
		t.Fatalf("scanner missed a negative line: %v", bad)
	}
	bad = negativePositions(json.RawMessage(`{"range":{"start":{"line":0,"character":-1}}}`))
	if len(bad) != 1 {
		t.Fatalf("scanner missed a negative character: %v", bad)
	}
	if bad := negativePositions(json.RawMessage(`{"range":{"start":{"line":0,"character":0}}}`)); len(bad) != 0 {
		t.Errorf("scanner reported %v for valid coordinates", bad)
	}
}

// negativePositions reports every line/character value below zero in a
// response payload.
func negativePositions(raw json.RawMessage) []string {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var bad []string
	var walk func(node interface{}, path string)
	walk = func(node interface{}, path string) {
		switch n := node.(type) {
		case map[string]interface{}:
			for k, val := range n {
				if k == "line" || k == "character" {
					if f, ok := val.(float64); ok && f < 0 {
						bad = append(bad, fmt.Sprintf("%s.%s=%v", path, k, f))
					}
				}
				walk(val, path+"."+k)
			}
		case []interface{}:
			for i, val := range n {
				walk(val, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(v, "")
	return bad
}

var _ = strings.TrimSpace

// TestLSPReportsSlowRequests covers the "request timeout, but no logs" case:
// editors report a timeout without saying which request hung, and routine
// logging is off by default, so the server must report its own overruns.
func TestLSPReportsSlowRequests(t *testing.T) {
	// Every request counts as slow with a zero threshold.
	t.Setenv("MDT_SLOW_REQUEST_MS", "0")

	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()
	ctx.CreateFile("app.marte", lifecycleFixture)

	client := ctx.RunLSP()
	if _, err := client.RawRequest("textDocument/documentSymbol", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": "file://" + ctx.RootDir() + "/app.marte"},
	}); err != nil {
		t.Fatalf("request failed: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(client.Stderr(), "slow request textDocument/documentSymbol") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("no slow-request line on stderr, got:\n%s", client.Stderr())
}

// TestLSPInitializeScalesWithWorkspaceSize guards the indexing cost: adding a
// file used to walk the whole tree to remove its previous fragments, so
// indexing N files was O(N^2) -- 1200 files took 3.4s, and bigger workspaces
// never finished before the editor's request timeout. Loading files that are
// not in the tree yet now skips that walk entirely.
func TestLSPInitializeScalesWithWorkspaceSize(t *testing.T) {
	ctx := framework.NewTestContext(t)
	defer ctx.Cleanup()

	const files = 800
	for i := 0; i < files; i++ {
		dir := fmt.Sprintf("mod%d", i/50)
		if _, err := os.Stat(filepath.Join(ctx.RootDir(), dir)); err != nil {
			if err := os.MkdirAll(filepath.Join(ctx.RootDir(), dir), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}
		ctx.CreateFile(fmt.Sprintf("%s/f%d.marte", dir, i), fmt.Sprintf(`#package P%d
+App%d = {
  Class = RealTimeApplication
  +Data = {
    Class = ReferenceContainer
    +DDB%d = {
      Class = GAMDataSource
      Signals = {
        T: uint32
      }
    }
  }
}
`, i, i, i))
	}

	start := time.Now()
	client := ctx.RunLSP() // constructor performs initialize, i.e. the scan
	elapsed := time.Since(start)
	_ = client

	// Measured ~0.05s after the fix; ~1.6s before it for 800 files (and
	// quadratic beyond that).
	if elapsed > 900*time.Millisecond {
		t.Errorf("initialize over %d files took %v; the workspace scan is quadratic again", files, elapsed)
	}
}
