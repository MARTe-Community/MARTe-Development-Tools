package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLSPBinaryDiagnostics(t *testing.T) {
	// 1. Build mdt
	// Ensure we are in test directory context
	buildCmd := exec.Command("go", "build", "-o", "../build/mdt", "../cmd/mdt")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build mdt: %v\nOutput: %s", err, output)
	}

	// 2. Start mdt lsp
	cmd := exec.Command("../build/mdt", "lsp")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	
	// Pipe stderr to test log for debugging
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			t.Logf("LSP STDERR: %s", scanner.Text())
		}
	}()

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start mdt lsp: %v", err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	reader := bufio.NewReader(stdout)

	send := func(m interface{}) {
		body, _ := json.Marshal(m)
		msg := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
				stdin.Write([]byte(msg))
			}
		
			readCh := make(chan map[string]interface{}, 100)
		
			go func() {		for {
			// Parse Header
			line, err := reader.ReadString('\n')
			if err != nil {
				close(readCh)
				return
			}
			var length int
			// Handle Content-Length: <len>\r\n
			if _, err := fmt.Sscanf(strings.TrimSpace(line), "Content-Length: %d", &length); err != nil {
				// Maybe empty line or other header?
				continue
			}
			
			// Read until empty line (\r\n)
			for {
				l, err := reader.ReadString('\n')
				if err != nil {
					close(readCh)
					return
				}
				if l == "\r\n" {
					break
				}
			}
			
			body := make([]byte, length)
			if _, err := io.ReadFull(reader, body); err != nil {
				close(readCh)
				return
			}
			
			var m map[string]interface{}
			if err := json.Unmarshal(body, &m); err == nil {
				readCh <- m
			}
		}
	}()

	cwd, _ := os.Getwd()
	projectRoot := filepath.Dir(cwd)
	absPath := filepath.Join(projectRoot, "examples/app_test.marte")
	uri := "file://" + absPath

	// 3. Initialize
	examplesDir := filepath.Join(projectRoot, "examples")
	send(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"rootUri": "file://" + examplesDir,
		},
	})

	// 4. Open app_test.marte
	content, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("Failed to read test file: %v", err)
	}
	send(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"uri":        uri,
				"languageId": "marte",
				"version":    1,
				"text":       string(content),
			},
		},
	})

	// 5. Wait for diagnostics
	foundOrdering := false
	foundVariable := false

	timeout := time.After(30 * time.Second)
	
	for {
		select {
		case msg, ok := <-readCh:
			if !ok {
				t.Fatal("LSP stream closed unexpectedly")
			}
			t.Logf("Received: %v", msg)
			if method, ok := msg["method"].(string); ok && method == "textDocument/publishDiagnostics" {
				params := msg["params"].(map[string]interface{})
				// Check URI match?
				// if params["uri"] != uri { continue } // Might be absolute vs relative
				
diags := params["diagnostics"].([]interface{})
				for _, d := range diags {
					m := d.(map[string]interface{})["message"].(string)
					if strings.Contains(m, "INOUT Signal 'A'") {
						foundOrdering = true
						t.Log("Found Ordering error")
					}
					if strings.Contains(m, "Unresolved variable reference: '$Value'") {
						foundVariable = true
						t.Log("Found Variable error")
					}
				}
				if foundOrdering && foundVariable {
					return // Success
				}
			}
		case <-timeout:
			t.Fatal("Timeout waiting for diagnostics")
		}
	}
}