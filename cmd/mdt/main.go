package main

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/logger"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

var (
	Version = "v0.1.0"
	Commit  = "none"
	Date    = "unknown"
)

const helpGeneral = `mdt — MARTe2 Developer Tools

Usage:
  mdt <command> [flags] [arguments]

Commands:
  lsp     Start the Language Server Protocol server
  build   Parse, validate, and merge .marte files into a single output
  check   Validate .marte files and report diagnostics
  fmt     Format .marte files in-place
  init    Create a new MARTe2 project scaffold
  graph   Launch the interactive signal-flow graph viewer
  version Show mdt version and build information

Run 'mdt <command> --help' for per-command usage.
`

const helpVersion = `Usage: mdt version

Show mdt version, commit hash, and build date.
`

const helpLSP = `Usage: mdt lsp [flags]

Start the LSP server (communicates via stdin/stdout).

Flags:
  --graph              Enable the interactive graph web server alongside LSP
  --graph-port=PORT    Port for the graph web server (default: random free port)
  -h, --help           Show this help message
`

const helpBuild = `Usage: mdt build [flags] [files...]

Parse, validate, and merge .marte files into a single output file.

Flags:
  -P <folder>      Scan folder recursively for .marte files
  -p <project>     Only process files belonging to this project (package prefix)
  -o <output>      Write merged output to file (default: stdout)
  -vVAR=VAL        Override a #var variable value
  -h, --help       Show this help message
`

const helpCheck = `Usage: mdt check [flags] [files...]

Validate .marte files and report diagnostics (errors and warnings).

Flags:
  -P <folder>      Scan folder recursively for .marte files
  -p <project>     Only process files belonging to this project (package prefix)
  -vVAR=VAL        Override a #var variable value
  -h, --help       Show this help message
`

const helpFmt = `Usage: mdt fmt <files...>

Format .marte files in-place.

Arguments:
  files    One or more .marte files to format

Flags:
  -h, --help    Show this help message
`

const helpInit = `Usage: mdt init <project_name>

Create a new MARTe2 project scaffold in a new directory.

Arguments:
  project_name    Name of the project (also used as directory name)

Flags:
  -h, --help    Show this help message
`

const helpGraph = `Usage: mdt graph [flags] [files...]

Launch the interactive signal-flow graph viewer, or write a static graph file.

Flags:
  -P <folder>          Scan folder recursively for .marte files
  -p <project>         Only process files belonging to this project
  -port <PORT>         Port for the interactive web server (default: random)
  -vVAR=VAL            Override a #var variable value
  -o <OUTPUT>          Write static graph to file (.dot, .svg, .html, .md)
  --state=STATE        Filter graph to a specific state (for static output)
  --state=STATE:THREAD Filter graph to a specific state and thread
  --follow=NAME        Show only the subgraph reachable from node NAME
  --simplified         Level 1: bypass IOGAM/GAMDataSource, keep signal display
  --simplified=2       Level 2: bypass + collapse signal labels to plain boxes
  -h, --help           Show this help message
`

func printHelp(cmd string) {
	switch cmd {
	case "lsp":
		fmt.Print(helpLSP)
	case "build":
		fmt.Print(helpBuild)
	case "check":
		fmt.Print(helpCheck)
	case "fmt":
		fmt.Print(helpFmt)
	case "init":
		fmt.Print(helpInit)
	case "graph":
		fmt.Print(helpGraph)
	case "version":
		fmt.Print(helpVersion)
	default:
		fmt.Print(helpGeneral)
	}
}

func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "--help" || os.Args[1] == "-h" {
		if len(os.Args) < 2 {
			fmt.Print(helpGeneral)
			os.Exit(1)
		}
		fmt.Print(helpGeneral)
		os.Exit(0)
	}

	command := os.Args[1]
	switch command {
	case "lsp":
		if hasHelpFlag(os.Args[2:]) {
			printHelp("lsp")
			os.Exit(0)
		}
		runLSP()
	case "build":
		if hasHelpFlag(os.Args[2:]) {
			printHelp("build")
			os.Exit(0)
		}
		runBuild(os.Args[2:])
	case "check":
		if hasHelpFlag(os.Args[2:]) {
			printHelp("check")
			os.Exit(0)
		}
		runCheck(os.Args[2:])
	case "fmt":
		if hasHelpFlag(os.Args[2:]) {
			printHelp("fmt")
			os.Exit(0)
		}
		runFmt(os.Args[2:])
	case "init":
		if hasHelpFlag(os.Args[2:]) {
			printHelp("init")
			os.Exit(0)
		}
		runInit(os.Args[2:])
	case "graph":
		if hasHelpFlag(os.Args[2:]) {
			printHelp("graph")
			os.Exit(0)
		}
		runGraph(os.Args[2:])
	case "version":
		runVersion()
	default:
		logger.Printf("Unknown command: %s\n", command)
		fmt.Print(helpGeneral)
		os.Exit(1)
	}
}

