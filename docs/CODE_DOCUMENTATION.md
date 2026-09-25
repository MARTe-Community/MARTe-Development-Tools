# mdt Internal Code Documentation

This document provides a detailed overview of the `mdt` codebase architecture and internal components.

## Architecture Overview

`mdt` is built as a modular system where core functionalities are separated into internal packages. The data flow typically follows this pattern:

1.  **Parsing**: Source code is parsed into an Abstract Syntax Tree (AST).
2.  **Indexing**: ASTs from multiple files are aggregated into a unified `ProjectTree`.
3.  **Processing**: The `ProjectTree` is used by the Validator, Builder, and LSP server to perform their respective tasks.

## Package Structure

```
cmd/
  mdt/              # Application entry point (CLI)
internal/
  builder/          # Logic for merging and building configurations
  formatter/        # Code formatting engine
  index/            # Symbol table and project structure management
  logger/           # Centralized logging
  lsp/              # Language Server Protocol implementation
  parser/           # Lexer, Parser, and AST definitions
  schema/           # CUE schema loading and integration
  validator/        # Semantic analysis and validation logic
```

## Core Packages

### 1. `internal/parser`

Responsible for converting MARTe configuration text into structured data.

*   **Lexer (`lexer.go`)**: Tokenizes the input stream. Handles MARTe specific syntax like `#package`, `#let`, `//!` pragmas, and `//#` docstrings. Supports standard identifiers and `#`-prefixed identifiers. Recognizes advanced number formats (hex `0x`, binary `0b`).
*   **Parser (`parser.go`)**: Recursive descent parser. Converts tokens into a `Configuration` object containing definitions, comments, and pragmas. Implements expression parsing with precedence.
*   **AST (`ast.go`)**: Defines the node types (`ObjectNode`, `Field`, `Value`, `VariableDefinition`, `BinaryExpression`, etc.). All nodes implement the `Node` interface providing position information.

### 2. `internal/index`

The brain of the system. It maintains a holistic view of the project.

*   **ProjectTree**: The central data structure. It holds the root of the configuration hierarchy (`Root`), references, and isolated files.
*   **ScanDirectory**: Recursively walks the project directory to find all `.marte` files, adding them to the tree even if they contain partial syntax errors. Uses a semaphore to limit concurrency.
*   **ProjectNode**: Represents a logical node in the configuration. Since a node can be defined across multiple files (fragments), `ProjectNode` aggregates these fragments. It also stores locally defined variables and constants in its `Variables` map.
*   **NodeMap**: A hash map index (`map[string][]*ProjectNode`) for $O(1)$ symbol lookups, optimizing `FindNode` operations.
*   **Reference Resolution**: The `ResolveReferences` method links `Reference` objects to their target `ProjectNode` or `VariableDefinition`. It uses `FileReferences` (map by file) to enable incremental updates and respects lexical scoping rules.

### 3. `internal/validator`

Ensures configuration correctness. Supports **cancellation** via `context.Context`.

*   **Validator**: Iterates over the `ProjectTree` to check rules. Uses a throttled worker pool (limited to `runtime.NumCPU()`) for parallel validation of top-level nodes.
*   **Performance**: Employs a recursion depth limit (depth=3) when converting `ProjectNode` to CUE-compatible maps to prevent exponential overhead in large projects while still allowing nested signal validation.
*   **Checks**:
    *   **Structure**: Duplicate fields, invalid content.
    *   **Schema**: Unifies nodes with CUE schemas (loaded via `internal/schema`) to validate types and mandatory fields.
    *   **Signals**: Verifies that signals referenced in GAMs exist in DataSources and match types. Performs project-wide consistency checks for implicit signals.
    *   **Threading**: Checks `CheckDataSourceThreading` to ensure non-multithreaded DataSources are not shared across threads in the same state.
    *   **Ordering**: `CheckINOUTOrdering` verifies that for `INOUT` signals, the producing GAM appears before the consuming GAM in the thread's execution list.
    *   **Variables**: `CheckVariables` validates variable values against their defined CUE types. Prevents external overrides of `#let` constants. `CheckUnresolvedVariables` ensures all used variables are defined.
    *   **Unused**: Detects unused GAMs and Signals (suppressible via pragmas).

### 4. `internal/lsp`

