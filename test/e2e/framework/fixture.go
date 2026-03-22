package framework

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Fixture struct {
	Name      string
	Files     map[string]string
	BuildArgs []string
}

type FixtureLoader struct {
	baseDir string
}

func NewFixtureLoader(baseDir string) *FixtureLoader {
	return &FixtureLoader{baseDir: baseDir}
}

func (fl *FixtureLoader) Load(name string) (*Fixture, error) {
	fixtureDir := filepath.Join(fl.baseDir, name)

	files := make(map[string]string)

	err := filepath.Walk(fixtureDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(fixtureDir, path)
		if err != nil {
			return err
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		files[relPath] = string(content)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to load fixture %s: %w", name, err)
	}

	return &Fixture{
		Name:  name,
		Files: files,
	}, nil
}

func (fl *FixtureLoader) SaveToDir(dir string, fixture *Fixture) error {
	for name, content := range fixture.Files {
		path := filepath.Join(dir, name)
		parent := filepath.Dir(path)

		if err := os.MkdirAll(parent, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", parent, err)
		}

		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write file %s: %w", path, err)
		}
	}

	return nil
}

type ExpectedOutput struct {
	Content     string
	Contains    []string
	NotContains []string
	Matchers    []OutputMatcher
}

type OutputMatcher func(output string) error

func MatchContains(substr string) OutputMatcher {
	return func(output string) error {
		if !strings.Contains(output, substr) {
			return fmt.Errorf("expected output to contain %q, got: %s", substr, output)
		}
		return nil
	}
}

func MatchNotContains(substr string) OutputMatcher {
	return func(output string) error {
		if strings.Contains(output, substr) {
			return fmt.Errorf("expected output to NOT contain %q, got: %s", substr, output)
		}
		return nil
	}
}

func MatchRegex(pattern string) OutputMatcher {
	return func(output string) error {
		if !strings.Contains(output, pattern) {
			return fmt.Errorf("expected output to match %q, got: %s", pattern, output)
		}
		return nil
	}
}

func (e *ExpectedOutput) Verify(output string) error {
	if e.Content != "" && e.Content != output {
		return fmt.Errorf("expected output:\n%s\n\ngot:\n%s", e.Content, output)
	}

	for _, substr := range e.Contains {
		if !strings.Contains(output, substr) {
			return fmt.Errorf("expected output to contain %q, got: %s", substr, output)
		}
	}

	for _, substr := range e.NotContains {
		if strings.Contains(output, substr) {
			return fmt.Errorf("expected output to NOT contain %q, got: %s", substr, output)
		}
	}

	for _, matcher := range e.Matchers {
		if err := matcher(output); err != nil {
			return err
		}
	}

	return nil
}

type ExpectedLog struct {
	Contains    []string
	NotContains []string
	Matchers    []OutputMatcher
}

func (e *ExpectedLog) Verify(log string) error {
	for _, substr := range e.Contains {
		if !strings.Contains(log, substr) {
			return fmt.Errorf("expected log to contain %q, got: %s", substr, log)
		}
	}

	for _, substr := range e.NotContains {
		if strings.Contains(log, substr) {
			return fmt.Errorf("expected log to NOT contain %q, got: %s", substr, log)
		}
	}

	for _, matcher := range e.Matchers {
		if err := matcher(log); err != nil {
			return err
		}
	}

	return nil
}

type ExpectedDiagnostics struct {
	Count    int
	File     string
	Contains []string
	Matchers []DiagnosticMatcher
}

type DiagnosticMatcher func(diags []Diagnostic) error

func MatchDiagnosticCount(count int) DiagnosticMatcher {
	return func(diags []Diagnostic) error {
		if len(diags) != count {
			return fmt.Errorf("expected %d diagnostics, got %d", count, len(diags))
		}
		return nil
	}
}

func MatchDiagnosticMessageContains(msg string) DiagnosticMatcher {
	return func(diags []Diagnostic) error {
		for _, d := range diags {
			if strings.Contains(d.Message, msg) {
				return nil
			}
		}
		return fmt.Errorf("expected diagnostic containing %q, got: %v", msg, diags)
	}
}

func MatchDiagnosticSeverity(sev string) DiagnosticMatcher {
	return func(diags []Diagnostic) error {
		for _, d := range diags {
			if d.Severity == sev {
				return nil
			}
		}
		return fmt.Errorf("expected diagnostic with severity %q, got: %v", sev, diags)
	}
}

func (e *ExpectedDiagnostics) Verify(diags []Diagnostic) error {
	if e.Count > 0 && len(diags) != e.Count {
		return fmt.Errorf("expected %d diagnostics, got %d", e.Count, len(diags))
	}

	for _, matcher := range e.Matchers {
		if err := matcher(diags); err != nil {
			return err
		}
	}

	return nil
}

func VerifyBuildResult(t T, result *BuildResult, expectedOutput *ExpectedOutput, expectedLog *ExpectedLog, expectedDiags *ExpectedDiagnostics) {
	if expectedOutput != nil {
		if err := expectedOutput.Verify(result.Output); err != nil {
			t.Fatalf("Output verification failed: %v", err)
		}
	}

	if expectedLog != nil {
		if err := expectedLog.Verify(result.Stderr); err != nil {
			t.Fatalf("Log verification failed: %v", err)
		}
	}

	if expectedDiags != nil {
		if err := expectedDiags.Verify(result.Diagnostics); err != nil {
			t.Fatalf("Diagnostics verification failed: %v", err)
		}
	}
}
