# Repository Guidelines

## Project Overview

**MARTe Development Tools (`mdt`)** is a comprehensive CLI toolkit and LSP server for developing, validating, and building configurations for the MARTe real-time framework. It provides a single portable Go binary that understands an extended MARTe configuration language (`.marte` files) with multi-file projects, variables, templates, conditional blocks, and signal-flow analysis.

Two binaries:
- **`mdt`** — the primary tool: LSP server, build/merge, validation, formatting, graph visualization, project scaffolding.
- **`mtr`** — an internal SQLite-backed test-runner/reporter (not part of the `mdt` CLI).

Module: `github.com/marte-community/marte-dev-tools` | Go 1.25

## Architecture & Data Flow

The pipeline is a sequence of independent stages built on one shared data structure:

```
Parse → Index → Validate → Build / LSP / Graph / Format
```

```
┌──────────┐     ┌──────────┐     ┌───────────┐
│  Parser  │────▶│  Index   │────▶│ Validator  │
│ (lexer + │     │(Project  │     │(CUE schema│
│  AST)    │     │ Tree)    │     │ checks)   │
└──────────┘     └──────────┘     └─────┬─────┘
                                        │
                   ┌────────────────────┼────────────────────┐
                   ▼                    ▼                    ▼
            ┌──────────┐        ┌──────────┐        ┌──────────┐
            │ Builder  │        │   LSP    │        │  Graph   │
            │(merge +  │        │(JSON-RPC │        │(DOT/HTML │
            │ evaluate)│        │ server)  │        │ signal)  │
            └──────────┘        └──────────┘        └──────────┘
```

### Package Dependency Graph

```
logger ──▶ index, lsp
parser ──▶ index, formatter, validator, graph, builder, lsp    (AST types consumed everywhere)
index  ──▶ builder, validator, graph, lsp                      (ProjectTree is the central data model)
schema ──▶ validator, builder, lsp                             (CUE schema loading + caching)
lsp/cache ──▶ lsp                                              (Session/View/Snapshot)
formatter ──▶ lsp
validator ──▶ builder, lsp                                     (shared validation engine)
```

### Key Data Flow: LSP Edit Cycle

1. Editor sends `didOpen`/`didChange` → `HandleDidOpen`/`HandleDidChange`
2. Clone snapshot → parse text → `AddFile` into `ProjectTree` → `ResolveReferences`/`ResolveFields` → publish snapshot → trigger debounced validation (1 second)
3. Debounced `runValidation` → `validator.ValidateProject` (multi-pass activation + CUE checks) → `publishDiagnosticsForFile`
4. All LSP features (hover, goto-def, completion, rename, symbols, inlay hints, call hierarchy) read from the **current snapshot's immutable tree** — lock-free concurrent reads

### Key Data Flow: CLI Build Cycle

1. `NewBuilder(files, overrides)` → parse each file → build `ProjectTree` → multi-pass active-node collection → `ResolveFields`/`ResolveReferences` → `validator.ValidateProject` → output merged config

### Shared Validation Engine

`check`, `build`, `lsp`, and `graph` all call the **same** `validator.ValidateProject`. Never add command-specific validation logic elsewhere.

## Key Directories

| Directory | Purpose |
|-----------|---------|
| `cmd/mdt/` | Main CLI entry point — manual `os.Args` dispatch to 7 subcommands |
| `cmd/mtr/` | Test-runner/reporter (`mtr run`/`report`/`stats`) with SQLite backend |
| `internal/parser/` | Hand-written lexer + recursive-descent parser → AST (`.marte` → `*Configuration`) |
| `internal/index/` | `ProjectTree` — the central in-memory data model (~2400 lines): symbol table, reference resolution, concurrent directory scanning |
| `internal/validator/` | Semantic validation engine (~2470 lines): CUE schema conformance, signal type/direction, threading constraints, multi-pass conditional activation |
| `internal/lsp/` | LSP server (~3630 lines): all JSON-RPC handlers, validation lifecycle, debouncing. Sub-package `lsp/cache/` holds Session/View/Snapshot |
| `internal/builder/` | Multi-file merge + template/variable evaluation |
| `internal/formatter/` | Standardized `.marte` pretty-printer |
| `internal/graph/` | Signal-flow graph → DOT/HTML with state/thread filtering and IOGAM bypass simplification |
| `internal/schema/` | CUE schema integration — embeds `marte.cue`, provides `LoadFullSchema()` with mtime-based caching |
| `internal/logger/` | Global singleton logger with colored ANSI output, `NO_COLOR` support |
| `test/` | All unit/integration tests (single `integration` package, no `*_test.go` in `internal/`) |
| `test/e2e/` | End-to-end tests that shell out to the compiled `build/mdt` binary |
| `docs/` | Tutorial, configuration guide, editor integration, graph guide, code documentation, e2e framework docs |
| `examples/` | `.marte` example projects: simple, complex multi-file, advanced language features, real-world config |
| `subprj/tree-sitter-marte/` | Standalone tree-sitter grammar for editor syntax highlighting (JS → C, Rust, Python, Go bindings) |
| `specification.md` | Full project spec — CLI, LSP features, language grammar, semantics, validation rules |
| `MARTe.ebnf` | Complete EBNF grammar for the MARTe extended configuration language |

