# E2E Test Framework

This document describes the end-to-end (E2E) test framework for `mdt`, which tests the CLI and LSP functionality using real build outputs and configuration files.

## Overview

The E2E test framework provides a comprehensive testing solution that:

- Uses the actual compiled `mdt` binary
- Tests real configuration files and build outputs
- Verifies log messages and diagnostics
- Includes a full LSP client for testing Language Server Protocol features
- Supports progressive/editable file testing

## Architecture

```
test/e2e/
├── framework/
│   ├── framework.go    # Core test infrastructure
│   ├── lspclient.go    # LSP JSON-RPC client
│   └── fixture.go      # Test fixture management
├── build_test.go       # Build command tests
├── check_test.go       # Validation tests
└── lsp_test.go        # LSP feature tests
```

### Components

#### 1. framework.go - Core Infrastructure

**TestContext** - Manages test environment:
- Creates isolated temp directories for each test
- Locates the `mdt` binary automatically
- Provides file creation and cleanup

```go
ctx := framework.NewTestContext(t)
defer ctx.Cleanup()
```

**BuildResult** - Captures build command output:
- `Output` - stdout from build
- `Stderr` - stderr/logs from build  
- `ExitCode` - command exit code
- `Diagnostics` - parsed error/warning messages

**CheckResult** - Captures validation output:
- `Stdout` - command output
- `Diagnostics` - parsed error/warning messages

#### 2. lspclient.go - LSP Testing Client

A full JSON-RPC client that communicates with the `mdt lsp` server:

| Method | Description |
|--------|-------------|
| `OpenFile(path, content)` | Opens a file in the LSP session |
| `EditFile(path, edits)` | Sends incremental changes |
| `GetDiagnostics(path)` | Returns current diagnostics |
| `Hover(path, line, char)` | Gets hover information |
| `Completion(path, line, char)` | Gets completion items |
| `Definition(path, line, char)` | Gets definition locations |
| `Symbol(path)` | Gets document symbols |
| `Rename(path, line, char, newName)` | Renames symbols |

#### 3. fixture.go - Test Fixtures

Manages test data files:

```go
loader := framework.NewFixtureLoader("test/e2e/fixtures")
fixture, _ := loader.Load("my_test_case")
loader.SaveToDir(tempDir, fixture)
```

### Assertions

The framework provides assertion helpers:

| Function | Description |
|----------|-------------|
| `AssertNoErrors(t, result)` | Fails if any diagnostics found |
| `AssertErrors(t, result, patterns...)` | Fails unless matching diagnostics found |
| `AssertOutput(t, result, substring)` | Fails unless output contains substring |
| `AssertLogContains(t, result, substring)` | Fails unless stderr contains substring |

## How to Add Tests

### Basic Test Structure

```go
package e2e

import (
    "testing"
    "github.com/marte-community/marte-dev-tools/test/e2e/framework"
)

func TestMyFeature(t *testing.T) {
    // 1. Create test context
    ctx := framework.NewTestContext(t)
    defer ctx.Cleanup()
    
    // 2. Wrap with convenience helpers
    tf := framework.WrapT(t, ctx)
    
    // 3. Create test files
    tf.CreateFile("config.marte", `
+MyConfig = {
    Class = "GAM"
    Signal = "value"
}
`)
    
    // 4. Run command
    result := tf.RunBuild("config.marte")
    
    // 5. Verify results
    if result.ExitCode != 0 {
        t.Fatalf("Build failed: %s", result.Stderr)
    }
    
    if !strings.Contains(result.Output, "MyConfig") {
        t.Fatalf("Expected output to contain MyConfig")
    }
}
```

### Testing Build Command

```go
func TestBuildCommand(t *testing.T) {
    ctx := framework.NewTestContext(t)
    defer ctx.Cleanup()
    
    tf := framework.WrapT(t, ctx)
    
    // Single file
    tf.CreateFile("config.marte", `+Test = { Class = "GAM" }`)
    result := tf.RunBuild("config.marte")
    
    // Multi-file (order matters for references)
    tf.CreateFile("base.marte", `+Base = { Class = "Type" }`)
    tf.CreateFile("derived.marte", `
#package derived
+Derived = {
    Class = "Type"
    Base = base.Base
}
`)
    result = tf.RunBuild("base.marte", "derived.marte")
    
    // Folder with -P flag
    result = tf.RunBuild("-P", tf.CreateSubdir("configs"))
    
    // Project filter with -p flag
    result = tf.RunBuild("-p", "myproject", "file1.marte", "file2.marte")
    
    // Variable override
    result = tf.RunBuild("-vNAME=override", "config.marte")
}
```

### Testing Check/Validation

```go
func TestValidation(t *testing.T) {
    ctx := framework.NewTestContext(t)
    defer ctx.Cleanup()
    
    tf := framework.WrapT(t, ctx)
    
    // Valid config - no errors
    tf.CreateFile("valid.marte", `