Implements the Language Server Protocol.

*   **Server (`server.go`)**: Handles JSON-RPC messages over stdio.
*   **Typing-Friendly Diagnostics**: parser and semantic diagnostics are published
    together once typing pauses (250ms debounce). Mid-typing states are broken by
    construction, so publishing them immediately made editors flash errors that
    outlived the edit.
*   **Serialized Validation**: Validation tasks are debounced (250ms) and serialized to prevent concurrent `ValidateProject` calls from overloading the system.
*   **Cancellation**: Validation requests are tracked per-file URI. Triggering a new validation for a file automatically cancels any ongoing validation for that same file using context cancellation.
*   **Evaluation**: Implements a lightweight expression evaluator to show evaluated values in Hover and completion snippets.
*   **Incremental Sync**: Supports `textDocumentSync: 2`. `HandleDidChange` applies patches to the in-memory document buffers using `offsetAt` logic (LSP positions are UTF-16 code units, so columns are counted in code units, not bytes).
*   **Lifecycle**: `shutdown` is answered with an explicit `null` result and `exit` terminates the process. The go-lsp library omits a nil result (`{"jsonrpc":"2.0","id":N}`, invalid JSON-RPC) and swallows the notification error its own `exit` handler returns, so both are overridden in `RunServer`; without that, editors log "language server failed to terminate gracefully" and a stale server keeps running after a restart.
*   **Wire Positions**: every coordinate sent to the client goes through `gp()`/`clampRange()`. Internal positions are 0 for entities without a source location (the package fragment, nodes generated by `with`/`template`/`foreach` expansion) and LSP line/character are unsigned, so an unclamped `-1` makes clients reject the whole message with `invalid value: integer -1, expected u32` — silently dropping the response or the diagnostics.
*   **No Nested Tree Locks**: the tree's read lock is not reentrant once a writer is queued, so a read-locked iteration must never call another locking tree method. `Walk` holds the lock for the visitor; code that needs to query the tree per node uses `Nodes()` (a snapshot taken under the lock, then released) and calls getters freely. The LSP's feature handlers previously mixed the two: an inlay-hint request iterating with `Walk` and calling `ResolveName` for each `DS::Signal` shorthand deadlocked as soon as an edit (a writer) was queued — hints and every later request hung behind them, which is what editors report as a request timeout.
*   **Overrun Reporting**: quiet by default, but the server reports its own problems: every request and notification logs a `slow request <method>: <duration>` warning past `MDT_SLOW_REQUEST_MS` (default 500) and a `request <method> still running after <n>` watchdog line past `MDT_REQUEST_WATCHDOG_MS` (default 3000). The initial workspace scan is covered by `MDT_SLOW_SCAN_MS` (default 2000). Set a threshold to `0` to trace every request. Editors only say "request timed out" without naming the request, so this is the log to read when that happens.
*   **Quiet by Default**: routine progress (workspace scan, indexing, validation runs, hover lookups) is written to stderr only when `MDT_DEBUG=1` (or `MDT_VERBOSE=1`/`MDT_LOG=<level>`). Editors surface every stderr line as an error, so the server keeps stderr for real problems. Set `MDT_DEBUG=1` in the editor's server environment when diagnosing.
*   **Incremental Indexing**: `AddFile` only walks the tree to remove a file's previous fragments when that file is already indexed (`knownFiles`). A file being loaded for the first time has nothing to remove, so indexing a workspace is linear; the walk made it quadratic (1200 files: 3.4s before, 0.05s after, and larger trees never finished before the editor's timeout). Re-adds still replace content: removing a file drops its fragments from shared nodes and prunes what became empty, without deleting subtrees that other files still contribute to.
*   **Workspace Scan Scope**: the recursive scan skips hidden directories and dependency/build trees (`node_modules`, `vendor`, `target`, `dist`, `__pycache__`, `site-packages`, `venv`, `bower_components`), and stops after `MDT_MAX_SCAN_ENTRIES` entries (default 200000) with a warning. Pointing the workspace at a home directory (millions of entries, no source of interest) used to make `initialize` run for minutes; it now answers in about a second and says why the tree was truncated. The scan root itself is always entered, so a dot-directory workspace works.
*   **Container Index**: `ResolveReferences` answers "which node contains this reference?" through a per-file span index (`buildContainerIndex`) instead of walking the whole tree per reference; the tree walk made each edit quadratic (~400ms per keystroke on a 36-file project, now ~5ms).
*   **Features**:
    *   `HandleCompletion`: Context-aware suggestions (Macros, Schema fields, Signal references, Class names).
    *   `HandleHover`: Shows documentation (including docstrings for variables), evaluated signal types/dimensions, and usage analysis.
    *   `HandleDefinition` / `HandleReferences`: specific lookup using the `index`.
    *   `HandleTypeDefinition`: Jumps from object instances (`+`) to their templates (`$`) or from signal usages to definitions in DataSources.
    *   `HandleCodeAction`: Provides quick-fixes for common errors (e.g., adding missing `Class` or `Type` fields).
    *   `HandleRename`: Project-wide renaming supporting objects, fields, and signals (including implicit ones).
    *   `HandleDocumentSymbol`: Provides a hierarchical view of objects, signals, variables, and constants within a file.
    *   `HandleWorkspaceSymbol`: Enables project-wide symbol searching with container context.
    *   `Call Hierarchy`: Traces signal flow between GAMs and DataSources (`IncomingCalls` shows producers, `OutgoingCalls` shows consumers).

