package framework

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/lsp"
)

type MDTPath string

var DefaultMDTPath MDTPath = "build/mdt"

func GetMDTPath() string {
	if path := os.Getenv("MDT_BINARY"); path != "" {
		return path
	}

	// If relative path, find project root by looking for go.mod
	path := string(DefaultMDTPath)
	if !filepath.IsAbs(path) {
		// Start from current working dir and walk up
		cwd, err := os.Getwd()
		if err == nil {
			for dir := cwd; dir != "/"; dir = filepath.Dir(dir) {
				if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
					absPath := filepath.Join(dir, path)
					if _, err := os.Stat(absPath); err == nil {
						return absPath
					}
				}
			}
		}
		// Fallback to current dir
		absPath, _ := filepath.Abs(path)
		return absPath
	}
	return path
}

type TestContext struct {
	tempDir     string
	fixtureDir  string
	mdtPath     string
	capturedLog string
}

func NewTestContext(t *testing.T) *TestContext {
	tempDir, err := os.MkdirTemp("", "mdt-e2e-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	return &TestContext{
		tempDir:     tempDir,
		mdtPath:     GetMDTPath(),
		capturedLog: "",
	}
}

func (tc *TestContext) Cleanup() {
	if tc.tempDir != "" {
		os.RemoveAll(tc.tempDir)
	}
}

func (tc *TestContext) RootDir() string {
	return tc.tempDir
}

func (tc *TestContext) CreateFile(name, content string) string {
	path := filepath.Join(tc.tempDir, name)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		panic(fmt.Sprintf("Failed to create dir %s: %v", dir, err))
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		panic(fmt.Sprintf("Failed to write file %s: %v", path, err))
	}
	return path
}

func (tc *TestContext) CreateSubdir(name string) string {
	path := filepath.Join(tc.tempDir, name)
	if err := os.MkdirAll(path, 0755); err != nil {
		panic(fmt.Sprintf("Failed to create dir %s: %v", path, err))
	}
	return path
}

func (tc *TestContext) ReadFile(name string) (string, error) {
	path := filepath.Join(tc.tempDir, name)
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

type BuildResult struct {
	Output      string
	Stderr      string
	ExitCode    int
	Files       map[string]string
	Diagnostics []Diagnostic
}

type Diagnostic struct {
	File     string
	Line     int
	Column   int
	Message  string
	Severity string
}

func (tc *TestContext) RunBuild(args ...string) *BuildResult {
	var stdout, stderr bytes.Buffer

	cmd := exec.Command(tc.mdtPath, append([]string{"build"}, args...)...)
	cmd.Dir = tc.tempDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &BuildResult{
				Output:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: exitErr.ExitCode(),
			}
		}
	}

	return &BuildResult{
		Output:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}
}

func (tc *TestContext) RunBuildOutput(args ...string) (string, error) {
	outputFile := filepath.Join(tc.tempDir, ".e2e_output.tmp")

	var stderr bytes.Buffer

	cmd := exec.Command(tc.mdtPath, append([]string{"build", "-o", outputFile}, args...)...)
	cmd.Dir = tc.tempDir
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build failed: %s\nstderr: %s", err.Error(), stderr.String())
	}

	content, err := os.ReadFile(outputFile)
	if err != nil {
		return "", fmt.Errorf("failed to read output: %w", err)
	}

	return string(content), nil
}

type CheckResult struct {
	Stdout      string
	Stderr      string
	ExitCode    int
	Diagnostics []Diagnostic
}

func (tc *TestContext) RunCheck(args ...string) *CheckResult {
	var stdout, stderr bytes.Buffer

	cmd := exec.Command(tc.mdtPath, append([]string{"check"}, args...)...)
	cmd.Dir = tc.tempDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	cmd.Run()

	// mdt check outputs to stdout
	diags := parseDiagnostics(stdout.String())

	return &CheckResult{
		Stdout:      stdout.String(),
		Stderr:      stderr.String(),
		ExitCode:    0,
		Diagnostics: diags,
	}
}

func parseDiagnostics(output string) []Diagnostic {
	var diags []Diagnostic
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if !strings.Contains(line, "ERROR:") && !strings.Contains(line, "WARNING:") {
			continue
		}

		// Format: [mdt] timestamp file:line:col: ERROR: message
		// Find the "ERROR:" or "WARNING:" position
		var severity, msgStart string
		if idx := strings.Index(line, "ERROR:"); idx >= 0 {
			severity = "error"
			msgStart = line[idx+7:]
		} else if idx := strings.Index(line, "WARNING:"); idx >= 0 {
			severity = "warning"
			msgStart = line[idx+9:]
		} else {
			continue
		}

		// Extract file:line:col part
		// Find file:line:col: which is before ERROR:
		errIdx := strings.Index(line, ": ERROR:")
		warnIdx := strings.Index(line, ": WARNING:")
		idx := errIdx
		if idx < 0 {
			idx = warnIdx
		}
		if idx < 0 {
			continue
		}

		beforeError := line[:idx]
		// Find the last colon which separates column from ERROR
		lastColon := strings.LastIndex(beforeError, ":")
		if lastColon < 0 {
			continue
		}

		// Extract line and column
		fileLineCol := beforeError[:lastColon]
		lastColon2 := strings.LastIndex(fileLineCol, ":")
		if lastColon2 < 0 {
			continue
		}

		lineStr := fileLineCol[lastColon2+1:]
		var lineNum int
		fmt.Sscanf(lineStr, "%d", &lineNum)

		// Extract file
		file := fileLineCol[:lastColon2]
		// Remove [mdt] timestamp prefix if present
		if idx := strings.Index(file, "] "); idx >= 0 {
			file = strings.TrimSpace(file[idx+2:])
		}

		diags = append(diags, Diagnostic{
			File:     file,
			Line:     lineNum,
			Message:  strings.TrimSpace(msgStart),
			Severity: severity,
		})
	}
	return diags
}

