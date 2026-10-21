package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/varsfile"
)

func writeVarsFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// Load dispatches on the extension and all three formats produce the
// same MARTe literals for the same values.
func TestVarsFileFormatsEquivalent(t *testing.T) {
	jsonPath := writeVarsFile(t, "vars.json", `{
	  "udp_streamer": false,
	  "NumChannels": 8,
	  "SamplingFrequency": 1.0e6,
	  "HostName": "localhost",
	  "CPUMask": [1, 2]
	}`)
	yamlPath := writeVarsFile(t, "vars.yaml", `udp_streamer: false
NumChannels: 8
SamplingFrequency: 1.0e6
HostName: localhost
CPUMask: [1, 2]
`)
	tomlPath := writeVarsFile(t, "vars.toml", `udp_streamer = false
NumChannels = 8
SamplingFrequency = 1.0e6
HostName = "localhost"
CPUMask = [1, 2]
`)

	want := map[string]string{
		"udp_streamer":      "false",
		"NumChannels":       "8",
		"SamplingFrequency": "1000000",
		"HostName":          `"localhost"`,
		"CPUMask":           "{ 1, 2 }",
	}

	for _, path := range []string{jsonPath, yamlPath, tomlPath} {
		got, err := varsfile.Load(path)
		if err != nil {
			t.Fatalf("Load(%s): %v", filepath.Base(path), err)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", filepath.Base(path), k, got[k], v)
			}
		}
		if len(got) != len(want) {
			t.Errorf("%s: got %d variables, want %d", filepath.Base(path), len(got), len(want))
		}
	}
}

// .yml is accepted like .yaml.
func TestVarsFileYAMLExtension(t *testing.T) {
	path := writeVarsFile(t, "vars.yml", "Enabled: true\n")
	vars, err := varsfile.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if vars["Enabled"] != "true" {
		t.Errorf("Enabled = %q, want true", vars["Enabled"])
	}
}

// YAML integers, floats and quoted strings keep their MARTe literal form.
func TestVarsFileYAMLValueKinds(t *testing.T) {
	path := writeVarsFile(t, "vars.yaml", `Count: 42
Ratio: 0.5
Label: "a b"
Path: /tmp/x
`)
	vars, err := varsfile.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]string{
		"Count": "42",
		"Ratio": "0.5",
		"Label": `"a b"`,
		"Path":  `"/tmp/x"`,
	}
	for k, v := range want {
		if vars[k] != v {
			t.Errorf("%s = %q, want %q", k, vars[k], v)
		}
	}
}

// TOML arrays of mixed scalar kinds and inline tables: arrays of scalars
// work, inline tables (nested objects) are rejected.
func TestVarsFileTOMLNestedRejected(t *testing.T) {
	path := writeVarsFile(t, "vars.toml", "[Group]\nEnabled = true\n")
	if _, err := varsfile.Load(path); err == nil {
		t.Fatal("expected error for nested TOML table")
	} else if !strings.Contains(err.Error(), "nested objects") {
		t.Errorf("error %q does not mention nested objects", err)
	}
}

func TestVarsFileYAMLNestedRejected(t *testing.T) {
	path := writeVarsFile(t, "vars.yaml", "Group:\n  Enabled: true\n")
	if _, err := varsfile.Load(path); err == nil {
		t.Fatal("expected error for nested YAML mapping")
	} else if !strings.Contains(err.Error(), "nested objects") {
		t.Errorf("error %q does not mention nested objects", err)
	}
}

// Malformed files report the format and the path.
func TestVarsFileMalformed(t *testing.T) {
	cases := []struct{ name, content, want string }{
		{"bad.yaml", "a: [1,\n", "invalid YAML"},
		{"bad.toml", "a = \n", "invalid TOML"},
		{"bad.json", `{"a": }`, "invalid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeVarsFile(t, tc.name, tc.content)
			_, err := varsfile.Load(path)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.name) {
				t.Errorf("error %q does not name the file", err)
			}
		})
	}
}

// Unknown extensions are rejected instead of silently loading nothing.
func TestVarsFileUnknownExtension(t *testing.T) {
	path := writeVarsFile(t, "vars.txt", "A: 1\n")
	if _, err := varsfile.Load(path); err == nil {
		t.Fatal("expected error for .txt")
	} else if !strings.Contains(err.Error(), "unsupported variables file extension") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Null values are not valid MARTe literals, in any format.
func TestVarsFileNullRejected(t *testing.T) {
	yamlPath := writeVarsFile(t, "vars.yaml", "Unset: null\n")
	if _, err := varsfile.Load(yamlPath); err == nil {
		t.Fatal("expected error for null YAML value")
	}
	tomlPath := writeVarsFile(t, "vars.toml", "Unset = 0\n")
	if _, err := varsfile.Load(tomlPath); err != nil {
		t.Fatalf("Load(toml): %v", err)
	}
	jsonPath := writeVarsFile(t, "vars.json", `{"Unset": null}`)
	if _, err := varsfile.Load(jsonPath); err == nil {
		t.Fatal("expected error for null JSON value")
	}
}