func runLSP() {
	args := os.Args[2:]
	graphEnabled := false
	graphPort := 0
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--graph":
			graphEnabled = true
		case strings.HasPrefix(args[i], "--graph-port="):
			fmt.Sscanf(strings.TrimPrefix(args[i], "--graph-port="), "%d", &graphPort)
		case args[i] == "--graph-port" && i+1 < len(args):
			fmt.Sscanf(args[i+1], "%d", &graphPort)
			i++
		}
	}
	if graphEnabled {
		go runGraphLSP(graphPort)
	}
	lsp.RunServer()
}

func runBuild(args []string) {
	files := []string{}
	overrides := make(map[string]string)
	outputFile := ""
	root_path := ""
	projectFilter := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-P" && i+1 < len(args) {
			root_path = args[i+1]
			i++
		} else if arg == "-p" && i+1 < len(args) {
			projectFilter = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "-v") {
			pair := arg[2:]
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) == 2 {
				overrides[parts[0]] = parts[1]
			}
		} else if arg == "-o" && i+1 < len(args) {
			outputFile = args[i+1]
			logger.SetOutput(os.Stdout)
			i++
		} else {
			files = append(files, arg)
		}
	}
	if root_path != "" {
		err := filepath.WalkDir(root_path, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".marte") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			logger.Printf("Error while exploring project dir: %v", err)
			os.Exit(1)
		}
	}

	if len(files) < 1 {
		logger.Println("Usage: mdt build [-P folder_path] [-p project_name] [-o output] [-vVAR=VAL] <input_files...>")
		os.Exit(1)
	}

	// 1. Run Validation
	tree := index.NewProjectTree()
	filteredFiles := []string{}

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			logger.Printf("Error reading %s: %v\n", file, err)
			os.Exit(1)
		}

		p := parser.NewParser(string(content))
		config, err := p.Parse()
		if err != nil {
			logger.Printf("%s: Grammar error: %v\n", file, err)
			os.Exit(1)
		}

		if projectFilter != "" {
			fileProj := ""
			if config.Package != nil {
				parts := strings.Split(config.Package.URI, ".")
				fileProj = strings.TrimSpace(parts[0])
			}
			if fileProj != projectFilter {
				continue
			}
		}

		filteredFiles = append(filteredFiles, file)
		tree.AddFile(file, config)
	}

	if len(filteredFiles) == 0 {
		if projectFilter != "" {
			logger.Printf("No files found for project '%s'\n", projectFilter)
		} else {
			logger.Println("No input files to process.")
		}
		os.Exit(0)
	}

	v := validator.NewValidator(tree, ".", overrides)
	v.ValidateProject(context.Background())

	hasErrors := false
	for _, diag := range v.Diagnostics {
		level := "ERROR"
		if diag.Level == validator.LevelWarning {
			level = "WARNING"
		} else {
			hasErrors = true
		}
		logger.Printf("%s:%d:%d: %s: %s\n", diag.File, diag.Position.Line, diag.Position.Column, level, diag.Message)
	}

	if hasErrors {
		logger.Println("Build failed due to validation errors.")
		os.Exit(1)
	}

	// 2. Perform Build
	b := builder.NewBuilder(filteredFiles, overrides)

	var out *os.File = os.Stdout
	if outputFile != "" {
		f, err := os.Create(outputFile)
		if err != nil {
			logger.Printf("Error creating output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	err := b.Build(out)
	if err != nil {
		logger.Printf("Build failed: %v\n", err)
		os.Exit(1)
	}
}

func runCheck(args []string) {
	files := []string{}
	overrides := make(map[string]string)
	root_path := ""
	projectFilter := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-P" && i+1 < len(args) {
			root_path = args[i+1]
			i++
		} else if arg == "-p" && i+1 < len(args) {
			projectFilter = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "-v") {
			pair := arg[2:]
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) == 2 {
				overrides[parts[0]] = parts[1]
			}
		} else {
			files = append(files, arg)
		}
	}

	if root_path != "" {
		err := filepath.WalkDir(root_path, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".marte") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			logger.Printf("Error while exploring project dir: %v\n", err)
			os.Exit(1)
		}
	}

	if len(files) < 1 {
		logger.Println("Usage: mdt check [-P folder_path] [-p project_name] [-vVAR=VAL] <input_files...>")
		os.Exit(1)
	}

	logger.SetOutput(os.Stdout)
	tree := index.NewProjectTree()
	syntaxErrors := 0
	foundFiles := 0

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			logger.Printf("Error reading %s: %v\n", file, err)
			continue
		}

		p := parser.NewParser(string(content))
		config, _ := p.Parse()

		if projectFilter != "" {
			fileProj := ""
			if config != nil && config.Package != nil {
				parts := strings.Split(config.Package.URI, ".")
				fileProj = strings.TrimSpace(parts[0])
			}
			if fileProj != projectFilter {
				continue
			}
		}
		foundFiles++

		if len(p.Errors()) > 0 {
			syntaxErrors += len(p.Errors())
			for _, e := range p.Errors() {
				logger.Printf("%s: Grammar error: %v\n", file, e)
			}
		}

		if config != nil {
			tree.AddFile(file, config)
		}
	}

	if foundFiles == 0 && projectFilter != "" {
		logger.Printf("No files found for project '%s'\n", projectFilter)
		return
	}

	v := validator.NewValidator(tree, ".", overrides)
	v.ValidateProject(context.Background())

	for _, diag := range v.Diagnostics {
		level := "ERROR"
		if diag.Level == validator.LevelWarning {
			level = "WARNING"
		}
		logger.Printf("%s:%d:%d: %s: %s\n", diag.File, diag.Position.Line, diag.Position.Column, level, diag.Message)
	}

	totalIssues := len(v.Diagnostics) + syntaxErrors
	if totalIssues > 0 {
		logger.Printf("\nFound %d issues.\n", totalIssues)
	} else {
		logger.Println("No issues found.")
	}
}