+Valid = {
    Class = "GAM"
}
`)
    result := tf.RunCheck("valid.marte")
    framework.AssertNoErrors(tf, result)
    
    // Invalid - expect specific error
    tf.CreateFile("invalid.marte", `
+NoClass = {
    Field = "value"
}
`)
    result = tf.RunCheck("invalid.marte")
    framework.AssertErrors(tf, result, "Class")
}
```

### Testing LSP Features

```go
func TestLSPFeatures(t *testing.T) {
    ctx := framework.NewTestContext(t)
    defer ctx.Cleanup()
    
    tf := framework.WrapT(t, ctx)
    client := tf.RunLSP()
    defer client.Close()
    
    // Open a file
    content := `
+MySignal = {
    Type = "uint32"
}
+Config = {
    Class = "GAM"
    Signal = MySignal
}
`
    path := tf.CreateFile("test.marte", content)
    client.OpenFile(path, content)
    
    // Get diagnostics
    diags := client.GetDiagnostics(path)
    // ... verify diags
    
    // Get hover info
    hover, err := client.Hover(path, 6, 12)
    
    // Get completions
    items, err := client.Completion(path, 6, 5)
    
    // Progressive edit
    client.EditFile(path, []framework.TextEdit{
        {
            Range: framework.Range{
                Start: framework.Position{Line: 1, Character: 0},
                End:   framework.Position{Line: 1, Character: 0},
            },
            NewText: "+NewObject = { Class = \"Type\" }\n\n",
        },
    })
    
    // Get updated diagnostics
    diags = client.GetDiagnostics(path)
}
```

### Using Fixtures

```go
func TestWithFixture(t *testing.T) {
    ctx := framework.NewTestContext(t)
    defer ctx.Cleanup()
    
    tf := framework.WrapT(t, ctx)
    
    // Load fixture from files
    fixture := &framework.Fixture{
        Name: "mytest",
        Files: map[string]string{
            "main.marte":    `+Main = { Class = "App" }`,
            "config.marte":  `+Config = { ... }`,
        },
    }
    
    // Save to temp dir
    for name, content := range fixture.Files {
        tf.CreateFile(name, content)
    }
    
    // Run test
    result := tf.RunBuild("main.marte")
    // ... verify
}
```

## How to Run Tests

### Run All E2E Tests

```bash
go test ./test/e2e/...
```

### Run Fixture-Based Tests

```bash
# Run all fixture tests
go test ./test/e2e/... -run TestFixtures

# Run specific fixture
go test ./test/e2e/... -run "TestFixtures/my_fixture_name"
```

### Run Specific Test Categories

```bash
# Build tests only
go test ./test/e2e/... -run "TestBuild"

# Check/validation tests only
go test ./test/e2e/... -run "TestCheck"

# LSP tests only
go test ./test/e2e/... -run "TestLSP"
```

### Run Specific Test

```bash
go test ./test/e2e/... -run TestBuildBasic -v
```

## Fixture-Based Testing (Directory Structure)

The framework supports a simpler way to add tests by creating directories with a specific structure. Developers can add tests by creating folders in `test/e2e/fixtures/`.

### Directory Structure

```
test/e2e/fixtures/
└── my_test_name/
    ├── TEST.toml              # Test configuration
    ├── inputs/                # Input files
    │   ├── config.marte
    │   └── .marte_schema.cue  # Optional schema
    └── expected/              # Expected outputs
        ├── check/
        │   └── messages.toml  # Expected check messages
        ├── build/
        │   ├── messages.toml  # Expected build messages
        │   └── out.marte      # Expected build output
        ├── format/
        │   └── config.marte  # Expected formatted output
        └── lsp/
            └── edit.toml     # LSP interaction steps
```

### TEST.toml Format

```toml
description = "Test description"
tools = ["check", "build", "format", "lsp"]  # One or more
timeout = 30  # Optional, default 30 seconds
```

### Expected Messages Format (messages.toml)

```toml
[errors]
[[errors]]
file = "config.marte"
line = 1
message = "Error message substring"

[warnings]
[[warnings]]
message = "Warning message substring"

[infos]
[[infos]]
message = "Info message substring"
```

### Expected Build Output

For build tests, put expected output files in `expected/build/`:

```toml
# expected/build/messages.toml
[errors]
[[errors]]
message = "some error"
```

Expected output files should be named matching the output filename (e.g., `out.marte`).

### Expected Format Output

For format tests, put expected formatted files in `expected/format/`:

```
expected/format/config.marte  # Expected formatted version of inputs/config.marte
```

### LSP Test Steps (edit.toml)

```toml
[[steps]]
delay = 0          # Milliseconds to wait before this step
action = "open"   # open, edit, hover, completion, definition
path = "test.marte"
line = 1
char = 1
content = "file content for open"
newText = "text to insert for edit"
expected = { 
    diagnosticsCount = 0,
    hover = "expected hover text",
    completions = ["item1", "item2"],
    definitionsCount = 1,
    symbols = ["Symbol1"]
}
```

