package schema

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
)

//go:embed marte.json
var defaultSchemaJSON []byte

type Schema struct {
	Classes map[string]ClassDefinition `json:"classes"`
}

type ClassDefinition struct {
	Fields  []FieldDefinition `json:"fields"`
	Ordered bool              `json:"ordered"`
}

type FieldDefinition struct {
	Name      string `json:"name"`
	Type      string `json:"type"` // "int", "float", "string", "bool", "reference", "array", "node", "any"
	Mandatory bool   `json:"mandatory"`
}

func NewSchema() *Schema {
	return &Schema{
		Classes: make(map[string]ClassDefinition),
	}
}

func LoadSchema(path string) (*Schema, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var s Schema
	if err := json.Unmarshal(content, &s); err != nil {
		return nil, fmt.Errorf("failed to parse schema: %v", err)
	}

	return &s, nil
}

// DefaultSchema returns a built-in schema with core MARTe classes
func DefaultSchema() *Schema {
	var s Schema
	if err := json.Unmarshal(defaultSchemaJSON, &s); err != nil {
		panic(fmt.Sprintf("failed to parse default embedded schema: %v", err))
	}
	return &s
}
