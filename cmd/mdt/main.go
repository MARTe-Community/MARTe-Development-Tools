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
		logger.Println("Commands: lsp, build, check, fmt")
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
		logger.Println("Usage: mdt build <input_files...>")
		os.Exit(1)
	}

	b := builder.NewBuilder(args)
	err := b.Build(os.Stdout)
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
	// configs := make(map[string]*parser.Configuration) // We don't strictly need this map if we just build the tree

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

	// idx.ResolveReferences() // Not implemented in new tree yet, but Validator uses Tree directly
	v := validator.NewValidator(tree, ".")
	v.ValidateProject()

	// Legacy loop removed as ValidateProject covers it via recursion

	v.CheckUnused()

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
