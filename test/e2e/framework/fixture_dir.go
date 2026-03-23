package framework

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type TestConfig struct {
	Description string   `toml:"description"`
	Tools       []string `toml:"tools"`   // check, build, format, lsp
	Timeout     int      `toml:"timeout"` // in seconds, default 30
}

type ExpectedMessages struct {
	Errors   []ExpectedMessage `toml:"errors"`
	Warnings []ExpectedMessage `toml:"warnings"`
	Infos    []ExpectedMessage `toml:"infos"`
}

type ExpectedMessage struct {
	File    string `toml:"file"`
	Line    int    `toml:"line"`
	Message string `toml:"message"`
}

type ExpectedBuildOutput struct {
	Messages ExpectedMessages  `toml:"messages"`
	Files    map[string]string `toml:"files"` // output files to verify
}

type ExpectedFormatOutput struct {
	Files map[string]string `toml:"files"` // filename -> expected content
}

type LSPEditStep struct {
	Delay    int         `toml:"delay"`   // milliseconds to wait before this step
	Timeout  int         `toml:"timeout"` // max expected reply time in ms (default 5000)
	Action   string      `toml:"action"`  // open, edit, hover, completion, definition
	Path     string      `toml:"path"`
	Line     int         `toml:"line"`
	Char     int         `toml:"char"`
	Content  string      `toml:"content"` // for edit/open
	NewText  string      `toml:"newText"` // for edit
	Expected LSPExpected `toml:"expected"`
}

type LSPExpected struct {
	DiagsCount  int      `toml:"diagnosticsCount"`
	Hover       string   `toml:"hover"`       // expected hover content
	Completions []string `toml:"completions"` // expected completion labels
	Definitions int      `toml:"definitionsCount"`
	Symbols     []string `toml:"symbols"`     // expected symbol names
	CompletedIn int      `toml:"completedIn"` // assert processing time < this value (ms)
}

type ExpectedLSP struct {
	Steps []LSPEditStep `toml:"steps"`
}

type FixtureTest struct {
	Name           string
	Config         TestConfig
	InputDir       string
	Inputs         map[string]string
	ExpectedBuild  ExpectedBuildOutput
	ExpectedCheck  ExpectedMessages
	ExpectedFormat map[string]ExpectedFormatOutput
	ExpectedLSP    ExpectedLSP
}

func LoadFixtureTest(fixturePath string) (*FixtureTest, error) {
	configPath := filepath.Join(fixturePath, "TEST.toml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("TEST.toml not found in %s", fixturePath)
	}

	var config TestConfig
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read TEST.toml: %w", err)
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse TEST.toml: %w", err)
	}

	if len(config.Tools) == 0 {
		return nil, fmt.Errorf("no tools specified in TEST.toml")
	}

	if config.Timeout == 0 {
		config.Timeout = 30
	}

	test := &FixtureTest{
		Name:     filepath.Base(fixturePath),
		Config:   config,
		InputDir: filepath.Join(fixturePath, "inputs"),
		Inputs:   make(map[string]string),
	}

	// Load inputs
	inputsPath := filepath.Join(fixturePath, "inputs")
	if _, err := os.Stat(inputsPath); err == nil {
		if err := filepath.Walk(inputsPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			relPath, err := filepath.Rel(inputsPath, path)
			if err != nil {
				return err
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			test.Inputs[relPath] = string(content)
			return nil
		}); err != nil {
			return nil, fmt.Errorf("failed to load inputs: %w", err)
		}
	}

	// Load expected build output
	if test.hasTool("build") {
		test.ExpectedBuild = test.loadExpectedBuild(fixturePath)
	}

	// Load expected check messages
	if test.hasTool("check") {
		test.ExpectedCheck = test.loadExpectedCheck(fixturePath)
	}

	// Load expected format output
	if test.hasTool("format") {
		test.ExpectedFormat = test.loadExpectedFormat(fixturePath)
	}

	// Load expected LSP
	if test.hasTool("lsp") {
		test.ExpectedLSP = test.loadExpectedLSP(fixturePath)
	}

	return test, nil
}