## Development Commands

```bash
# Build
go build ./cmd/mdt              # dev build → ./mdt
make build                      # production build → build/mdt
make build_codac                # cross-compile linux/amd64, CGO disabled

# Test
go test ./test/...              # all unit/integration tests
go test ./test/... -run TestX   # single test
make test                       # go test -v ./test/
make test-e2e                   # build mdt, then run ./test/e2e/...
go test ./test/e2e/... -run "TestFixtures/my_fixture"
make coverage                   # coverage across ./internal/...

# Lint / Vet
go vet ./...
go fmt ./...
make vet

# Install
go install ./cmd/mdt            # to $GOPATH/bin
```

## Code Conventions & Common Patterns

### Go Style
- Standard Go formatting (`go fmt`). No custom formatter config.
- Error handling: standard Go pattern — return `error`, check at call site. `t.Fatalf` for test setup failures, `t.Errorf` for assertions.
- Naming: CamelCase exported, camelCase unexported. Test files: `domain_aspect_test.go`.

### Central Data Model: `ProjectTree` (`internal/index/index.go`)
- **`ProjectTree`** is the shared symbol table for every downstream package.
- **`ProjectNode`** — objects/signals/variables keyed by normalized name (O(1) lookup via `NodeMap`).
- **`Fragment`** — per-file definition blocks; a node can be split across multiple files, fragments are aggregated.
- **`Reference`** — cross-file name resolution with lexical scoping.
- **`EvaluationContext`** — variable scope chaining for `#var`/`#let` evaluation.
- Thread safety via `sync.RWMutex`. Concurrent directory scanning capped at 8 goroutines.

### Snapshot-based Immutability (`internal/lsp/cache/`)
- **`Session`** → one `mdt lsp` process lifetime.
- **`View`** → one workspace root; owns an `atomic.Value` of `Snapshot`.
- **`Snapshot`** → immutable clone of the full project state (tree, documents, parser errors).
- `Clone()` creates a fully-resolved deep copy; `CloneForEdit()` skips resolution for mutation efficiency.
- This enables **lock-free concurrent reads** while edits happen serially.

### Multi-pass Conditional Activation
Both `validator` and `builder` independently implement active-node collection — evaluating `#if`/`#else`/`#foreach`/`#template`/`#use` across up to 5 passes because newly activated nodes may define variables that change condition evaluation.

### LSP Global State
Package-level globals in `internal/lsp/`: `GlobalSession`, `GlobalSchema`, `Output`, `PublishDiagnosticsFn`, `GraphNotifyFn`. `GlobalSession` is a single `cache.Session` with multiple `View`s (keyed by workspace root). This lets the go-lsp handler adapter coexist with legacy direct `Handle*` calls used by tests.

### Two-tier Diagnostic Publishing
- **Parser errors**: published immediately (no debounce).
- **Semantic validation errors**: debounced 1 second after last change, with cancellation of in-flight validation via `context.Context`.
- Diagnostics are JSON-hashed to avoid re-publishing unchanged sets.

### CUE Schema Cascade
```
built-in (embedded marte.cue) → system (/usr/share/mdt/) → user (~/.local/share/) → project (.marte_schema.cue)
```
Later sources extend/override earlier ones. Full result cached per `projectRoot` with mtime-based invalidation.

### Signal Flow Graph Simplification (`internal/graph/`)
- Level 1: bypass IOGAM and pass-through DS nodes, preserving signal detail with dashed edges.
- Level 2: collapse to plain box nodes (no signal tables).
- DS split algorithm: splits a DataSource into read-clone + main when a GAM reads before any write in the same cycle (MARTe2 thread execution semantics).

### CLI Flag Parsing
`cmd/mdt/main.go` uses **manual `os.Args` switch + hand-rolled flag loops** — no `flag` package, no `cobra`. `cmd/mtr/` uses the standard `flag` package with `flag.NewFlagSet` per subcommand.

### Logging
All logs via `internal/logger` (global singleton). Output to `stderr` by default to avoid interfering with `stdout` (build artifacts, formatted text).

## Important Files

