package integration

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/logger"
)

func TestLoggerPrint(t *testing.T) {
	// Direct call for coverage
	logger.Println("Coverage check")
	logger.Info("info message")
	logger.Infof("info formatted %d", 1)
	logger.Warning("warning message")
	logger.Warningf("warning formatted %d", 2)
	logger.Error("error message")
	logger.Errorf("error formatted %d", 3)

	if os.Getenv("TEST_LOGGER_PRINT") == "1" {
		logger.Printf("Test Printf %d", 123)
		logger.Println("Test Println")
		logger.Info("Test Info")
		logger.Infof("Test Infof %d", 789)
		logger.Warning("Test Warning")
		logger.Warningf("Test Warningf %d", 1011)
		logger.Error("Test Error")
		logger.Errorf("Test Errorf %d", 1213)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestLoggerPrint")
	cmd.Env = append(os.Environ(), "TEST_LOGGER_PRINT=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	output := string(out)
	if !strings.Contains(output, "Test Printf 123") {
		t.Error("Printf output missing")
	}
	if !strings.Contains(output, "Test Println") {
		t.Error("Println output missing")
	}
	if !strings.Contains(output, "Test Info") {
		t.Error("Info output missing")
	}
	if !strings.Contains(output, "Test Infof 789") {
		t.Error("Infof output missing")
	}
	if !strings.Contains(output, "Test Warning") {
		t.Error("Warning output missing")
	}
	if !strings.Contains(output, "Test Warningf 1011") {
		t.Error("Warningf output missing")
	}
	if !strings.Contains(output, "Test Error") {
		t.Error("Error output missing")
	}
	if !strings.Contains(output, "Test Errorf 1213") {
		t.Error("Errorf output missing")
	}
}

func TestLoggerFatal(t *testing.T) {
	if os.Getenv("TEST_LOGGER_FATAL") == "1" {
		logger.Fatal("Test Fatal")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestLoggerFatal")
	cmd.Env = append(os.Environ(), "TEST_LOGGER_FATAL=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return // Success (exit code non-zero)
	}
	t.Fatalf("process ran with err %v, want exit status 1", err)
}

func TestLoggerFatalf(t *testing.T) {
	if os.Getenv("TEST_LOGGER_FATALF") == "1" {
		logger.Fatalf("Test Fatalf %d", 456)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestLoggerFatalf")
	cmd.Env = append(os.Environ(), "TEST_LOGGER_FATALF=1")
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok && !e.Success() {
		return // Success
	}
	t.Fatalf("process ran with err %v, want exit status 1", err)
}

func TestLoggerColors(t *testing.T) {
	// Backup original state
	origEnable := logger.EnableColors
	defer func() {
		logger.EnableColors = origEnable
	}()

	// Test with colors enabled
	logger.EnableColors = true
	colored := logger.Colorize("test", logger.ColorRed)
	if colored != logger.ColorRed+"test"+logger.ColorReset {
		t.Errorf("Expected red colored string, got %q", colored)
	}

	// Test with colors disabled
	logger.EnableColors = false
	plain := logger.Colorize("test", logger.ColorRed)
	if plain != "test" {
		t.Errorf("Expected plain string, got %q", plain)
	}
}
