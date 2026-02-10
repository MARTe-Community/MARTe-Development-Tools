package integration

import (
	"context"
	"testing"

	"github.com/marte-community/marte-dev-tools/internal/index"
	"github.com/marte-community/marte-dev-tools/internal/parser"
	"github.com/marte-community/marte-dev-tools/internal/validator"
)

func TestValidator_ByteSize(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected []string // Substrings expected in error messages
	}{
		{
			name: "Correct ByteSize default elements",
			src: `
			+Data = {
				Class = ReferenceContainer
				+MyDS = {
					Class = DataSource
					Signals = {
						Sig1 = {
							Type = uint32
							ByteSize = 4
						}
					}
				}
			}`,
			expected: []string{},
		},
		{
			name: "Correct ByteSize with elements",
			src: `
			+Data = {
				Class = ReferenceContainer
				+MyDS = {
					Class = DataSource
					Signals = {
						Sig1 = {
							Type = uint32
							NumberOfElements = 10
							ByteSize = 40
						}
					}
				}
			}`,
			expected: []string{},
		},
		{
			name: "Incorrect ByteSize default elements",
			src: `
			+Data = {
				Class = ReferenceContainer
				+MyDS = {
					Class = DataSource
					Signals = {
						Sig1 = {
							Type = uint32
							ByteSize = 8 // Expected 4
						}
					}
				}
			}`,
			expected: []string{"Size mismatch", "defined 8", "expected 4"},
		},
		{
			name: "Correct ByteSize with elements and dimensions",
			src: `
			+Data = {
				Class = ReferenceContainer
				+MyDS = {
					Class = DataSource
					Signals = {
						Sig1 = {
							Type = uint32
							NumberOfElements = 10
							NumberOfDimensions = 2
							ByteSize = 80
						}
					}
				}
			}`,
			expected: []string{},
		},
		{
			name: "Correct ByteDimension field",
			src: `
			+Data = {
				Class = ReferenceContainer
				+MyDS = {
					Class = DataSource
					Signals = {
						Sig1 = {
							Type = uint32
							NumberOfElements = 10
							ByteDimension = 40
						}
					}
				}
			}`,
			expected: []string{},
		},
		{
			name: "Incorrect ByteDimension",
			src: `
			+Data = {
				Class = ReferenceContainer
				+MyDS = {
					Class = DataSource
					Signals = {
						Sig1 = {
							Type = uint16
							NumberOfElements = 5
							NumberOfDimensions = 3
							ByteDimension = 10 // Expected 2 * 5 * 3 = 30
						}
					}
				}
			}`,
			expected: []string{"Size mismatch", "defined 10", "expected 30"},
		},
		{
			name: "Missing Type (cannot validate ByteSize)",
			src: `
			+Data = {
				Class = ReferenceContainer
				+MyDS = {
					Class = DataSource
					Signals = {
						Sig1 = {
							ByteSize = 4
						}
					}
				}
			}`,
			// "Missing mandatory field Type" is expected from other checks,
			// but here we check specifically for ByteSize mismatch which shouldn't happen if Type is missing.
			expected: []string{"missing mandatory field 'Type'"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt := index.NewProjectTree()
			p := parser.NewParser(tt.src)
			cfg, err := p.Parse()
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			pt.AddFile("test.marte", cfg)

			v := validator.NewValidator(pt, ".", nil)
			v.ValidateProject(context.Background())

			for _, exp := range tt.expected {
				found := false
				for _, d := range v.Diagnostics {
					if contains(d.Message, exp) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected error containing '%s', but got: %v", exp, v.Diagnostics)
				}
			}

			if len(tt.expected) == 0 && len(v.Diagnostics) > 0 {
				// Filter out unrelated errors if any (e.g. unknown class if schema not loaded)
				// But we expect 0 errors for "Correct" cases.
				// Note: Schema validation might fail if classes like DataSource aren't in loaded schema or mock.
				// But our ByteSize check is unrelated to schema.
				// However, if we get unrelated errors, we should inspect them.
				// For this test, let's assume we might get "Unknown Class" warnings/errors if schema is missing.
				// We should be careful.
				// Let's filter for ByteSize errors.
				for _, d := range v.Diagnostics {
					if contains(d.Message, "Size mismatch") {
						t.Errorf("Unexpected Size error: %s", d.Message)
					}
				}
			}
		})
	}
}
