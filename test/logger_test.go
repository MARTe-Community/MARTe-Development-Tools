package integration

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/logger"
)

// TestDebugOutputIsOptIn covers the LSP logging policy: editors label every
// stderr line as an error, so routine progress must stay silent unless debug
// logging is explicitly enabled.
func TestDebugOutputIsOptIn(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stderr)

	logger.SetVerbose(false)
	defer logger.SetVerbose(false)

	logger.Debugf("progress %d", 1)
	logger.Debug("progress", 2)
	if got := buf.String(); got != "" {
		t.Errorf("debug output written while disabled: %q", got)
	}

	logger.SetVerbose(true)
	if !logger.Verbose() {
		t.Error("Verbose() should report true after SetVerbose(true)")
	}
	logger.Debugf("progress %d", 1)
	logger.Debug("progress", 2)
	got := buf.String()
	if !strings.Contains(got, "progress 1") || !strings.Contains(got, "progress 2") {
		t.Errorf("debug output missing: %q", got)
	}

	// Real errors keep flowing regardless of the debug flag.
	buf.Reset()
	logger.SetVerbose(false)
	logger.Errorf("boom: %s", "detail")
	logger.Error("boom")
	if got := buf.String(); !strings.Contains(got, "boom") {
		t.Errorf("error output suppressed: %q", got)
	}
	_ = logger.Verbose()
}
