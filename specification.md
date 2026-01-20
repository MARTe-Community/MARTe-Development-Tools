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
- **Hover Documentation**:
  - **Objects**: Display `CLASS::Name` and any associated docstrings.
  - **Signals**: Display `DataSource.Name TYPE (SIZE) [IN/OUT/INOUT]` along with docstrings.
  - **GAMs**: Show the list of States where the GAM is referenced.
  - **Referenced Signals**: Show the list of GAMs where the signal is referenced.
- **Go to Definition**: Jump to the definition of a reference, supporting navigation across any file in the current project.
- **Go to References**: Find usages of a node or field, supporting navigation across any file in the current project.
- **Code Completion**: Autocomplete fields, values, and references.
- **Code Snippets**: Provide snippets for common patterns.

## Build System & File Structure

- **File Extension**: `.marte`
- **Project Structure**: Files can be distributed across sub-folders.
- **Namespaces**: The `#package` macro defines the namespace for the file.
  - **Semantic**: `#package PROJECT.NODE` implies that all definitions within the file are treated as children/fields of the node `NODE`.
  - **URI Symbols**: The symbols `+` and `$` used for object nodes are **not** written in the URI of the `#package` macro (e.g., use `PROJECT.NODE` even if the node is defined as `+NODE`).
- **Build Process**:
  - The build tool merges all files sharing the same base namespace.
  - **Multi-File Nodes**: Nodes can be defined across multiple files. The build tool and validator must merge these definitions before processing.
  - **Merging Order**: For objects defined across multiple files, the **first file** to be considered is the one containing the `Class` field definition.
  - **Field Order**: Within a single file, the relative order of defined fields must be maintained.
  - The LSP indexes only files belonging to the same project/namespace scope.
- **Output**: The output format is the same as the input configuration but without the `#package` macro.

## MARTe Configuration Language

### Grammar

- `comment` : `//.*`
- `configuration`: `definition+`
- `definition`: `field = value | node = subnode`
- `field`: `[a-zA-Z][a-zA-Z0-9_\-]*`
- `node`: `[+$][a-zA-Z][a-zA-Z0-9_\-]*`
- `subnode`: `{ definition+ }`
- `value`: `string|int|float|bool|reference|array`
- `int`: `/-?[0-9]+|0b[01]+|0x[0-9a-fA-F]+`
- `float`: `-?[0-9]+\.[0-9]+|-?[0-9]+\.?[0-9]*e\-?[0-9]+`
- `bool`: `true|false`
- `string`: `".*"`
- `reference` : `string|.*`
- `array`: `{ value }`

#### Extended grammar

- `package` : `#package URI`
- `URI`: `PROJECT | PROJECT.PRJ_SUB_URI`
- `PRJ_SUB_URI`: `NODE | NODE.PRJ_SUB_URI`
- `docstring` : `//#.*`
- `pragma`: `//!.*`

### Semantics

- **Nodes (`+` / `$`)**: The prefixes `+` and `$` indicate that the node represents an object.
  - **Constraint**: These nodes _must_ contain a field named `Class` within their subnode definition.
- **Signals**: Signals are considered nodes but **not** objects. They do not require a `Class` field.
- **Pragmas (`//!`)**: Used to suppress specific diagnostics. The developer can use these to explain why a rule is being ignored.
- **Structure**: A configuration is composed by one or more definitions.

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
    - **Extensibility**: Signal definitions can include additional fields as required by the specific application context.
- **Signal Reference Syntax**:
  - Signals are referenced or defined in `InputSignals` or `OutputSignals` sub-nodes using one of the following formats:
    1.  **Direct Reference**:
        ```
        SIGNAL_NAME = {
            DataSource = SIGNAL_DATASOURCE
            // Other fields if necessary
        }
        ```
    2.  **Aliased Reference**:
        ```
        NAME = {
            Alias = SIGNAL_NAME
            DataSource = SIGNAL_DATASOURCE
            // ...
        }
        ```
  - **Implicit Definition Constraint**: If a signal is implicitly defined within a GAM, the `Type` field **must** be present in the reference block to define the signal's properties.
- **Directionality**: DataSources and their signals are directional:
  - `Input`: Only providing data.
  - `Output`: Only receiving data.
  - `Inout`: Bidirectional data flow.

### Object Indexing & References

The tool must build an index of the configuration to support LSP features and validations:

- **GAMs**: Referenced in `$APPLICATION.States.$STATE_NAME.Threads.$THREAD_NAME.Functions` (where `$APPLICATION` is a `RealTimeApplication` node).
- **Signals**: Referenced within the `InputSignals` and `OutputSignals` sub-nodes of a GAM.
- **DataSources**: Referenced within the `DataSource` field of a signal reference/definition.
- **General References**: Objects can also be referenced in other fields (e.g., as targets for messages).

### Validation Rules

- **Consistency**: The `lsp`, `check`, and `build` commands **must share the same validation engine** to ensure consistent results across all tools.
- **Class Validation**:
  - For each known `Class`, the validator checks:
    - **Mandatory Fields**: Verification that all required fields are present.
    - **Field Types**: Verification that values assigned to fields match the expected types (e.g., `int`, `string`, `bool`).
    - **Field Order**: Verification that specific fields appear in a prescribed order when required by the class definition.
    - **Conditional Fields**: Validation of fields whose presence or value depends on the values of other fields within the same node or context.
  - **Schema Definition**:
    - Class validation rules must be defined in a separate schema file.
    - **Project-Specific Classes**: Developers can define their own project-specific classes and corresponding validation rules, expanding the validation capabilities for their specific needs.
- **Duplicate Fields**:
  - **Constraint**: A field must not be defined more than once within the same object/node scope.
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
  - **Unused GAM**: A GAM is defined but not referenced in any thread or scheduler.
  - **Unused Signal**: A signal is explicitly defined in a `DataSource` but never referenced in any `GAM`.
  - **Implicitly Defined Signal**: A signal is defined only within a `GAM` and not in its parent `DataSource`.

- **Errors**:
  - **Type Inconsistency**: A signal is referenced with a type different from its definition.
  - **Size Inconsistency**: A signal is referenced with a size (dimensions/elements) different from its definition.
  - **Duplicate Field Definition**: A field is defined multiple times within the same node scope (including across multiple files).
  - **Validation Errors**:
    - Missing mandatory fields.
    - Field type mismatches.
    - Grammar errors (e.g., missing closing brackets).
