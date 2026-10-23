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
	"strings"
	"sync"

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
	case "xlsx", "xls", "excel":
		return loadXLSX(path)
	default:
		return nil, fmt.Errorf("unsupported structured format %q (supported: json, csv, xlsx)", format)
	}
}

// projectMarker names the directory that acts as the project home. Its
// presence (no content is read) anchors relative document paths.
const projectMarker = "marte_project.json"

var homeCache sync.Map // "<dir>\x00<fallback>" → home

// ProjectHome returns the directory that relative document paths resolve
// against: the nearest ancestor of baseFile containing marte_project.json,
// otherwise the nearest git root, otherwise fallback (which may be empty).
func ProjectHome(baseFile, fallback string) string {
	dir := filepath.Dir(baseFile)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	key := dir + "\x00" + fallback
	if cached, ok := homeCache.Load(key); ok {
		return cached.(string)
	}
	home := discoverProjectHome(dir, fallback)
	homeCache.Store(key, home)
	return home
}

func discoverProjectHome(dir, fallback string) string {
	gitRoot := ""
	for {
		if entryExists(filepath.Join(dir, projectMarker)) {
			return dir
		}
		// .git is usually a directory; some setups use a file.
		if gitRoot == "" && entryExists(filepath.Join(dir, ".git")) {
			gitRoot = dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if gitRoot != "" {
		return gitRoot
	}
	return fallback
}

// ResolvePath resolves p relative to the directory of the configuration file
// that references it, unless p is absolute.
func ResolvePath(baseFile, p string) string {
	return ResolvePathFrom(baseFile, "", p)
}

// ResolvePathFrom resolves a document path for a configuration file. Absolute
// paths are returned unchanged. A relative path is first tried against the
// project home (git root or the directory holding marte_project.json) and
// then against the directory of the configuration file, so projects can keep
// documents at their root while single-file configs keep working. When the
// file exists in neither place, the project-home form is returned so the
// diagnostic points at the canonical location.
func ResolvePathFrom(baseFile, home, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	// Explicitly relative navigation (`./x`, `../x`) always means "from this
	// file", never from the project home.
	if isDotRelative(p) {
		return filepath.Join(filepath.Dir(baseFile), p)
	}
	fromHome := ""
	if home != "" {
		fromHome = filepath.Join(home, p)
		if fileExists(fromHome) {
			return fromHome
		}
	}
	fromFile := filepath.Join(filepath.Dir(baseFile), p)
	if fileExists(fromFile) {
		return fromFile
	}
	if fromHome != "" {
		return fromHome
	}
	return fromFile
}

// isDotRelative reports whether p is explicitly relative to the current
// file (`./…` or `../…`).
func isDotRelative(p string) bool {
	return p == "." || p == ".." ||
		strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// entryExists reports whether path exists (file or directory).
// ProjectRootFor returns the project home for the given source files: the
// nearest ancestor directory containing marte_project.json, else fallback.
// The project home anchors the .marte_schema.cue cascade, so sources kept in
// a subdirectory (src/, cfg/, ...) still load the project schema.
func ProjectRootFor(files []string, fallback string) string {
	for _, f := range files {
		dir := filepath.Dir(f)
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		for {
			if entryExists(filepath.Join(dir, projectMarker)) {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return fallback
}

func entryExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
