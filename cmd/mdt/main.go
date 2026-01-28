package main

import (
	"bytes"
	"os"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/logger"
	"github.com/marte-community/marte-dev-tools/internal/lsp"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func main() {
	if len(os.Args) < 2 {
		logger.Println("Usage: mdt <command> [arguments]")
		logger.Println("Commands: lsp, build, check, fmt, init")
		logger.Println("  build [-o output_file] <input_files...>")
		logger.Println("  check <input_files...>")
		logger.Println("  fmt <input_files...>")
		logger.Println("  init <project_name>")
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "lsp":
		runLSP()
	case "build":
		runBuild(os.Args[2:])
	case "check":
		runCheck(os.Args[2:])
	case "fmt":
		runFmt(os.Args[2:])
	case "init":
		runInit(os.Args[2:])
	default:
		logger.Printf("Unknown command: %s\n", command)
		os.Exit(1)
	}
}

func runLSP() {
	lsp.RunServer()
}

func runBuild(args []string) {
	if len(args) < 1 {
		logger.Println("Usage: mdt build [-o output_file] <input_files...>")
		os.Exit(1)
	}

	var outputFilePath string
	var inputFiles []string

	for i := 0; i < len(args); i++ {
		if args[i] == "-o" {
			if i+1 < len(args) {
				outputFilePath = args[i+1]
				i++
			} else {
				logger.Println("Error: -o requires a file path")
				os.Exit(1)
			}
		} else {
			inputFiles = append(inputFiles, args[i])
		}
	}

	if len(inputFiles) < 1 {
		logger.Println("Usage: mdt build [-o output_file] <input_files...>")
		os.Exit(1)
	}

	output := os.Stdout
	if outputFilePath != "" {
		f, err := os.Create(outputFilePath)
		if err != nil {
			logger.Printf("Error creating output file %s: %v\n", outputFilePath, err)
			os.Exit(1)
		}
		defer f.Close()
		output = f
	}

	b := builder.NewBuilder(inputFiles)
	err := b.Build(output)
	if err != nil {
		logger.Printf("Build failed: %v\n", err)
		os.Exit(1)
	}
}

func runCheck(args []string) {
	if len(args) < 1 {
		logger.Println("Usage: mdt check <input_files...>")
		os.Exit(1)
	}

	tree := index.NewProjectTree()

	for _, file := range args {
		content, err := os.ReadFile(file)
		if err != nil {
			logger.Printf("Error reading %s: %v\n", file, err)
			continue
		}

		p := parser.NewParser(string(content))
		config, err := p.Parse()
		if err != nil {
			logger.Printf("%s: Grammar error: %v\n", file, err)
			continue
		}

		tree.AddFile(file, config)
	}

	v := validator.NewValidator(tree, ".")
	v.ValidateProject()

	for _, diag := range v.Diagnostics {
		level := "ERROR"
		if diag.Level == validator.LevelWarning {
			level = "WARNING"
		}
		logger.Printf("%s:%d:%d: %s: %s\n", diag.File, diag.Position.Line, diag.Position.Column, level, diag.Message)
	}

	if len(v.Diagnostics) > 0 {
		logger.Printf("\nFound %d issues.\n", len(v.Diagnostics))
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
	if err := os.MkdirAll("src", 0755); err != nil {
		logger.Fatalf("Error creating project directories: %v", err)
	}

	files := map[string]string{
		"Makefile":            "MDT=mdt\n\nall: check build\n\ncheck:\n\t$(MDT) check src/*.marte\n\nbuild:\n\t$(MDT) build -o app.marte src/*.marte\n\nfmt:\n\t$(MDT) fmt src/*.marte\n",
		".marte_schema.cue":   "package schema\n\n#Classes: {\n    // Add your project-specific classes here\n}\n",
		"src/app.marte":       "#package " + projectName + "\n\n+App = {\n  Class = RealTimeApplication\n  +Data = {\n    Class = ReferenceContainer\n  }\n  +Functions = {\n    Class = ReferenceContainer\n  }\n  +States = {\n    Class = ReferenceContainer\n  }\n  +Scheduler = {\n    Class = GAMScheduler\n    TimingDataSource = TimingDataSource\n  }\n}\n",
		"src/data.marte":      "#package " + projectName + ".App.Data\n\n// Define your DataSources here\nDefaultDataSource = DDB\n//# Default DB\n+DDB = {\n  Class=GAMDataSource\n}\n//# Timing Data Source to track threads timings\n+TimingDataSource = {\n  Class = TimingDataSource\n}",
		"src/functions.marte": "#package " + projectName + ".App.Functions\n\n// Define your GAMs here\n",
	}

	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			logger.Fatalf("Error creating file %s: %v", path, err)
		}
		logger.Printf("Created %s\n", path)
	}

	logger.Printf("Project '%s' initialized successfully.\n", projectName)
}
