package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectFilter(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "proj_filter")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// File 1: Project A
	file1 := filepath.Join(tmpDir, "a.marte")
	content1 := "#package ProjectA\n+ObjA = { Class = MyClass }"
	os.WriteFile(file1, []byte(content1), 0644)

	// File 2: Project B
	file2 := filepath.Join(tmpDir, "b.marte")
	content2 := "#package ProjectB\n+ObjB = { Class = MyClass }"
	os.WriteFile(file2, []byte(content2), 0644)

	// Build mdt
	binPath := filepath.Join(tmpDir, "mdt")
	buildCmd := exec.Command("go", "build", "-o", binPath, "../cmd/mdt/main.go")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build mdt: %v\n%s", err, string(out))
	}

	// 1. Test build with filter ProjectA
	cmd := exec.Command(binPath, "build", "-p", "ProjectA", file1, file2)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Build failed: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "ObjA") {
		t.Error("Expected ObjA in output")
	}
	if strings.Contains(string(out), "ObjB") {
		t.Error("Did not expect ObjB in output")
	}

	// 2. Test check with filter ProjectB
	cmd = exec.Command(binPath, "check", "-p", "ProjectB", file1, file2)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Check failed: %v\n%s", err, string(out))
	}
	// Since both are valid, it should say "No issues found"
	if !strings.Contains(string(out), "No issues found") {
		t.Errorf("Expected 'No issues found', got: %s", string(out))
	}

	// 3. Test build with filter ProjectC (none)
	cmd = exec.Command(binPath, "build", "-p", "ProjectC", file1, file2)
	out, err = cmd.CombinedOutput()
	if err != nil {
		// It might exit with 0 or error depending on implementation. 
        // My implementation says "No files found for project 'ProjectC'" and os.Exit(0)
	}
	if !strings.Contains(string(out), "No files found for project 'ProjectC'") {
		t.Errorf("Expected 'No files found' message, got: %s", string(out))
	}
}
