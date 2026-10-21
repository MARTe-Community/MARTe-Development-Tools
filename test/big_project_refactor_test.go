package integration

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/builder"
	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// buildProjectDir builds every .marte file under dir (one directory level of
// subprojects, like the CLI's -P) and returns the output.
func buildProjectDir(t *testing.T, dir string) string {
	t.Helper()
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			sub, err := os.ReadDir(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			for _, f := range sub {
				if filepath.Ext(f.Name()) == ".marte" {
					files = append(files, filepath.Join(dir, e.Name(), f.Name()))
				}
			}
			continue
		}
		if filepath.Ext(e.Name()) == ".marte" {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	b := builder.NewBuilder(files, nil)
	out := filepath.Join(t.TempDir(), "out.marte")
	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := b.Build(f); err != nil {
		f.Close()
		t.Fatalf("build %s: %v", dir, err)
	}
	f.Close()
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(content)
}

// canonicalConfig reduces a built configuration to its semantics: object
// classes, fields, signal entries (as maps, since field order is not
// semantic) and thread function order. Quoting style and definition order
// inside a container are ignored.
func canonicalConfig(t *testing.T, src string) []string {
	t.Helper()
	cfg, err := parser.NewParser(src).Parse()
	if err != nil {
		t.Fatalf("parse output: %v", err)
	}
	var lines []string

	var walk func(path string, defs []parser.Definition)
	walk = func(path string, defs []parser.Definition) {
		fields := map[string]string{}
		children := []*parser.ObjectNode{}
		for _, def := range defs {
			switch d := def.(type) {
			case *parser.Field:
				fields[d.Name] = canonicalValue(d.Value)
			case *parser.ObjectNode:
				children = append(children, d)
			}
		}
		// Function lists keep their order: it is the execution sequence.
		if fv, ok := fields["Functions"]; ok {
			_ = fv
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
			childPath := path + "/" + normName(name)
			class := ""
			var sigs []string
			for _, d := range child.Subnode.Definitions {
				if f, ok := d.(*parser.Field); ok && f.Name == "Class" {
					class = canonicalValue(f.Value)
				}
				if obj, ok := d.(*parser.ObjectNode); ok {
					n := normName(canonicalValue(obj.Name))
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
	return lines
}

func lookupField(defs []parser.Definition, name string) (*parser.Field, bool) {
	for _, d := range defs {
		if f, ok := d.(*parser.Field); ok && f.Name == name {
			return f, true
		}
	}
	return nil, false
}

func normName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "+")
	s = strings.TrimPrefix(s, "$")
	return s
}

// canonicalSignals renders a signal container; entry order is kept for
// IOGAM inputs/outputs (copies are positional) and ignored otherwise.
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
	// IOGAM entry order is the copy order and must match; other containers
	// are maps.
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

// The refactored big project (copy groups instead of IOGAMs, with
// per-signal options) must be semantically identical to the original: same
// objects and classes, same signals and fields, same thread function
// sequences. Definition order and field/alias ordering are not semantic.
func TestBigProjectRefactorEquivalence(t *testing.T) {
	orig := buildProjectDir(t, "../examples/big_project")
	refac := buildProjectDir(t, "../examples/big_project_sugar")
	origLines := canonicalConfig(t, orig)
	refLines := canonicalConfig(t, refac)
	if strings.Join(origLines, "\n") == strings.Join(refLines, "\n") {
		return
	}
	// Report the first difference.
	max := len(origLines)
	if len(refLines) < max {
		max = len(refLines)
	}
	for i := 0; i < max; i++ {
		if origLines[i] != refLines[i] {
			t.Fatalf("configurations differ at line %d:\n original: %s\nrefactored: %s", i+1, origLines[i], refLines[i])
		}
	}
	if len(origLines) != len(refLines) {
		t.Fatalf("configurations differ in size: original %d entries, refactored %d entries", len(origLines), len(refLines))
	}
}
