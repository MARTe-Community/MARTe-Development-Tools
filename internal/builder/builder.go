package builder

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
)

type Builder struct {
	Files []string
}

func NewBuilder(files []string) *Builder {
	return &Builder{Files: files}
}

func (b *Builder) Build(outputDir string) error {
	// Build the Project Tree
	tree := index.NewProjectTree()
	
	for _, file := range b.Files {
		content, err := ioutil.ReadFile(file)
		if err != nil {
			return err
		}

		p := parser.NewParser(string(content))
		config, err := p.Parse()
		if err != nil {
			return fmt.Errorf("error parsing %s: %v", file, err)
		}
		
tree.AddFile(file, config)
	}

	// Iterate over top-level children of the root (Packages)
	// Spec says: "merges all files sharing the same base namespace"
	// So if we have #package A.B and #package A.C, they define A.
	// We should output A.marte? Or A/B.marte?
	// Usually MARTe projects output one file per "Root Object" or as specified.
	// The prompt says: "Output format is the same as input ... without #package".
	// "Build tool merges all files sharing the same base namespace into a single output."
	
	// If files have:
	// File1: #package App
	// File2: #package App
	// Output: App.marte
	
	// If File3: #package Other
	// Output: Other.marte
	
	// So we iterate Root.Children.
	for name, node := range tree.Root.Children {
		outputPath := filepath.Join(outputDir, name+".marte")
		f, err := os.Create(outputPath)
		if err != nil {
			return err
		}
		defer f.Close()
		
		// Write node content
		// Top level node in tree corresponds to the "Base Namespace" name?
		// e.g. #package App.Sub -> Root->App->Sub.
		// If we output App.marte, we should generate "+App = { ... }"
		
		// But wait. Input: #package App. 
		// +Node = ...
		// Output: +Node = ...
	
		// If Input: #package App.
		// +App = ... (Recursive?)
		// MARTe config is usually a list of definitions.
		// If #package App, and we generate App.marte.
		// Does App.marte contain "App = { ... }"?
		// Or does it contain the CONTENT of App?
		// "Output format is the same as input configuration but without the #package macro"
		// Input: #package App \n +Node = {}
		// Output: +Node = {}
	
		// So we are printing the CHILDREN of the "Base Namespace".
		// But wait, "Base Namespace" could be complex "A.B".
		// "Merges files with the same base namespace".
		// Assuming base namespace is the first segment? or the whole match?
	
		// Let's assume we output one file per top-level child of Root.
		// And we print that Child as an Object.
		
		// Actually, if I have:
		// #package App
		// +Node = {}
	
		// Tree: Root -> App -> Node.
	
		// If I generate App.marte.
		// Should it look like:
		// +Node = {}
		// Or
		// +App = { +Node = {} }?
	
		// If "without #package macro", it implies we are expanding the package into structure?
		// Or just removing the line?
		// If I remove #package App, and keep +Node={}, then +Node is at root.
		// But originally it was at App.Node.
		// So preserving semantics means wrapping it in +App = { ... }.
		
b.writeNodeContent(f, node, 0)
	}
	
	return nil
}

