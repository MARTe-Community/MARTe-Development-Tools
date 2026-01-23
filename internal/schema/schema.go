package schema

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed marte.json
var defaultSchemaJSON []byte

type Schema struct {
	Classes map[string]ClassDefinition `json:"classes"`
}

type ClassDefinition struct {
	Fields    []FieldDefinition `json:"fields"`
	Ordered   bool              `json:"ordered"`
	Direction string            `json:"direction"`
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

// DefaultSchema returns the built-in embedded schema
func DefaultSchema() *Schema {
	var s Schema
	if err := json.Unmarshal(defaultSchemaJSON, &s); err != nil {
		panic(fmt.Sprintf("failed to parse default embedded schema: %v", err))
	}
	if s.Classes == nil {
		s.Classes = make(map[string]ClassDefinition)
	}
	return &s
}

// Merge adds rules from 'other' to 's'.
// Rules for the same class are merged (new fields added, existing fields updated).
func (s *Schema) Merge(other *Schema) {
	if other == nil {
		return
	}
	for className, classDef := range other.Classes {
		if existingClass, ok := s.Classes[className]; ok {
			// Merge fields
			fieldMap := make(map[string]FieldDefinition)
			for _, f := range classDef.Fields {
				fieldMap[f.Name] = f
			}

			var mergedFields []FieldDefinition
			seen := make(map[string]bool)

			// Keep existing fields, update if present in other
			for _, f := range existingClass.Fields {
				if newF, ok := fieldMap[f.Name]; ok {
					mergedFields = append(mergedFields, newF)
				} else {
					mergedFields = append(mergedFields, f)
				}
				seen[f.Name] = true
			}

			// Append new fields
			for _, f := range classDef.Fields {
				if !seen[f.Name] {
					mergedFields = append(mergedFields, f)
				}
			}

			existingClass.Fields = mergedFields
			if classDef.Ordered {
				existingClass.Ordered = true
			}
			if classDef.Direction != "" {
				existingClass.Direction = classDef.Direction
			}
			s.Classes[className] = existingClass
		} else {
			s.Classes[className] = classDef
		}
	}
}

func LoadFullSchema(projectRoot string) *Schema {
	s := DefaultSchema()

	// 1. System Paths
	sysPaths := []string{
		"/usr/share/mdt/marte_schema.json",
	}

	home, err := os.UserHomeDir()
	if err == nil {
		sysPaths = append(sysPaths, filepath.Join(home, ".local/share/mdt/marte_schema.json"))
	}

	for _, path := range sysPaths {
		if sysSchema, err := LoadSchema(path); err == nil {
			s.Merge(sysSchema)
		}
	}

	// 2. Project Path
	if projectRoot != "" {
		projectSchemaPath := filepath.Join(projectRoot, ".marte_schema.json")
		if projSchema, err := LoadSchema(projectSchemaPath); err == nil {
			s.Merge(projSchema)
		}
	}

	return s
}
