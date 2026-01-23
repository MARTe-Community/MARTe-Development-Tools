package schema

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

//go:embed marte.cue
var defaultSchemaCUE []byte

type Schema struct {
	Context *cue.Context
	Value   cue.Value
}

func NewSchema() *Schema {
	ctx := cuecontext.New()
	return &Schema{
		Context: ctx,
		Value:   ctx.CompileBytes(defaultSchemaCUE),
	}
}

// LoadSchema loads a CUE schema from a file and returns the cue.Value
func LoadSchema(ctx *cue.Context, path string) (cue.Value, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return cue.Value{}, err
	}
	return ctx.CompileBytes(content), nil
}

func LoadFullSchema(projectRoot string) *Schema {
	ctx := cuecontext.New()
	baseVal := ctx.CompileBytes(defaultSchemaCUE)
	if baseVal.Err() != nil {
		// Fallback or panic? Panic is appropriate for embedded schema failure
		panic(fmt.Sprintf("Embedded schema invalid: %v", baseVal.Err()))
	}

	// 1. System Paths
	sysPaths := []string{
		"/usr/share/mdt/marte_schema.cue",
	}

	home, err := os.UserHomeDir()
	if err == nil {
		sysPaths = append(sysPaths, filepath.Join(home, ".local/share/mdt/marte_schema.cue"))
	}

	for _, path := range sysPaths {
		if val, err := LoadSchema(ctx, path); err == nil && val.Err() == nil {
			baseVal = baseVal.Unify(val)
		}
	}

	// 2. Project Path
	if projectRoot != "" {
		projectSchemaPath := filepath.Join(projectRoot, ".marte_schema.cue")
		if val, err := LoadSchema(ctx, projectSchemaPath); err == nil && val.Err() == nil {
			baseVal = baseVal.Unify(val)
		}
	}

	return &Schema{
		Context: ctx,
		Value:   baseVal,
	}
}
