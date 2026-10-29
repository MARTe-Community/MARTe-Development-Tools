package schema

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"
)

//go:embed marte.cue
var defaultSchemaCUE []byte

type Schema struct {
	Context *cue.Context
	Value   cue.Value
	// Errors lists schema files that could not be applied to Value, with the
	// reason. A file that fails to compile (or conflicts with a lower layer)
	// contributes nothing, so every class it declares is reported as unknown
	// unless the failure itself is surfaced.
	Errors []LoadError
}

// LoadError reports one schema file that was skipped while assembling the
// effective schema.
type LoadError struct {
	Path string
	Err  error
}

// Message renders the failure as one line, including the CUE details.
func (e LoadError) Message() string {
	detail := cueerrors.Details(e.Err, nil)
	detail = strings.Join(strings.Fields(detail), " ")
	if detail == "" {
		detail = e.Err.Error()
	}
	return fmt.Sprintf("Ignoring schema file %s: %s (classes it declares will be reported as unknown)", e.Path, detail)
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

// schemaPaths returns the ordered list of optional system/project schema
// files that get unified on top of the embedded base schema for projectRoot.
// Order matters: later paths override/extend earlier ones.
func schemaPaths(projectRoot string) []string {
	paths := []string{"/usr/share/mdt/marte_schema.cue"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".local/share/mdt/marte_schema.cue"))
	}
	if projectRoot != "" {
		paths = append(paths, filepath.Join(projectRoot, ".marte_schema.cue"))
	}
	return paths
}

func fileModTime(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().UnixNano()
}

func modTimesEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

type fullSchemaCacheEntry struct {
	schema   *Schema
	modTimes map[string]int64
}

var (
	fullSchemaCacheMu sync.Mutex
	fullSchemaCache   = make(map[string]*fullSchemaCacheEntry)
)

// LoadFullSchema loads the built-in schema, unified with any system/project
// override files, for projectRoot. The result is cached per projectRoot and
// reused across calls as long as none of the underlying schema files have
// changed on disk (checked via mtime). This matters because LoadFullSchema
// is called on every LSP validation pass -- i.e. on every keystroke, via
// validator.NewValidator -- and re-parsing and re-unifying the CUE schema
// from scratch each time was a measurable, entirely avoidable hot path.
func LoadFullSchema(projectRoot string) *Schema {
	paths := schemaPaths(projectRoot)
	modTimes := make(map[string]int64, len(paths))
	for _, p := range paths {
		modTimes[p] = fileModTime(p)
	}

	// Fast path: read-lock equivalent check.
	fullSchemaCacheMu.Lock()
	if entry, ok := fullSchemaCache[projectRoot]; ok && modTimesEqual(entry.modTimes, modTimes) {
		fullSchemaCacheMu.Unlock()
		return entry.schema
	}
	fullSchemaCacheMu.Unlock()

	s := loadFullSchemaUncached(paths)

	// Write path: double-check that no other goroutine populated the cache
	// while we were computing. This closes the TOCTOU window between the
	// first unlock and the re-lock.
	fullSchemaCacheMu.Lock()
	if entry, ok := fullSchemaCache[projectRoot]; ok && modTimesEqual(entry.modTimes, modTimes) {
		fullSchemaCacheMu.Unlock()
		return entry.schema
	}
	fullSchemaCache[projectRoot] = &fullSchemaCacheEntry{schema: s, modTimes: modTimes}
	fullSchemaCacheMu.Unlock()

	return s
}

func loadFullSchemaUncached(paths []string) *Schema {
	ctx := cuecontext.New()
	baseVal := ctx.CompileBytes(defaultSchemaCUE)
	if baseVal.Err() != nil {
		// Fallback or panic? Panic is appropriate for embedded schema failure
		panic(fmt.Sprintf("Embedded schema invalid: %v", baseVal.Err()))
	}

	var loadErrors []LoadError
	for _, path := range paths {
		val, err := LoadSchema(ctx, path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			loadErrors = append(loadErrors, LoadError{Path: path, Err: err})
			continue
		}
		if val.Err() != nil {
			loadErrors = append(loadErrors, LoadError{Path: path, Err: val.Err()})
			continue
		}
		merged := baseVal.Unify(val)
		if merged.Err() != nil {
			// A conflicting overlay must not replace the schema assembled so
			// far: an error value would make every lookup fail.
			loadErrors = append(loadErrors, LoadError{Path: path, Err: merged.Err()})
			continue
		}
		baseVal = merged
	}

	return &Schema{
		Context: ctx,
		Value:   baseVal,
		Errors:  loadErrors,
	}
}
