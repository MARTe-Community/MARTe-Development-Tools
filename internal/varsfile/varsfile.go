// Package varsfile loads variable override files for the mdt CLI.
//
// The supported format is a flat JSON object mapping variable names to
// values:
//
//	{
//	  "udp_streamer": false,
//	  "NumChannels": 8,
//	  "SamplingFrequency": 1.0e6,
//	  "HostName": "localhost",
//	  "CPUMask": [1, 2]
//	}
//
// Values are converted to MARTe configuration literals so they can flow
// through the regular override pipeline. Nested objects are rejected:
// variables are scalars or lists of scalars.
package varsfile

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// LoadJSON reads a JSON variable file and returns name -> literal maps.
// The returned slice lists the file order; keys of later files override
// earlier ones when merged by the caller.
func LoadJSON(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON: %v", path, err)
	}
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

// SortedNames returns the variable names of a set of overrides, sorted.
// Useful for deterministic diagnostics.
func SortedNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
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
