// Package varsfile loads variable override files for the mdt CLI.
//
// Flat JSON, YAML and TOML objects are supported, mapping variable names
// to values:
//
//	# vars.yaml
//	udp_streamer: false
//	NumChannels: 8
//	SamplingFrequency: 1.0e6
//	HostName: localhost
//	CPUMask: [1, 2]
//
//	# vars.toml
//	udp_streamer = false
//	NumChannels = 8
//	SamplingFrequency = 1.0e6
//	HostName = "localhost"
//	CPUMask = [1, 2]
//
// Values are converted to MARTe configuration literals so they can flow
// through the regular override pipeline. Nested objects are rejected:
// variables are scalars or lists of scalars.
package varsfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

// Load reads a variable file, picking the format from the file extension:
// .json, .yaml/.yml or .toml. Unknown extensions are rejected so a typo
// cannot silently load nothing.
func Load(path string) (map[string]string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return LoadJSON(path)
	case ".yaml", ".yml":
		return LoadYAML(path)
	case ".toml":
		return LoadTOML(path)
	default:
		return nil, fmt.Errorf("%s: unsupported variables file extension (use .json, .yaml, .yml or .toml)", path)
	}
}

// LoadJSON reads a JSON variable file and returns name -> literal maps.
func LoadJSON(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %v", path, err)
	}
	return convert(doc, path)
}

// LoadYAML reads a YAML variable file (one document, flat mapping).
func LoadYAML(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: invalid YAML: %v", path, err)
	}
	return convert(doc, path)
}

// LoadTOML reads a TOML variable file (top-level table).
func LoadTOML(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := toml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: invalid TOML: %v", path, err)
	}
	return convert(doc, path)
}

// convert turns a decoded document into MARTe literals. Keys are sorted
// so error reporting is deterministic.
func convert(doc map[string]any, path string) (map[string]string, error) {
	out := make(map[string]string, len(doc))
	for _, name := range sortedKeys(doc) {
		lit, err := toLiteral(doc[name])
		if err != nil {
			return nil, fmt.Errorf("%s: variable %q: %v", path, name, err)
		}
		out[name] = lit
	}
	return out, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// toLiteral converts a decoded JSON value to a MARTe configuration
// literal (a valid right-hand side of `Name = ...`).
func toLiteral(v any) (string, error) {
	switch t := v.(type) {
	case bool:
		return strconv.FormatBool(t), nil
	case string:
		return quote(t), nil
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), nil
		}
		return strconv.FormatFloat(t, 'g', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(t), 'g', -1, 32), nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case uint64:
		return strconv.FormatUint(t, 10), nil
	case []any:
		parts := make([]string, 0, len(t))
		for i, e := range t {
			switch e.(type) {
			case map[string]any, []any:
				return "", fmt.Errorf("element %d: nested objects/arrays are not supported", i)
			}
			lit, err := toLiteral(e)
			if err != nil {
				return "", err
			}
			parts = append(parts, lit)
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	case map[string]any:
		return "", fmt.Errorf("nested objects are not supported (variables are scalars or lists of scalars)")
	case nil:
		return "", fmt.Errorf("null is not a valid variable value")
	default:
		return "", fmt.Errorf("unsupported value type %T", v)
	}
}

// quote renders a Go string as a MARTe string literal. Only the two
// escapes the language needs are produced, keeping the output readable.
func quote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