### 5. `internal/builder`

Merges multiple MARTe files into a single output.

*   **Logic**: It parses all input files, builds a temporary `ProjectTree`, and then reconstructs the source code.
*   **Merging**: It interleaves fields and subnodes from different file fragments to produce a coherent single-file configuration, respecting the `#package` hierarchy.
*   **Evaluation**: Evaluates all expressions and variable references into concrete MARTe values in the final output. Prevents overrides of `#let` constants.

### 6. `internal/schema`

Manages CUE schemas.

*   **Loading**: Loads the embedded default schema (`marte.cue`) and merges it with any user-provided `.marte_schema.cue`.
*   **Metadata**: Handles the `#meta` field in schemas to extract properties like `direction` and `multithreaded` support for the validator.

### 7. `internal/logger`

Centralized logging facility.

*   **Customizable Output**: Supports redirection of log output to any `io.Writer` via `SetOutput`, facilitating testing and integration into different environments.
*   **Convenience Wrappers**: Provides standard logging methods (`Printf`, `Println`, `Fatal`) with a consistent `[mdt]` prefix.

## Key Data Flows

### Reference Resolution
1.  **Scan**: Files are parsed and added to the `ProjectTree`.
2.  **Index**: `RebuildIndex` populates `NodeMap`.
3.  **Resolve**: `ResolveReferences` iterates all recorded references (values) and calls `FindNode`.
4.  **Link**: If found, `ref.Target` is set to the `ProjectNode`.

### Validation Lifecycle
1.  `mdt check` or LSP `didChange` triggers validation.
2.  LSP triggers are debounced (250ms) and queued.
3.  The validation worker ensures only one global validation runs at a time.
4.  If a new request for the same file URI arrives, the current validation for that URI is canceled via `context.Context`.
5.  A new `Validator` is created with the current `Tree`.
6.  `ValidateProject(ctx)` is called.
7.  The worker pool walks the tree, runs checks, and populates `Diagnostics`, frequently checking for cancellation.
8.  Diagnostics are printed (CLI) or published via `textDocument/publishDiagnostics` (LSP).

### Threading Check Logic
1.  Iterates all `RealTimeApplication` nodes found in the project.
2.  For each App:
    1.  Finds `States` and `Threads`.
    2.  For each Thread, resolves the `Functions` (GAMs).
    3.  For each GAM, resolves connected `DataSources` via Input/Output signals.
    4.  Maps `DataSource -> Thread` within the context of a State.
    5.  If a DataSource is seen in >1 Thread, it checks the `#meta.multithreaded` property. If false (default), an error is raised.

### INOUT Ordering Logic
1.  Iterates Threads.
2.  Iterates GAMs in execution order.
3.  Tracks `producedSignals` and `consumedSignals`.
4.  For each GAM, checks Inputs. If Input is `INOUT` (and not multithreaded) and not in `producedSignals`, reports "Consumed before Produced" error.
5.  Registers Outputs in `producedSignals`.
6.  At end of thread, checks for signals that were produced but never consumed, reporting a warning.