| File | Role |
|------|------|
| `cmd/mdt/main.go` | CLI entry point — subcommand dispatch, manual flag parsing |
| `cmd/mdt/graph.go` | Graph subcommand: interactive web server or static output |
| `internal/parser/ast.go` | All AST node types (Node, Definition, Value interfaces + ~20 concrete types) |
| `internal/parser/parser.go` | Recursive-descent parser → `*Configuration` |
| `internal/parser/lexer.go` | Hand-written lexer with ~40 token types |
| `internal/index/index.go` | `ProjectTree`, `ProjectNode`, `Fragment`, `Reference`, `EvaluationContext` — ~2400 lines |
| `internal/validator/validator.go` | `Validator`, `Diagnostics`, `ValidateProject`, `collectActiveNodes` — ~2470 lines |
| `internal/lsp/server.go` | All LSP JSON-RPC handlers — ~3630 lines |
| `internal/lsp/cache/cache.go` | `Session`, `View`, `Snapshot` — immutable state management |
| `internal/lsp/handler.go` | go-lsp server adapter wrapping Handle* functions |
| `internal/schema/schema.go` | `LoadFullSchema()` with embed + caching |
| `internal/schema/marte.cue` | Built-in CUE schema for standard MARTe classes |
| `internal/builder/builder.go` | `Builder`, merge + evaluate pipeline |
| `internal/formatter/formatter.go` | `Format(*Configuration, io.Writer)` |
| `internal/graph/graph.go` | `Generate()` → DOT/HTML signal-flow graph — ~2464 lines |
| `specification.md` | Authoritative spec for CLI, LSP, language grammar, semantics, validation |
| `MARTe.ebnf` | Complete EBNF grammar |
| `go.mod` | Module + dependencies |
| `Makefile` | Build, test, coverage, vet, fmt targets |

## Runtime & Tooling Preferences

- **Language**: Go 1.25
- **Build**: `go build` (no additional build tools)
- **Package manager**: Go modules (`go.mod`/`go.sum`)
- **Formatter**: `go fmt` (Go standard)
- **Linter**: `go vet`
- **No code generation** for the Go project (the tree-sitter subproject generates `parser.c` from `grammar.js`, but the Go parser is hand-written)
- **Root `package.json`**: only contains `@google/gemini-cli` dev dependency — not a Node project
- **Supported platforms**: Linux (primary), macOS, Windows. Statically compiled when `CGO_ENABLED=0`.
- **External binaries needed**: `dot` (Graphviz) for SVG graph output at runtime

## Testing & QA

### Framework
- **Pure Go stdlib `testing`** — no testify, gomega, or third-party assertions.
- Zero test dependencies in `go.mod`.

### Test Location & Package
- All tests in `test/` as package `integration` — no `*_test.go` files inside `internal/`.
- E2E tests in `test/e2e/` as separate package.

### Naming & Patterns
- Functions: `TestCamelCase`
- Subtests: `t.Run("description", func(t *testing.T) { ... })`
- Table-driven tests: `[]struct{ name, src string; expected ... }`
- Inputs: inline `.marte` strings for unit/integration tests; file fixtures for e2e.
- Assertions: `t.Errorf(...)` for test failures, `t.Fatalf(...)` for setup failures.
- LSP tests: `lsp.ResetTestServer()` setup, `lsp.HandleMessage(msg)` dispatch. No real subprocess.
- Helpers: `framework.AssertNoErrors`, `framework.AssertErrors` (e2e only).

### Test Categories (94+ files in `test/`)

| Category | Example files | Count |
|----------|--------------|-------|
| Validator | `validator_iogam_test.go`, `validator_schema_meta_test.go`, `validator_variable_usage_test.go` | ~25 |
| LSP | `lsp_server_test.go`, `lsp_fuzz_test.go`, `lsp_hover_namespace_test.go`, `lsp_incremental_correctness_test.go`, `lsp_references_repro_test.go` | ~30 |
| Parser/Lexer | `lexer_test.go`, `parser_test.go`, `ast_test.go` | ~5 |
| Index | `index_test.go`, `scoping_test.go`, `let_macro_test.go` | ~6 |
| Formatter | `formatter_test.go` | ~6 |
| Builder | `builder_test.go` | ~3 |
| Integration | `integration_test.go`, `isolation_test.go` | ~10 |
| E2E | `test/e2e/{check,build,lsp,format}_test.go` | 4 |
| Regression | `project_filter_test.go`, `index_cleanup_test.go` | ~5 |

### E2E Framework (`test/e2e/`)
- Compiles `build/mdt` (override with `MDT_BINARY` env var), runs as subprocess.
- `TestContext`: temp directory lifecycle, CLI wrappers (`RunBuild`, `RunCheck`, `RunFmt`, `RunLSP`).
- `LSPTestClient`: full JSON-RPC 2.0 over stdio — initialize, open, edit, hover, completion, definition.
- **Fixture format** (`test/e2e/fixtures/<name>/`): `TEST.toml` config + `inputs/` directory + `expected/` output validation.
- Two example fixtures: `example_valid_config` (check tool), `example_build` (build tool).

### Running Tests
```bash
make test                         # unit/integration
make test-e2e                     # end-to-end (builds mdt first)
go test ./test/... -run TestName  # single test
make coverage                     # coverage report
```
