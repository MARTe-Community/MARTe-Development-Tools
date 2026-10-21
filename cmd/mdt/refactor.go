package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/formatter"
	"github.com/marte-community/marte-dev-tools/internal/logger"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/refactor"
)

// runRefactor rewrites configurations to the current sugar syntax:
// IOGAM objects become copy groups and signal declarations become compact
// shorthand forms.
func runRefactor(args []string) {
	files := []string{}
	rootPath := ""
	dryRun := false
	checkOnly := false
	verify := true

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-P" && i+1 < len(args):
			rootPath = args[i+1]
			i++
		case arg == "--dry-run" || arg == "-n":
			dryRun = true
		case arg == "--check":
			checkOnly = true
		case arg == "--verify":
			verify = true
		case arg == "--no-verify":
			verify = false
		case strings.HasPrefix(arg, "-"):
			logger.Printf("Unknown flag %s\n", arg)
			os.Exit(1)
		default:
			files = append(files, arg)
		}
	}

	if rootPath != "" {
		err := filepath.WalkDir(rootPath, func(path string, d os.DirEntry, err error) error {
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
	files = expandDirectoryInputs(files)
	if len(files) == 0 {
		logger.Println("Usage: mdt refactor [-P folder_path] [--check] [--dry-run] <files...>")
		os.Exit(1)
	}

	// Parse everything first: DataSource signal types are needed to decide
	// how to group unequal IOGAM signal lists.
	configs := make([]*parser.Configuration, 0, len(files))
	sources := make([]string, 0, len(files))
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			logger.Printf("Error reading %s: %v\n", file, err)
			os.Exit(1)
		}
		cfg, err := parser.NewParser(string(content)).Parse()
		if err != nil {
			logger.Printf("Error parsing %s: %v\n", file, err)
			os.Exit(1)
		}
		configs = append(configs, cfg)
		sources = append(sources, string(content))
	}
	types := refactor.CollectTypes(configs)

	outputs := make([]string, len(configs))
	changed := make([]bool, len(configs))
	changedFiles := 0
	for i, cfg := range configs {
		res := refactor.Config(cfg, types, refactor.Options{
			CompactSignals: true,
			CopyGroups:     true,
			Pragmas:        refactor.SourcePragmas(sources[i]),
		})
		for _, w := range res.Warnings {
			logger.Printf("%s: %s\n", files[i], w)
		}

		var buf bytes.Buffer
		formatter.Format(cfg, &buf)
		outputs[i] = buf.String()
		if outputs[i] == sources[i] && res.Signals == 0 && res.CopyGroups == 0 {
			continue
		}
		if res.Signals > 0 || res.CopyGroups > 0 {
			logger.Printf("%s: %d signal declaration(s), %d IOGAM(s)\n", files[i], res.Signals, res.CopyGroups)
		}
		changed[i] = true
		changedFiles++
	}

	// Verify that the rewrite does not change the built configuration before
	// touching any file: on a mismatch nothing is written, so there is
	// nothing to revert.
	if verify && changedFiles > 0 {
		if err := refactor.VerifyRewrite(sources, outputs); err != nil {
			logger.Printf("Verification failed: %v\n", err)
			logger.Println("No files were modified (refactor reverted).")
			os.Exit(1)
		}
		logger.Println("Verification: the refactored configuration builds identically.")
	}

	for i := range configs {
		if !changed[i] {
			continue
		}
		switch {
		case checkOnly:
			// counted already
		case dryRun:
			fmt.Printf("// ==== %s ====\n%s\n", files[i], outputs[i])
		default:
			// Write through a temporary file so a crash cannot leave a
			// half-written configuration behind.
			tmp := files[i] + ".refactor.tmp"
			if err := os.WriteFile(tmp, []byte(outputs[i]), 0o644); err != nil {
				logger.Printf("Error writing %s: %v\n", files[i], err)
				os.Exit(1)
			}
			if err := os.Rename(tmp, files[i]); err != nil {
				os.Remove(tmp)
				logger.Printf("Error writing %s: %v\n", files[i], err)
				os.Exit(1)
			}
		}
	}

	if checkOnly {
		if changedFiles > 0 {
			logger.Printf("%d file(s) need refactoring.\n", changedFiles)
			os.Exit(1)
		}
		logger.Println("All files are refactored.")
	}
}
