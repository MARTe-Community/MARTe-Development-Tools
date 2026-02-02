# MARTe Development Tools

## Project goal

- A single portable executable named `mdt` (MARTe Development Tools) written in _Go_
- It should parse and index a custom configuration language (MARTe)
- It should provide an LSP server for this language
- It should provide a build tool for unifying multi-file projects into a single configuration output

## CLI Commands

The executable should support the following subcommands:

- `lsp`: Starts the Language Server Protocol server.
- `build`: Merges files with the same base namespace into a single output.
- `check`: Runs diagnostics and validations on the configuration files.
- `fmt`: Formats the configuration files.

## LSP Features

The LSP server should provide the following capabilities:

- **Diagnostics**: Report syntax errors and validation issues.
- **Incremental Sync**: Supports `textDocumentSync` kind 2 (Incremental) for better performance with large files.
- **Hover Documentation**:
  - **Objects**: Display `CLASS::Name` and any associated docstrings.
  - **Signals**: Display `DataSource.Name TYPE (SIZE) [IN/OUT/INOUT]` along with docstrings.
  - **GAMs**: Show the list of States where the GAM is referenced.
  - **Referenced Signals**: Show the list of GAMs where the signal is referenced (indicating Input/Output direction).
- **Go to Definition**: Jump to the definition of a reference, supporting navigation across any file in the current project.
- **Go to References**: Find usages of a node or field, supporting navigation across any file in the current project.
- **Code Completion**: Autocomplete fields, values, and references.
  - **Context-Aware**: Suggestions depend on the cursor position (e.g., inside an object, assigning a value).
  - **Schema-Driven**: Field suggestions are derived from the CUE schema for the current object's Class, indicating mandatory vs. optional fields.
  - **Reference Suggestions**:
    - `DataSource` fields suggest available DataSource objects.
    - `Functions` (in Threads) suggest available GAM objects.
  - **Signal Completion**: Inside `InputSignals` or `OutputSignals` of a GAM:
    - Suggests available signals from valid DataSources (filtering by direction: `IN`/`INOUT` for Inputs, `OUT`/`INOUT` for Outputs).
    - Format: `SIGNAL_NAME:DATASOURCE_NAME`.
    - Auto-inserts: `SIGNAL_NAME = { DataSource = DATASOURCE_NAME }`.
- **Rename Symbol**: Rename an object, field, or reference across the entire project scope.
  - Supports renaming of Definitions (`+Name` or `Name`), preserving any modifiers (`+`/`$`).
  - Updates all references to the renamed symbol, including qualified references (e.g., `Pkg.Name`).
- **Code Snippets**: Provide snippets for common patterns (e.g., `+Object = { ... }`).
- **Formatting**: Format the document using the same rules and engine as the `fmt` command.

## Build System & File Structure

- **File Extension**: `.marte`
- **Project Structure**: Files can be distributed across sub-folders.
- **Namespaces**: The `#package` macro defines the namespace for the file.
  - **Single File Context**: If no `#package` is defined in a file, the LSP, build tool, and validator must consider **only** that file (no project-wide merging or referencing).
  - **Semantic**: `#package PROJECT_NAME.SUB_URI` implies that:
    - `PROJECT_NAME` is a namespace identifier used to group files from the same project. It does **not** create a node in the configuration tree.
    - `SUB_URI` defines the path of nodes where the file's definitions are placed. All definitions within the file are treated as children/fields of the node defined by `SUB_URI`.
  - **URI Symbols**: The symbols `+` and `$` used for object nodes are **not** written in the URI of the `#package` macro (e.g., use `PROJECT.NODE` even if the node is defined as `+NODE`).
