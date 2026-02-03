package main

import (
	"bytes"
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

func main() {
	if len(os.Args) < 2 {
		logger.Println("Usage: mdt <command> [arguments]")
		logger.Println("Commands: lsp, build, check, fmt, init")
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
	files := []string{}
	overrides := make(map[string]string)
	outputFile := ""
	root_path := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-P" && i+1 < len(args) {
			root_path = args[i+1]
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
				logger.Printf("append: %s\n", path)
			}
			return nil
		})
		if err != nil {
			logger.Printf("Error while exploring project dir: %v", err)
			os.Exit(1)
		}
	} else if len(files) < 1 {
		logger.Println("Usage: mdt build [-o output] [-vVAR=VAL] <input_files...>")
		os.Exit(1)
	}

	// 1. Run Validation
	tree := index.NewProjectTree()
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

		tree.AddFile(file, config)
	}

	v := validator.NewValidator(tree, ".", overrides)
	v.ValidateProject()

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
	b := builder.NewBuilder(files, overrides)

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

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-P" && i+1 < len(args) {
			root_path = args[i+1]
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
		logger.Println("Usage: mdt check [-P folder_path] [-vVAR=VAL] <input_files...>")
		os.Exit(1)
	}

	logger.SetOutput(os.Stdout)
	tree := index.NewProjectTree()
	syntaxErrors := 0

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			logger.Printf("Error reading %s: %v\n", file, err)
			continue
		}

		p := parser.NewParser(string(content))
		config, _ := p.Parse()
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

	v := validator.NewValidator(tree, ".", overrides)
	v.ValidateProject()

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