func runFmt(args []string) {
	if len(args) < 1 {
		logger.Println("Usage: mdt fmt <input_files...>")
		os.Exit(1)
	}

	for _, file := range args {
		content, err := os.ReadFile(file)
		if err != nil {
			logger.Printf("Error reading %s: %v\n", file, err)
			continue
		}

		p := parser.NewParser(string(content))
		config, err := p.Parse()
		if err != nil {
			logger.Printf("Error parsing %s: %v\n", file, err)
			continue
		}

		var buf bytes.Buffer
		formatter.Format(config, &buf)

		err = os.WriteFile(file, buf.Bytes(), 0644)
		if err != nil {
			logger.Printf("Error writing %s: %v\n", file, err)
			continue
		}
		logger.Printf("Formatted %s\n", file)
	}
}

func runInit(args []string) {
	if len(args) < 1 {
		logger.Println("Usage: mdt init <project_name>")
		os.Exit(1)
	}

	projectName := args[0]
	if err := os.MkdirAll(filepath.Join(projectName, "src"), 0755); err != nil {
		logger.Fatalf("Error creating project directories: %v", err)
	}

	files := map[string]string{
		"Makefile": `MDT=mdt

all: check build

check:
	$(MDT) check src/*.marte

build:
	$(MDT) build -o app.marte src/*.marte

fmt:
	$(MDT) fmt src/*.marte
`,
		".marte_schema.cue": `package schema

#Classes: {
    // Add your project-specific classes here
}
`,
		"src/app.marte": `#package App

+Main = {
    Class = RealTimeApplication
    +States = {
        Class = ReferenceContainer
        +Run = {
            Class = RealTimeState
            +MainThread = {
                Class = RealTimeThread
                Functions = {}
            }
        }
    }
    +Data = {
        Class = ReferenceContainer
    }
}
`,
		"src/components.marte": `#package App.Data

// Define your DataSources here
`,
	}

	for path, content := range files {
		fullPath := filepath.Join(projectName, path)
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			logger.Fatalf("Error creating file %s: %v", fullPath, err)
		}
		logger.Printf("Created %s\n", fullPath)
	}

	logger.Printf("Project '%s' initialized successfully.\n", projectName)
}

func runVersion() {
	fmt.Printf("mdt %s\n", Version)
	fmt.Printf("commit: %s\n", Commit)
	fmt.Printf("build date: %s\n", Date)
}