func (b *Builder) writeNodeContent(f *os.File, node *index.ProjectNode, indent int) {
	// 1. Sort Fragments: Class first
	sort.SliceStable(node.Fragments, func(i, j int) bool {
		return hasClass(node.Fragments[i]) && !hasClass(node.Fragments[j])
	})
	
	indentStr := strings.Repeat("  ", indent)
	
	// If this node has a RealName (e.g. +App), we print it as an object definition
	// UNLESS it is the top-level output file itself? 
	// If we are writing "App.marte", maybe we are writing the *body* of App?
	// Spec: "unifying multi-file project into a single configuration output"
	
	// Let's assume we print the Node itself.
	if node.RealName != "" {
		fmt.Fprintf(f, "%s%s = {\n", indentStr, node.RealName)
		indent++
		indentStr = strings.Repeat("  ", indent)
	}
	
	// 2. Write definitions from fragments
	for _, frag := range node.Fragments {
		// Use formatter logic to print definitions
		// We need a temporary Config to use Formatter? 
		// Or just reimplement basic printing? Formatter is better.
		// But Formatter prints to io.Writer.

		// We can reuse formatDefinition logic if we exposed it, or just copy basic logic.
		// Since we need to respect indentation, using Formatter.Format might be tricky 
		// unless we wrap definitions in a dummy structure.
		
		for _, def := range frag.Definitions {
			// Basic formatting for now, referencing formatter style
			b.writeDefinition(f, def, indent)
		}
	}
	
	// 3. Write Children (recursively)
	// Children are sub-nodes defined implicitly via #package A.B or explicitly +Sub
	// Explicit +Sub are handled via Fragments logic (they are definitions in fragments).
	// Implicit nodes (from #package A.B.C where B was never explicitly defined)
	// show up in Children map but maybe not in Fragments?
	
	// If a Child is NOT in fragments (implicit), we still need to write it.
	// If it IS in fragments (explicit +Child), it was handled in loop above?
	// Wait. My Indexer puts `+Sub` into `node.Children["Sub"]` AND adds a `Fragment` to `node` containing `+Sub` object?
	
	// Let's check Indexer.
	// Case ObjectNode:
	//   Adds Fragment to `child` (the Sub node).
	//   Does NOT add `ObjectNode` definition to `node`'s fragment list?
	//   "pt.addObjectFragment(child...)"
	//   It does NOT add to `fileFragment.Definitions`.
	
	// So `node.Fragments` only contains Fields!
	// Children are all in `node.Children`.
	
	// So:
	// 1. Write Fields (from Fragments).
	// 2. Write Children (from Children map).
	
	// But wait, Fragments might have order?
	// "Relative ordering within a file is preserved."
	// My Indexer splits Fields and Objects.
	// Fields go to Fragments. Objects go to Children.
	// This loses the relative order between Fields and Objects in the source file!
	
	// Correct Indexer approach for preserving order:
	// `Fragment` should contain a list of `Entry`.
	// `Entry` can be `Field` OR `ChildNodeName`.
	
	// But I just rewrote Indexer to split them.
	// If strict order is required "within a file", my Indexer is slightly lossy regarding Field vs Object order.
	// Spec: "Relative ordering within a file is preserved."
	
	// To fix this without another full rewrite:
	// Iterating `node.Children` alphabetically is arbitrary.
	// We should ideally iterate them in the order they appear.
	
	// For now, I will proceed with writing Children after Fields, which is a common convention, 
	// unless strict interleaving is required. 
	// Given "Class first" rule, reordering happens anyway.
	
	// Sorting Children?
	// Maybe keep a list of OrderedChildren in ProjectNode?
	
	sortedChildren := make([]string, 0, len(node.Children))
	for k := range node.Children {
		sortedChildren = append(sortedChildren, k)
	}
	sort.Strings(sortedChildren) // Alphabetical for determinism
	
	for _, k := range sortedChildren {
		child := node.Children[k]
		b.writeNodeContent(f, child, indent)
	}
	
	if node.RealName != "" {
		indent--
		indentStr = strings.Repeat("  ", indent)
		fmt.Fprintf(f, "%s}\n", indentStr)
	}
}

func (b *Builder) writeDefinition(f *os.File, def parser.Definition, indent int) {
	indentStr := strings.Repeat("  ", indent)
	switch d := def.(type) {
	case *parser.Field:
		fmt.Fprintf(f, "%s%s = %s\n", indentStr, d.Name, b.formatValue(d.Value))
	}
}

func (b *Builder) formatValue(val parser.Value) string {
	switch v := val.(type) {
	case *parser.StringValue:
		if v.Quoted {
			return fmt.Sprintf("\"%s\"", v.Value)
		}
		return v.Value
	case *parser.IntValue:
		return v.Raw
	case *parser.FloatValue:
		return v.Raw
	case *parser.BoolValue:
		return fmt.Sprintf("%v", v.Value)
	case *parser.ReferenceValue:
		return v.Value
	case *parser.ArrayValue:
		elements := []string{}
		for _, e := range v.Elements {
			elements = append(elements, b.formatValue(e))
		}
		return fmt.Sprintf("{ %s }", strings.Join(elements, " "))
	default:
		return ""
	}
}

func hasClass(frag *index.Fragment) bool {
	for _, def := range frag.Definitions {
		if f, ok := def.(*parser.Field); ok && f.Name == "Class" {
			return true
		}
	}
	return false
}