- **Build Process**:
  - The build tool merges all files sharing the same base namespace into a **single output configuration**.
  - **Namespace Consistency**: The build tool must verify that all input files belong to the same project namespace (the first segment of the `#package` URI). If multiple project namespaces are detected, the build must fail with an error.
  - **Target**: The build output is written to standard output (`stdout`) by default. It can be written to a target file if the `-o` (or `--output`) argument is provided via CLI.
  - **Multi-File Definitions**: Nodes and objects can be defined across multiple files. The build tool, validator, and LSP must merge these definitions (including all fields and sub-nodes) from the entire project to create a unified view before processing or validating.
  - **Global References**: References to nodes, signals, or objects can point to definitions located in any file within the project. Support for dot-separated paths (e.g., `Node.SubNode`) is required.
  - **Merging Order**: For objects defined across multiple files, definitions are merged. The build tool must preserve the relative order of fields and sub-nodes as they appear in the source files, interleaving them correctly in the final output.
  - **Field Order**: Within a single file (and across merged files), the relative order of defined fields must be maintained in the output.
  - The LSP indexes only files belonging to the same project/namespace scope.
- **Output**: The output format is the same as the input configuration but without the `#package` macro.

## MARTe Configuration Language

### Grammar

- `comment` : `//.*`
- `configuration`: `(definition | macro)+`
- `definition`: `field = value | node = subnode`
- `macro`: `package | variable | constant`
- `field`: `[a-zA-Z][a-zA-Z0-9_\-]*`
- `node`: `[+$][a-zA-Z][a-zA-Z0-9_\-]*`
- `subnode`: `{ (definition | macro)+ }`
- `value`: `expression`
- `expression`: `atom | binary_expr | unary_expr`
- `atom`: `string | int | float | bool | reference | array | "(" expression ")"`
- `binary_expr`: `expression operator expression`
- `unary_expr`: `unary_operator expression`
- `operator`: `+ | - | * | / | % | & | | | ^ | ..`
- `unary_operator`: `- | !`
- `int`: `/-?[0-9]+|0b[01]+|0x[0-9a-fA-F]+`
- `float`: `-?[0-9]+\.[0-9]+|-?[0-9]+\.?[0-9]*[eE][+-]?[0-9]+`
- `bool`: `true|false`
- `string`: `".*"`
- `reference` : `[a-zA-Z][a-zA-Z0-9_\-\.]* | @[a-zA-Z0-9_]+ | $[a-zA-Z0-9_]+`
- `array`: `{ (value | ",")* }`

#### Extended grammar

- `package` : `#package URI`
- `variable`: `#var NAME: TYPE [= expression]`
- `constant`: `#let NAME: TYPE = expression`
- `URI`: `PROJECT | PROJECT.PRJ_SUB_URI`
- `PRJ_SUB_URI`: `NODE | NODE.PRJ_SUB_URI`
- `docstring` : `//#.*`
- `pragma`: `//!.*`

### Semantics

- **Nodes (`+` / `$`)**: The prefixes `+` and `$` indicate that the node represents an object.
  - **Constraint**: These nodes _must_ contain a field named `Class` within their subnode definition (across all files where the node is defined).
- **Signals**: Signals are considered nodes but **not** objects. They do not require a `Class` field.
- **Variables (`#var`)**: Define overrideable parameters. Can be overridden via CLI (`-vVAR=VAL`).
- **Constants (`#let`)**: Define fixed parameters. **Cannot** be overridden externally. Must have an initial value.
- **Expressions**: Evaluated during build and displayed evaluated in LSP hover documentation.
- **Docstrings (`//#`)**: Associated with the following definition (Node, Field, Variable, or Constant).
- **Pragmas (`//!`)**: Used to suppress specific diagnostics. The developer can use these to explain why a rule is being ignored. Supported pragmas:
  - `//!unused: REASON` or `//!ignore(unused): REASON` - Suppress "Unused GAM" or "Unused Signal" warnings.
  - `//!implicit: REASON` or `//!ignore(implicit): REASON` - Suppress "Implicitly Defined Signal" warnings.
  - `//!allow(WARNING_TYPE): REASON` or `//!ignore(WARNING_TYPE): REASON` - Global suppression for a specific warning type across the whole project (supported: `unused`, `implicit`, `not_consumed`, `not_produced`).
  - `//!cast(DEF_TYPE, CUR_TYPE): REASON` - Suppress "Type Inconsistency" errors if types match.