### Example: Creating a New Fixture Test

1. Create a new directory:
```bash
mkdir -p test/e2e/fixtures/my_test/inputs test/e2e/fixtures/my_test/expected/check
```

2. Create TEST.toml:
```bash
echo 'description = "My test"
tools = ["check"]' > test/e2e/fixtures/my_test/TEST.toml
```

3. Create input files in `inputs/`:
```bash
echo '+MyConfig = { Class = "GAM" }' > test/e2e/fixtures/my_test/inputs/config.marte
```

4. Create expected messages (optional):
```toml
# expected/check/messages.toml
[errors]
[[errors]]
message = "Class"
```

5. Run the test:
```bash
go test ./test/e2e/... -run "TestFixtures/my_test"
```

### Running All Fixture Tests

```bash
go test ./test/e2e/... -run TestFixtures -v
```

### Use Custom MDT Binary

```bash
# Set custom binary path
MDT_BINARY=/path/to/custom/mdt go test ./test/e2e/...

# Or set in test code
func init() {
    framework.DefaultMDTPath = "/custom/path/mdt"
}
```

### Debug Test Output

```bash
# Verbose output
go test ./test/e2e/... -v

# With timing
go test ./test/e2e/... -v -run TestBuildBasic

# With standard test flags
go test ./test/e2e/... -v -count=1 -timeout 60s
```

## Configuration Files

### MARTe Syntax Reference

The E2E tests use real MARTe configuration syntax:

```marte
# Package declaration (namespace)
#package myproject.App

# Constants/variables
#let MY_VALUE = 123

+ObjectName = {
    Class = "GAM"           # Required - the class type
    Signal = value          # Reference to another object
    Type = "uint32"         # Signal type
}
```

### Common Test Patterns

**Testing signal linking:**
```marte
+Producer = {
    Class = "InputGAM"
    OutputSignals = {
        Signal1 = { Type = "uint32" }
    }
}

+Consumer = {
    Class = "OutputGAM"
    InputSignals = {
        Signal1 = { Type = "uint32" }  # Must match producer
    }
}
```

**Testing conditionals:**
```marte
#let ENABLE_FEATURE = true

+Config = {
    Class = "Test"
    #if ENABLE_FEATURE
    Feature = { Enabled = true }
    #endif
}
```

**Testing loops:**
```marte
+Config = {
    Signals = {
        #foreach $name in ["Signal1", "Signal2", "Signal3"]
        $name = { Type = "uint32" }
        #endforeach
    }
}
```

## Troubleshooting

### Tests Fail to Find MDT Binary

The framework automatically locates the binary by:
1. Checking `MDT_BINARY` environment variable
2. Walking up from current directory to find `go.mod`
3. Looking for `build/mdt` relative to project root

If it fails, set the path explicitly:
```bash
MDT_BINARY=/full/path/to/mdt go test ./test/e2e/...
```

### LSP Tests Hang or Timeout

The LSP client has a 30-second default timeout. If tests hang:
- Check that the LSP server starts correctly
- Verify the binary path is correct
- Check for deadlocks in the JSON-RPC handling

### Build Output Empty

The `mdt build` command outputs to both stdout and stderr. The framework captures both. If you're not seeing output:
- Check `result.Stderr` for logs
- Ensure `-o` flag is not used with direct stdout capture
- The framework uses a temp file workaround for `-o -` issues

### Diagnostics Not Parsed

The diagnostic parser expects this format:
```
[mdt] YYYY/MM/DD HH:MM:SS file:line:col: ERROR: message
file:line:col: WARNING: message
```

If your diagnostics aren't being parsed, check the actual output format with:
```go
t.Logf("Stdout: %s", result.Stdout)
t.Logf("Stderr: %s", result.Stderr)
```

## Best Practices

1. **Always defer Cleanup()** - Prevents temp directory leaks
2. **Use framework.WrapT()** - Provides convenient helper methods
3. **Test isolation** - Each test gets its own temp directory
4. **Use assertions** - `AssertNoErrors`, `AssertErrors` provide better failure messages
5. **Log for debugging** - Use `t.Logf()` for debugging test failures
6. **Check exit codes** - Always verify `result.ExitCode` for build/check commands

## Extending the Framework

### Adding New Assertions

Add to `framework.go`:

```go
func AssertExitCode(t *T, result *BuildResult, expected int) {
    if result.ExitCode != expected {
        t.Fatalf("Expected exit code %d, got %d", expected, result.ExitCode)
    }
}
```

### Adding LSP Methods

Add to `lspclient.go`:

```go
func (c *LSPTestClient) TypeDefinition(path string, line, char int) ([]Location, error) {
    // Implement textDocument/typeDefinition
}
```

## Related Documentation

- [Configuration Guide](CONFIGURATION_GUIDE.md) - MARTe configuration syntax
- [Editor Integration](EDITOR_INTEGRATION.md) - Using mdt with editors
- [Tutorial](TUTORIAL.md) - Getting started with mdt