func (t *FixtureTest) hasTool(tool string) bool {
	for _, t := range t.Config.Tools {
		if t == tool {
			return true
		}
	}
	return false
}

func (t *FixtureTest) loadExpectedBuild(fixturePath string) ExpectedBuildOutput {
	var expected ExpectedBuildOutput

	// Load messages
	msgPath := filepath.Join(fixturePath, "expected", "build", "messages.toml")
	if data, err := os.ReadFile(msgPath); err == nil {
		toml.Unmarshal(data, &expected.Messages)
	}

	// Load output files
	expected.Files = make(map[string]string)
	outPath := filepath.Join(fixturePath, "expected", "build")
	if _, err := os.Stat(outPath); err == nil {
		filepath.Walk(outPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || info.Name() == "messages.toml" {
				return nil
			}
			relPath, _ := filepath.Rel(outPath, path)
			if relPath == "messages.toml" {
				return nil
			}
			content, _ := os.ReadFile(path)
			expected.Files[relPath] = string(content)
			return nil
		})
	}

	return expected
}

func (t *FixtureTest) loadExpectedCheck(fixturePath string) ExpectedMessages {
	var expected ExpectedMessages
	msgPath := filepath.Join(fixturePath, "expected", "check", "messages.toml")
	if data, err := os.ReadFile(msgPath); err == nil {
		toml.Unmarshal(data, &expected)
	}
	return expected
}

func (t *FixtureTest) loadExpectedFormat(fixturePath string) map[string]ExpectedFormatOutput {
	result := make(map[string]ExpectedFormatOutput)

	formatPath := filepath.Join(fixturePath, "expected", "format")
	if _, err := os.Stat(formatPath); err != nil {
		return result
	}

	filepath.Walk(formatPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".marte") {
			content, _ := os.ReadFile(path)
			relPath, _ := filepath.Rel(formatPath, path)
			result[relPath] = ExpectedFormatOutput{
				Files: map[string]string{
					relPath: string(content),
				},
			}
		}
		return nil
	})

	return result
}

func (t *FixtureTest) loadExpectedLSP(fixturePath string) ExpectedLSP {
	var expected ExpectedLSP
	lspPath := filepath.Join(fixturePath, "expected", "lsp", "edit.toml")
	if data, err := os.ReadFile(lspPath); err == nil {
		toml.Unmarshal(data, &expected)
	}
	return expected
}

func (t *FixtureTest) TimeoutDuration() time.Duration {
	return time.Duration(t.Config.Timeout) * time.Second
}

type FixtureTestRunner struct {
	framework *TestContext
	test      *FixtureTest
}

func NewFixtureTestRunner(framework *TestContext, test *FixtureTest) *FixtureTestRunner {
	return &FixtureTestRunner{
		framework: framework,
		test:      test,
	}
}

func (r *FixtureTestRunner) Setup() error {
	// Copy all inputs to temp directory
	for name, content := range r.test.Inputs {
		r.framework.CreateFile(name, content)
	}
	return nil
}

func FindAllFixtures(basePath string) ([]string, error) {
	var fixtures []string

	entries, err := os.ReadDir(basePath)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		testPath := filepath.Join(basePath, entry.Name())
		testTomlPath := filepath.Join(testPath, "TEST.toml")

		if _, err := os.Stat(testTomlPath); err == nil {
			fixtures = append(fixtures, testPath)
		}
	}

	return fixtures, nil
}