- **Structure**: A configuration is composed by one or more definitions or macros.
- **Strictness**: Any content that is not a valid comment (or pragma/docstring) or a valid definition/macro is **not allowed** and must generate a parsing error.

### Core MARTe Classes

MARTe configurations typically involve several main categories of objects:

- **State Machine (`StateMachine`)**: Defines state machines and transition logic.
- **Real-Time Application (`RealTimeApplication`)**: Defines a real-time application, including its data sources, functions, states, and scheduler.
- **Data Source**: Multiple classes used to define input and/or output signal sources.
- **GAM (Generic Application Module)**: Multiple classes used to process signals.
  - **Constraint**: A GAM node must contain at least one `InputSignals` sub-node, one `OutputSignals` sub-node, or both.

### Signals and Data Flow

- **Signal Definition**:
  - **Explicit**: Signals defined within the `DataSource` definition.
  - **Implicit**: Signals defined only within a `GAM`, which are then automatically managed.
  - **Requirements**:
    - All signal definitions **must** include a `Type` field with a valid value.
    - **Size Information**: Signals can optionally include `NumberOfDimensions` and `NumberOfElements` fields. If not explicitly defined, these default to `1`.
    - **Property Matching**: Signal references in GAMs must match the properties (`Type`, `NumberOfElements`, `NumberOfDimensions`) of the defined signal in the `DataSource`.
    - **Consistency**: Implicit signals used across different GAMs must share the same `Type` and size properties.
    - **Extensibility**: Signal definitions can include additional fields as required by the specific application context.
- **Signal Reference Syntax**:
  - Signals are referenced or defined in `InputSignals` or `OutputSignals` sub-nodes using one of the following formats:
    1.  **Direct Reference (Option 1)**:
        ```
        SIGNAL_NAME = {
            DataSource = DATASOURCE_NAME
            // Other fields if necessary
        }
        ```
        In this case, the GAM signal name is the same as the DataSource signal name.
    2.  **Aliased Reference (Option 2)**:
        ```
        GAM_SIGNAL_NAME = {
            Alias = SIGNAL_NAME
            DataSource = DATASOURCE_NAME
            // ...
        }
        ```
        In this case, `Alias` points to the DataSource signal name.
  - **Implicit Definition Constraint**: If a signal is implicitly defined within a GAM, the `Type` field **must** be present in the reference block to define the signal's properties.
- **Renaming**: Renaming a signal (explicit or implicit) via LSP updates all its usages across all GAMs and DataSources in the project. Local aliases (`Alias = Name`) are preserved while their targets are updated.
- **Directionality**: DataSources and their signals are directional:
  - `Input` (IN): Only providing data. Signals can only be used in `InputSignals`.
  - `Output` (OUT): Only receiving data. Signals can only be used in `OutputSignals`.
  - `Inout` (INOUT): Bidirectional data flow. Signals can be used in both `InputSignals` and `OutputSignals`.
  - **Validation**: The tool must validate that signal usage in GAMs respects the direction of the referenced DataSource.

### Object Indexing & References

The tool must build an index of the configuration to support LSP features and validations:

- **Recursive Indexing**: All `.marte` files in the project root and subdirectories are indexed automatically.
- **GAMs**: Referenced in `$APPLICATION.States.$STATE_NAME.Threads.$THREAD_NAME.Functions` (where `$APPLICATION` is a `RealTimeApplication` node).
- **Signals**: Referenced within the `InputSignals` and `OutputSignals` sub-nodes of a GAM.
- **DataSources**: Referenced within the `DataSource` field of a signal reference/definition.
- **Variables/Constants**: Referenced via `@NAME` or `$NAME` in expressions.
- **General References**: Objects can also be referenced in other fields (e.g., as targets for messages).

### Validation Rules

- **Consistency**: The `lsp`, `check`, and `build` commands **must share the same validation engine** to ensure consistent results across all tools.
- **Global Validation Context**:
  - All validation steps must operate on the aggregated view of the project.
  - A node's validity is determined by the combination of all its fields and sub-nodes defined across all project files.
