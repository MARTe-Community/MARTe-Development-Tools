package builder

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/marte-dev/marte-dev-tools/internal/parser"
)

type Builder struct {
	Files []string
}

func NewBuilder(files []string) *Builder {
	return &Builder{Files: files}
}

func (b *Builder) Build(outputDir string) error {
	packages := make(map[string]*parser.Configuration)

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

		pkgURI := ""
		if config.Package != nil {
			pkgURI = config.Package.URI
		}

		if existing, ok := packages[pkgURI]; ok {
			existing.Definitions = append(existing.Definitions, config.Definitions...)
		} else {
			packages[pkgURI] = config
		}
	}

	for pkg, config := range packages {
		if pkg == "" {
			continue // Or handle global package
		}
		
		outputPath := filepath.Join(outputDir, pkg+".marte")
		err := b.writeConfig(outputPath, config)
		if err != nil {
			return err
		}
	}

	return nil
}

func (b *Builder) writeConfig(path string, config *parser.Configuration) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, def := range config.Definitions {
		b.writeDefinition(f, def, 0)
	}
	return nil
}

func (b *Builder) writeDefinition(f *os.File, def parser.Definition, indent int) {
	indentStr := strings.Repeat("    ", indent)
	switch d := def.(type) {
	case *parser.Field:
		fmt.Fprintf(f, "%s%s = %s\n", indentStr, d.Name, b.formatValue(d.Value))
	case *parser.ObjectNode:
		fmt.Fprintf(f, "%s%s = {\n", indentStr, d.Name)
		for _, subDef := range d.Subnode.Definitions {
			b.writeDefinition(f, subDef, indent+1)
		}
		fmt.Fprintf(f, "%s}\n", indentStr)
	}
}

func (b *Builder) formatValue(val parser.Value) string {
	switch v := val.(type) {
	case *parser.StringValue:
		return fmt.Sprintf("\"%s\"", v.Value)
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
		return fmt.Sprintf("{%s}", strings.Join(elements, " "))
	default:
		return ""
	}
}
