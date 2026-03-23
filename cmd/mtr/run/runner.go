package run

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const runnerVersion = "v0.1.0"

type Runner struct {
	db *DB
}

func NewRunner() *Runner {
	db, err := NewDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to initialize database: %v\n", err)
	}
	return &Runner{db: db}
}

func (r *Runner) RunTests(paths string) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}

	fmt.Printf("Running tests from: %s\n", paths)
	fmt.Println("Note: This requires the test framework to be enhanced with result callbacks.")
	fmt.Println("Currently, this is a placeholder that runs go test and captures results.")
	fmt.Println()

	testPaths := strings.Split(paths, ",")

	var totalPassed int
	var totalFailed int

	for _, path := range testPaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		fmt.Printf("Running: go test -v %s\n", path)

		var memBefore, memAfter runtime.MemStats
		runtime.ReadMemStats(&memBefore)

		start := time.Now()

		cmd := exec.Command("go", "test", "-v", path)
		cmd.Dir = getProjectRoot()

		output, err := cmd.CombinedOutput()

		testDuration := time.Since(start)
		runtime.ReadMemStats(&memAfter)

		peakMemoryKB := int64((memAfter.Alloc - memBefore.Alloc) / 1024)

		testName := extractTestName(path)
		testVersion := "v1"

		passed := err == nil && !strings.Contains(string(output), "FAIL")

		result := &TestResult{
			TestFile:     path,
			TestName:     testName,
			TestVersion:  testVersion,
			Passed:       passed,
			DurationMs:   testDuration.Milliseconds(),
			PeakMemoryKB: peakMemoryKB,
			RunnerVer:    runnerVersion,
		}

		if err != nil {
			result.ErrorMsg = err.Error()
		}

		if !passed {
			result.ErrorDiff = string(output)
		}

		if err := r.db.Insert(result); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to store result: %v\n", err)
		}

		if passed {
			totalPassed++
			fmt.Printf("PASS: %s (%.2fs)\n", testName, testDuration.Seconds())
		} else {
			totalFailed++
			fmt.Printf("FAIL: %s\n  Error: %v\n", testName, err)
		}
	}

	fmt.Printf("\nResults: %d passed, %d failed\n", totalPassed, totalFailed)

	if r.db != nil {
		r.db.Close()
	}

	return nil
}

func extractTestName(path string) string {
	parts := strings.Split(path, "/")
	name := parts[len(parts)-1]
	name = strings.TrimSuffix(name, ".go")
	name = strings.TrimSuffix(name, "_test")
	return name
}

func getProjectRoot() string {
	return "/home/martino/Projects/marte_dev_tools"
}
