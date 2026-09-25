// Package loader converts external structured files (JSON, CSV) into
// parser values for `with json("…") as name` blocks.
//
// JSON mapping:
//
//	object → MapValue (keys sorted, deterministic iteration)
//	array  → ArrayValue
//	number → IntValue when integral, FloatValue otherwise
//	string → StringValue
//	bool   → BoolValue
//	null   → the key is omitted
//
// CSV mapping: the first row is the header; every following row becomes
// a MapValue of header → cell (all values are strings), and the file
// yields an ArrayValue of those rows.
package loader

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marte-community/marte-dev-tools/internal/parser"
)

// Load reads a structured file and returns its value tree.
// The path must already be resolved (absolute or relative to the
// caller's working directory).
func Load(format, path string) (parser.Value, error) {
	switch format {
	case "json":
		return loadJSON(path)
	case "csv":
		return loadCSV(path)
	default:
		return nil, fmt.Errorf("unsupported structured format %q (supported: json, csv)", format)
	}
}

// ResolvePath resolves p relative to the directory of the
// configuration file that references it, unless p is absolute.
func ResolvePath(baseFile, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(filepath.Dir(baseFile), p)
}

func loadJSON(path string) (parser.Value, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %v", path, err)
	}
	v, err := convert(doc, path)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func convert(v any, path string) (parser.Value, error) {
	switch t := v.(type) {
	case map[string]any:
		values := make(map[string]parser.Value, len(t))
		for k, e := range t {
			ev, err := convert(e, path)
			if err != nil {
				return nil, err
			}
			if ev == nil {
				continue // JSON null: omit
			}
			values[k] = ev
		}
		return parser.NewMapValue(parser.Position{}, values), nil
	case []any:
		arr := &parser.ArrayValue{}
		for _, e := range t {
			ev, err := convert(e, path)
			if err != nil {
				return nil, err
			}
			if ev == nil {
				continue
			}
			arr.Elements = append(arr.Elements, ev)
		}
		return arr, nil
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return &parser.IntValue{Value: i, Raw: t.String()}, nil
		}
		f, err := t.Float64()
		if err != nil {
			return nil, fmt.Errorf("%s: invalid number %s", path, t.String())
		}
		return &parser.FloatValue{Value: f, Raw: t.String()}, nil
	case string:
		return &parser.StringValue{Value: t, Quoted: true}, nil
	case bool:
		return &parser.BoolValue{Value: t}, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("%s: unsupported JSON value %T", path, v)
	}
}

func loadCSV(path string) (parser.Value, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: invalid CSV: %v", path, err)
	}
	if len(rows) == 0 {
		return &parser.ArrayValue{}, nil
	}
	header := rows[0]
	arr := &parser.ArrayValue{}
	for _, row := range rows[1:] {
		values := make(map[string]parser.Value, len(header))
		for i, cell := range row {
			if i < len(header) {
				values[header[i]] = &parser.StringValue{Value: cell, Quoted: true}
			}
		}
		arr.Elements = append(arr.Elements, parser.NewMapValue(parser.Position{}, values))
	}
	return arr, nil
}

// SortedKeys returns the keys of a MapValue, sorted. Loader-produced
// maps are already sorted; this is a convenience for iteration sites.
func SortedKeys(m *parser.MapValue) []string {
	keys := make([]string, len(m.Keys))
	copy(keys, m.Keys)
	sort.Strings(keys)
	return keys
}
