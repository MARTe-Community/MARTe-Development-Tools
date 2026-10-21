package refactor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// CompareBuilds checks that two built configurations are semantically
// identical: same objects and classes, same fields, same signal entries
// (including options) and the same ordered thread function lists. Field
// order and alias quoting are ignored because they are not semantic.
//
// It returns a descriptive error pointing at the first difference.
func CompareBuilds(original, refactored string) error {
	a, err := canonicalLines(original)
	if err != nil {
		return fmt.Errorf("original build: %v", err)
	}
	b, err := canonicalLines(refactored)
	if err != nil {
		return fmt.Errorf("refactored build: %v", err)
	}
	max := len(a)
	if len(b) < max {
		max = len(b)
	}
	for i := 0; i < max; i++ {
		if a[i] != b[i] {
			return fmt.Errorf("configurations differ at entry %d:\n  original:   %s\n  refactored: %s", i+1, a[i], b[i])
		}
	}
	if len(a) != len(b) {
		return fmt.Errorf("configurations differ in size: original has %d entries, refactored has %d", len(a), len(b))
	}
	return nil
}

// canonicalLines reduces a built configuration to a sorted list of semantic
// facts.
func canonicalLines(src string) ([]string, error) {
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		return nil, err
	}
	var lines []string
	var walk func(path string, defs []parser.Definition)
	walk = func(path string, defs []parser.Definition) {
		fields := map[string]string{}
		var children []*parser.ObjectNode
		for _, def := range defs {
			switch d := def.(type) {
			case *parser.Field:
				fields[d.Name] = canonicalValue(d.Value)
			case *parser.ObjectNode:
				children = append(children, d)
			}
		}
		if f, ok := lookupField(defs, "Functions"); ok {
			lines = append(lines, "FUNCS "+path+" = "+canonicalValue(f.Value))
		}
		names := make([]string, 0, len(fields))
		for k := range fields {
			if k == "Functions" {
				continue
			}
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			lines = append(lines, "FIELD "+path+" "+k+" = "+fields[k])
		}
		for _, child := range children {
			name := canonicalValue(child.Name)
			childPath := path + "/" + normNodeName(name)
			class := ""
			var sigs []string
			for _, d := range child.Subnode.Definitions {
				if f, ok := d.(*parser.Field); ok && f.Name == "Class" {
					class = canonicalValue(f.Value)
				}
				if obj, ok := d.(*parser.ObjectNode); ok {
					n := normNodeName(canonicalValue(obj.Name))
					if n == "InputSignals" || n == "OutputSignals" || n == "Signals" {
						sigs = append(sigs, canonicalSignals(childPath, n, obj, class == "IOGAM"))
					}
				}
			}
			lines = append(lines, "OBJ "+childPath+" class="+class)
			lines = append(lines, sigs...)
			walk(childPath, child.Subnode.Definitions)
		}
	}
	walk("", cfg.Definitions)
	sort.Strings(lines)
	return lines, nil
}

func lookupField(defs []parser.Definition, name string) (*parser.Field, bool) {
	for _, d := range defs {
		if f, ok := d.(*parser.Field); ok && f.Name == name {
			return f, true
		}
	}
	return nil, false
}

func normNodeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "+")
	s = strings.TrimPrefix(s, "$")
	return s
}

// canonicalSignals renders a signal container. IOGAM entry order is the copy
// order and must match; other containers are maps.
func canonicalSignals(path, container string, obj *parser.ObjectNode, ordered bool) string {
	var entries []string
	for _, d := range obj.Subnode.Definitions {
		if s, ok := d.(*parser.ObjectNode); ok {
			name := canonicalValue(s.Name)
			fields := map[string]string{}
			for _, sd := range s.Subnode.Definitions {
				if f, ok := sd.(*parser.Field); ok {
					fields[f.Name] = canonicalValue(f.Value)
				}
			}
			keys := make([]string, 0, len(fields))
			for k := range fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var b strings.Builder
			b.WriteString(name)
			for _, k := range keys {
				b.WriteString("|")
				b.WriteString(k)
				b.WriteString("=")
				b.WriteString(fields[k])
			}
			entries = append(entries, b.String())
		}
	}
	if !ordered {
		sort.Strings(entries)
	}
	return "SIG " + path + "/" + container + " = [" + strings.Join(entries, " ; ") + "]"
}

func canonicalValue(v parser.Value) string {
	switch t := v.(type) {
	case *parser.StringValue:
		return strings.TrimSpace(t.Value)
	case *parser.ReferenceValue:
		return strings.TrimSpace(t.Value)
	case *parser.IntValue:
		return strings.TrimSpace(t.Raw)
	case *parser.FloatValue:
		return strings.TrimSpace(t.Raw)
	case *parser.BoolValue:
		if t.Value {
			return "true"
		}
		return "false"
	case *parser.ArrayValue:
		parts := make([]string, 0, len(t.Elements))
		for _, e := range t.Elements {
			parts = append(parts, canonicalValue(e))
		}
		return "[ " + strings.Join(parts, " ") + " ]"
	case *parser.MapValue:
		keys := append([]string{}, t.Keys...)
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+canonicalValue(t.Values[k]))
		}
		return "{ " + strings.Join(parts, " ") + " }"
	}
	return ""
}

// VerifyRewrite builds the original and the refactored file set and checks
// that the resulting configurations are semantically identical. It is the
// safety net used by `mdt refactor`: on an error nothing should be written.
func VerifyRewrite(original, refactored []string) error {
	origBuild, err := BuildContents(original)
	if err != nil {
		return fmt.Errorf("cannot build the original files: %v", err)
	}
	refBuild, err := BuildContents(refactored)
	if err != nil {
		return fmt.Errorf("cannot build the refactored files: %v", err)
	}
	return CompareBuilds(origBuild, refBuild)
}

// BuildContents builds an in-memory set of files and returns the merged
// output. Package directives decide the logical structure, so the temporary
// paths used here do not matter.
func BuildContents(contents []string) (string, error) {
	dir, err := os.MkdirTemp("", "mdt-refactor-verify-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	paths := make([]string, 0, len(contents))
	for i, c := range contents {
		p := filepath.Join(dir, fmt.Sprintf("f%d.marte", i))
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			return "", err
		}
		paths = append(paths, p)
	}
	b := builder.NewBuilder(paths, nil)
	outPath := filepath.Join(dir, "out.marte")
	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	if err := b.Build(f); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	out, err := os.ReadFile(outPath)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