func RunFixtureTests(t *testing.T, basePath string) {
	fixtures, err := FindAllFixtures(basePath)
	if err != nil {
		t.Fatalf("Failed to find fixtures: %v", err)
	}

	if len(fixtures) == 0 {
		t.Logf("No fixture tests found in %s", basePath)
		return
	}

	for _, fixturePath := range fixtures {
		fixture, err := LoadFixtureTest(fixturePath)
		if err != nil {
			t.Errorf("Failed to load fixture %s: %v", fixturePath, err)
			continue
		}

		for _, tool := range fixture.Config.Tools {
			testName := fmt.Sprintf("%s/%s", fixture.Name, tool)
			t.Run(testName, func(t *testing.T) {
				ctx := NewTestContext(t)
				defer ctx.Cleanup()

				runner := NewFixtureTestRunner(ctx, fixture)
				if err := runner.Setup(); err != nil {
					t.Fatalf("Failed to setup: %v", err)
				}

				switch tool {
				case "check":
					runner.runCheckTest(t)
				case "build":
					runner.runBuildTest(t)
				case "format":
					runner.runFormatTest(t)
				case "lsp":
					runner.runLSPTest(t)
				default:
					t.Errorf("Unknown tool: %s", tool)
				}
			})
		}
	}
}

func (r *FixtureTestRunner) runCheckTest(t *testing.T) {
	tf := WrapT(t, r.framework)

	// Build args from input files
	var args []string
	for name := range r.test.Inputs {
		args = append(args, name)
	}

	result := tf.RunCheck(args...)

	// Verify diagnostics
	expected := r.test.ExpectedCheck
	actualDiags := result.Diagnostics

	// Check errors
	for _, exp := range expected.Errors {
		found := false
		for _, diag := range actualDiags {
			if diag.Severity == "error" && matchesExp(diag, exp) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected error not found: %v", exp)
		}
	}

	// Check warnings
	for _, exp := range expected.Warnings {
		found := false
		for _, diag := range actualDiags {
			if diag.Severity == "warning" && matchesExp(diag, exp) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected warning not found: %v", exp)
		}
	}
}

func (r *FixtureTestRunner) runBuildTest(t *testing.T) {
	tf := WrapT(t, r.framework)

	var args []string
	for name := range r.test.Inputs {
		args = append(args, name)
	}

	// If there's an expected output file, use -o flag
	if len(r.test.ExpectedBuild.Files) > 0 {
		for name := range r.test.ExpectedBuild.Files {
			// Use output file name as specified in expected
			args = append([]string{"-o", name}, args...)
			break
		}
	}

	result := tf.RunBuild(args...)

	// Check exit code
	if result.ExitCode != 0 && len(r.test.ExpectedBuild.Messages.Errors) == 0 {
		t.Logf("Build failed: %s", result.Stderr)
	}

	// Verify messages
	expected := r.test.ExpectedBuild.Messages
	actualDiags := result.Diagnostics

	for _, exp := range expected.Errors {
		found := false
		for _, diag := range actualDiags {
			if diag.Severity == "error" && matchesExpMessage(diag.Message, exp.Message) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected error not found: %s", exp.Message)
		}
	}

	// Verify output files
	for name, expectedContent := range r.test.ExpectedBuild.Files {
		actualContent, err := tf.ReadFile(name)
		if err != nil {
			t.Errorf("Failed to read output file %s: %v", name, err)
			continue
		}
		if actualContent != expectedContent {
			t.Errorf("Output file %s mismatch.\nExpected:\n%s\n\nActual:\n%s", name, expectedContent, actualContent)
		}
	}
}

func (r *FixtureTestRunner) runFormatTest(t *testing.T) {
	tf := WrapT(t, r.framework)

	for name := range r.test.Inputs {
		_ = tf.RunFmt(name)

		expectedOutput, ok := r.test.ExpectedFormat[name]
		if !ok {
			continue
		}

		expectedContent, ok := expectedOutput.Files[name]
		if !ok {
			continue
		}

		actualContent, err := tf.ReadFile(name)
		if err != nil {
			t.Errorf("Failed to read file %s: %v", name, err)
			continue
		}

		if actualContent != expectedContent {
			t.Errorf("Formatted output mismatch for %s.\nExpected:\n%s\n\nActual:\n%s", name, expectedContent, actualContent)
		}
	}
}