func (tc *TestContext) RunLSP() *LSPTestClient {
	return NewLSPTestClient(tc.mdtPath, tc.tempDir)
}

func (tc *TestContext) ResetLSP() {
	lsp.ResetTestServer()
}

type FmtResult struct {
	Output  string
	Stderr  string
	Changed bool
}

func (tc *TestContext) RunFmt(args ...string) *FmtResult {
	var stdout, stderr bytes.Buffer

	cmd := exec.Command(tc.mdtPath, append([]string{"fmt"}, args...)...)
	cmd.Dir = tc.tempDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	changed := err == nil && strings.Contains(stderr.String(), "fmt")

	return &FmtResult{
		Output:  stdout.String(),
		Stderr:  stderr.String(),
		Changed: changed,
	}
}

type T struct {
	*testing.T
	ctx *TestContext
}

func WrapT(t *testing.T, ctx *TestContext) *T {
	return &T{T: t, ctx: ctx}
}

func (t *T) RootDir() string {
	return t.ctx.RootDir()
}

func (t *T) CreateFile(name, content string) string {
	return t.ctx.CreateFile(name, content)
}

func (t *T) CreateSubdir(name string) string {
	return t.ctx.CreateSubdir(name)
}

func (t *T) ReadFile(name string) (string, error) {
	return t.ctx.ReadFile(name)
}

func (t *T) RunBuild(args ...string) *BuildResult {
	return t.ctx.RunBuild(args...)
}

func (t *T) RunBuildOutput(args ...string) (string, error) {
	return t.ctx.RunBuildOutput(args...)
}

func (t *T) RunCheck(args ...string) *CheckResult {
	return t.ctx.RunCheck(args...)
}

func (t *T) RunLSP() *LSPTestClient {
	return t.ctx.RunLSP()
}

func (t *T) RunFmt(args ...string) *FmtResult {
	return t.ctx.RunFmt(args...)
}

func (t *T) ResetLSP() {
	t.ctx.ResetLSP()
}

func AssertNoErrors(t *T, result *CheckResult) {
	if result.ExitCode != 0 {
		t.Fatalf("Expected no errors, got exit code %d: %s", result.ExitCode, result.Stderr)
	}
	if len(result.Diagnostics) > 0 {
		var errMsgs []string
		for _, d := range result.Diagnostics {
			errMsgs = append(errMsgs, fmt.Sprintf("%s:%d: %s", d.File, d.Line, d.Message))
		}
		t.Fatalf("Expected no diagnostics, got:\n%s", strings.Join(errMsgs, "\n"))
	}
}

func AssertErrors(t *T, result *CheckResult, expectedPatterns ...string) {
	if len(result.Diagnostics) == 0 {
		t.Fatalf("Expected errors matching %v, but got none", expectedPatterns)
	}

	for _, pattern := range expectedPatterns {
		found := false
		for _, d := range result.Diagnostics {
			if strings.Contains(d.Message, pattern) || strings.Contains(d.File, pattern) {
				found = true
				break
			}
		}
		if !found {
			var diagMsgs []string
			for _, d := range result.Diagnostics {
				diagMsgs = append(diagMsgs, fmt.Sprintf("%s:%d: %s", d.File, d.Line, d.Message))
			}
			t.Fatalf("Expected error containing '%s', but got:\n%s", pattern, strings.Join(diagMsgs, "\n"))
		}
	}
}

func AssertOutput(t *T, result *BuildResult, expectedSubstring string) {
	if !strings.Contains(result.Output, expectedSubstring) {
		t.Fatalf("Expected output to contain:\n%s\n\nGot:\n%s", expectedSubstring, result.Output)
	}
}

func AssertLogContains(t *T, result *BuildResult, expectedSubstring string) {
	fullLog := result.Stderr
	if !strings.Contains(fullLog, expectedSubstring) {
		t.Fatalf("Expected log to contain:\n%s\n\nGot:\n%s", expectedSubstring, fullLog)
	}
}

func AssertLogMatches(t *T, result *BuildResult, pattern string) {
	fullLog := result.Stderr
	if !containsMatch(fullLog, pattern) {
		t.Fatalf("Expected log to match pattern:\n%s\n\nGot:\n%s", pattern, fullLog)
	}
}

func containsMatch(s, pattern string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return strings.Contains(s, pattern)
	}
	for _, p := range parts {
		if p == "" {
			continue
		}
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

type LSPTestSession struct {
	Client  *LSPTestClient
	Context context.Context
	Cancel  context.CancelFunc
}

func NewLSPTestSession(t *T) *LSPTestSession {
	client := t.RunLSP()
	ctx, cancel := context.WithTimeout(context.Background(), 30e9)
	return &LSPTestSession{
		Client:  client,
		Context: ctx,
		Cancel:  cancel,
	}
}

func (s *LSPTestSession) OpenFile(path string, content string) string {
	return s.Client.OpenFile(path, content)
}

func (s *LSPTestSession) EditFile(path string, changes []TextEdit) {
	s.Client.EditFile(path, changes)
}

func (s *LSPTestSession) Close() {
	s.Cancel()
	s.Client.Close()
}

type TextEdit struct {
	Range   Range
	NewText string
}

type Range struct {
	Start Position
	End   Position
}

type Position struct {
	Line      int
	Character int
}
