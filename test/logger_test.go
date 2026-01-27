package integration

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/logger"
)

func TestLoggerPrint(t *testing.T) {
	if os.Getenv("TEST_LOGGER_PRINT") == "1" {
		logger.Printf("Test Printf %d", 123)
		logger.Println("Test Println")
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