func (r *FixtureTestRunner) runLSPTest(t *testing.T) {
	tf := WrapT(t, r.framework)
	client := tf.RunLSP()
	defer client.Close()

	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	testStart := time.Now()
	totalDelay := time.Duration(0)

	steps := r.test.ExpectedLSP.Steps

	for i, step := range steps {
		stepStart := time.Now()

		if step.Delay > 0 {
			delay := time.Duration(step.Delay) * time.Millisecond
			totalDelay += delay
			time.Sleep(delay)
			client.AddDelayTime(delay)
		}

		switch step.Action {
		case "open":
			content := step.Content
			if content == "" {
				content = r.test.Inputs[step.Path]
			}
			client.OpenFile(step.Path, content)

		case "edit":
			client.EditFile(step.Path, []TextEdit{
				{
					Range: Range{
						Start: Position{Line: step.Line, Character: step.Char},
						End:   Position{Line: step.Line, Character: step.Char},
					},
					NewText: step.NewText,
				},
			})

		case "hover":
			hover, err := client.Hover(step.Path, step.Line, step.Char)
			if err != nil {
				t.Errorf("Step %d: hover failed: %v", i, err)
				continue
			}
			if step.Expected.Hover != "" && !strings.Contains(hover, step.Expected.Hover) {
				t.Errorf("Step %d: hover mismatch. Expected contains: %s, got: %s", i, step.Expected.Hover, hover)
			}

		case "completion":
			items, err := client.Completion(step.Path, step.Line, step.Char)
			if err != nil {
				t.Errorf("Step %d: completion failed: %v", i, err)
				continue
			}
			if len(step.Expected.Completions) > 0 {
				found := false
				for _, item := range items {
					for _, expected := range step.Expected.Completions {
						if item.Label == expected {
							found = true
							break
						}
					}
				}
				if !found {
					t.Errorf("Step %d: expected completion not found", i)
				}
			}

		case "definition":
			defs, err := client.Definition(step.Path, step.Line, step.Char)
			if err != nil {
				t.Errorf("Step %d: definition failed: %v", i, err)
				continue
			}
			if step.Expected.Definitions > 0 && len(defs) != step.Expected.Definitions {
				t.Errorf("Step %d: expected %d definitions, got %d", i, step.Expected.Definitions, len(defs))
			}
		}

		stepDuration := time.Since(stepStart) - time.Duration(step.Delay)*time.Millisecond

		if step.Timeout > 0 && stepDuration > time.Duration(step.Timeout)*time.Millisecond {
			t.Errorf("Step %d: exceeded timeout. Expected < %dms, took %v", i, step.Timeout, stepDuration)
		}

		if step.Expected.CompletedIn > 0 && stepDuration > time.Duration(step.Expected.CompletedIn)*time.Millisecond {
			t.Errorf("Step %d: exceeded completedIn. Expected < %dms, took %v", i, step.Expected.CompletedIn, stepDuration)
		}

		if step.Expected.DiagsCount > 0 {
			diags := client.GetDiagnostics(step.Path)
			if len(diags) != step.Expected.DiagsCount {
				t.Errorf("Step %d: expected %d diagnostics, got %d", i, step.Expected.DiagsCount, len(diags))
			}
		}
	}

	testDuration := time.Since(testStart) - totalDelay

	runtime.ReadMemStats(&memAfter)
	peakMemoryKB := int64((memAfter.Alloc - memBefore.Alloc) / 1024)

	t.Logf("LSP test metrics: duration=%v, peak_memory=%dKB, steps=%d",
		testDuration, peakMemoryKB, len(steps))
}

func matchesExp(diag Diagnostic, exp ExpectedMessage) bool {
	if exp.File != "" && diag.File != exp.File {
		return false
	}
	if exp.Line > 0 && diag.Line != exp.Line {
		return false
	}
	if exp.Message != "" && !strings.Contains(diag.Message, exp.Message) {
		return false
	}
	return true
}

func matchesExpMessage(actual, expected string) bool {
	if expected == "" {
		return true
	}
	return strings.Contains(actual, expected)
}
