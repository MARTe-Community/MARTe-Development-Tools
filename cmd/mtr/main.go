package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/marte-community/marte-dev-tools/cmd/mtr/run"
)

var (
	version = "v0.1.0"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runCmd := flag.NewFlagSet("run", flag.ExitOnError)
		paths := runCmd.String("paths", "", "Comma-separated test paths to run")
		runCmd.Parse(os.Args[2:])
		if *paths == "" {
			fmt.Println("Error: --paths flag is required")
			runCmd.Usage()
			os.Exit(1)
		}
		runner := run.NewRunner()
		if err := runner.RunTests(*paths); err != nil {
			fmt.Fprintf(os.Stderr, "Error running tests: %v\n", err)
			os.Exit(1)
		}

	case "report":
		reportCmd := flag.NewFlagSet("report", flag.ExitOnError)
		testName := reportCmd.String("test", "", "Filter by test name")
		since := reportCmd.String("since", "", "Show results since date (YYYY-MM-DD)")
		reportCmd.Parse(os.Args[2:])
		reporter := run.NewReporter()
		if err := reporter.Report(*testName, *since); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating report: %v\n", err)
			os.Exit(1)
		}

	case "stats":
		reporter := run.NewReporter()
		if err := reporter.Stats(); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating stats: %v\n", err)
			os.Exit(1)
		}

	case "version":
		fmt.Printf("mtr %s\n", version)

	case "help":
		printUsage()

	default:
		fmt.Printf("Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("mtr - MARTe Test Runner")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  mtr <command> [flags]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  run       Run tests and store results")
	fmt.Println("  report    Show test results with optional filters")
	fmt.Println("  stats     Show overall pass/fail statistics")
	fmt.Println("  version   Show mtr version")
	fmt.Println("  help      Show this help message")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  mtr run --paths ./test/e2e/...")
	fmt.Println("  mtr report --test TestLSPHover")
	fmt.Println("  mtr report --since 2024-01-01")
	fmt.Println("  mtr stats")
}
