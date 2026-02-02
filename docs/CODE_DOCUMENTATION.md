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
*   **ScanDirectory**: Recursively walks the project directory to find all `.marte` files, adding them to the tree even if they contain partial syntax errors.
*   **ProjectNode**: Represents a logical node in the configuration. Since a node can be defined across multiple files (fragments), `ProjectNode` aggregates these fragments. It also stores locally defined variables and constants in its `Variables` map.
*   **NodeMap**: A hash map index (`map[string][]*ProjectNode`) for $O(1)$ symbol lookups, optimizing `FindNode` operations.
*   **Reference Resolution**: The `ResolveReferences` method links `Reference` objects to their target `ProjectNode` or `VariableDefinition`. It uses `ResolveName` (exported) which respects lexical scoping rules by searching the hierarchy upwards from the reference's container, using `FindNode` for deep searches within each scope.

### 3. `internal/validator`

Ensures configuration correctness.

*   **Validator**: Iterates over the `ProjectTree` to check rules.
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
*   **Evaluation**: Implements a lightweight expression evaluator to show evaluated values in Hover and completion snippets.
*   **Incremental Sync**: Supports `textDocumentSync: 2`. `HandleDidChange` applies patches to the in-memory document buffers using `offsetAt` logic.
*   **Features**:
    *   `HandleCompletion`: Context-aware suggestions (Macros, Schema fields, Signal references, Class names).
    *   `HandleHover`: Shows documentation (including docstrings for variables), evaluated signal types/dimensions, and usage analysis.
    *   `HandleDefinition` / `HandleReferences`: specific lookup using the `index`.
    *   `HandleRename`: Project-wide renaming supporting objects, fields, and signals (including implicit ones).

### 5. `internal/builder`

Merges multiple MARTe files into a single output.

*   **Logic**: It parses all input files, builds a temporary `ProjectTree`, and then reconstructs the source code.
*   **Merging**: It interleaves fields and subnodes from different file fragments to produce a coherent single-file configuration, respecting the `#package` hierarchy.
*   **Evaluation**: Evaluates all expressions and variable references into concrete MARTe values in the final output. Prevents overrides of `#let` constants.

### 6. `internal/schema`

Manages CUE schemas.

*   **Loading**: Loads the embedded default schema (`marte.cue`) and merges it with any user-provided `.marte_schema.cue`.
*   **Metadata**: Handles the `#meta` field in schemas to extract properties like `direction` and `multithreaded` support for the validator.

## Key Data Flows

### Reference Resolution
1.  **Scan**: Files are parsed and added to the `ProjectTree`.
2.  **Index**: `RebuildIndex` populates `NodeMap`.
3.  **Resolve**: `ResolveReferences` iterates all recorded references (values) and calls `FindNode`.
4.  **Link**: If found, `ref.Target` is set to the `ProjectNode`.

### Validation Lifecycle
1.  `mdt check` or LSP `didChange` triggers validation.
2.  A new `Validator` is created with the current `Tree`.
3.  `ValidateProject` is called.
4.  It walks the tree, runs checks, and populates `Diagnostics`.
5.  Diagnostics are printed (CLI) or published via `textDocument/publishDiagnostics` (LSP).

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