- **Class Validation**:
  - For each known `Class`, the validator checks:
    - **Mandatory Fields**: Verification that all required fields are present.
    - **Field Types**: Verification that values assigned to fields match the expected types (e.g., `int`, `string`, `bool`).
    - **Field Order**: Verification that specific fields appear in a prescribed order when required by the class definition.
    - **Conditional Fields**: Validation of fields whose presence or value depends on the values of other fields within the same node or context.
  - **Schema Definition**:
    - Class validation rules must be defined in a separate schema file using the **CUE** language.
    - **Metadata**: Class properties like direction (`#direction`) and multithreading support (`#multithreaded`) are stored within a `#meta` field in the class definition (e.g., `#meta: { direction: "IN", multithreaded: true }`).
    - **Project-Specific Classes**: Developers can define their own project-specific classes and corresponding validation rules, expanding the validation capabilities for their specific needs.
  - **Schema Loading**:
    - **Default Schema**: The tool should look for a default schema file `marte_schema.cue` in standard system locations:
      - `/usr/share/mdt/marte_schema.cue`
      - `$HOME/.local/share/mdt/marte_schema.cue`
    - **Project Schema**: If a file named `.marte_schema.cue` exists in the project root, it must be loaded.
    - **Merging**: The final schema is a merge of the built-in schema, the system default schema (if found), and the project-specific schema. Rules in later sources (Project > System > Built-in) append to or override earlier ones.
- **Duplicate Fields**:
  - **Constraint**: A field must not be defined more than once within the same object/node scope, even if those definitions are spread across different files.
  - **Multi-File Consideration**: Validation must account for nodes being defined across multiple files (merged) when checking for duplicates.

### Formatting Rules

The `fmt` command must format the code according to the following rules:

- **Indentation**: 2 spaces per indentation level.
- **Assignment**: 1 space before and after the `=` operator (e.g., `Field = Value`).
- **Comments**:
  - 1 space after `//`, `//#`, or `//!`.
  - Comments should "stick" to the next definition (no empty lines between the comment and the code it documents).
  - **Placement**:
    - Comments can be placed inline after a definition (e.g., `field = value // comment`).
    - Comments can be placed after a subnode opening bracket (e.g., `node = { // comment`) or after an object definition.
- **Arrays**: 1 space after the opening bracket `{` and 1 space before the closing bracket `}` (e.g., `{ 1 2 3 }`).
- **Strings**: Quoted strings must preserve their quotes during formatting.

### Diagnostic Messages

The LSP and `check` command should report the following:

- **Warnings**:
  - **Unused GAM**: A GAM is defined but not referenced in any thread or scheduler. (Suppress with `//!unused`)
  - **Unused Signal**: A signal is explicitly defined in a `DataSource` but never referenced in any `GAM`. (Suppress with `//!unused`)
  - **Implicitly Defined Signal**: A signal is defined only within a `GAM` and not in its parent `DataSource`. (Suppress with `//!implicit`)

- **Errors**:
  - **Type Inconsistency**: A signal is referenced with a type different from its definition. (Suppress with `//!cast`)
  - **Size Inconsistency**: A signal is referenced with a size (dimensions/elements) different from its definition.
  - **Invalid Signal Content**: The `Signals` container of a `DataSource` contains invalid elements (e.g., fields instead of nodes).
  - **Duplicate Field Definition**: A field is defined multiple times within the same node scope (including across multiple files).
  - **Validation Errors**:
    - Missing mandatory fields.
    - Field type mismatches.
    - Grammar errors (e.g., missing closing brackets).
    - **Invalid Function Reference**: Elements in the `Functions` array of a `State.Thread` must be valid references to defined GAM nodes.
  - **Threading Violation**: A DataSource that is not marked as multithreaded (via `#meta.multithreaded`) is used by GAMs running in different threads within the same State.

## Logging

- **Requirement**: All logs must be managed through a centralized logger.
- **Output**: Logs should be written to `stderr` by default to avoid interfering with `stdout` which might be used for CLI output (e.g., build artifacts or formatted text).
