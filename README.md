# MARTe Development Tools (mdt)

`mdt` is a comprehensive toolkit for developing, validating, and building configurations for the MARTe real-time framework. It provides a CLI and a Language Server Protocol (LSP) server to enhance the development experience.

## Features

- **LSP Server**: Real-time syntax checking, validation, autocomplete, hover documentation, and navigation (Go to Definition/References).
- **Builder**: Merges multiple configuration files into a single, ordered output file.
- **Formatter**: Standardizes configuration file formatting.
- **Validator**: Advanced semantic validation using [CUE](https://cuelang.org/) schemas, ensuring type safety and structural correctness.

## Installation

### From Source

Requirements: Go 1.21+

```bash
go install github.com/marte-community/marte-dev-tools/cmd/mdt@latest
```

## Usage

### CLI Commands

- **Check**: Run validation on a file or project.
  ```bash
  mdt check path/to/project
  ```
- **Build**: Merge project files into a single output.
  ```bash
  mdt build -o output.marte main.marte
  ```
- **Format**: Format configuration files.
  ```bash
  mdt fmt path/to/file.marte
  ```
- **LSP**: Start the language server (used by editor plugins).
  ```bash
  mdt lsp
  ```

### Editor Integration

`mdt lsp` implements the Language Server Protocol. You can use it with any LSP-compatible editor (VS Code, Neovim, Emacs, etc.).

## MARTe Configuration

The tools support the MARTe configuration format with extended features:
- **Objects**: `+Node = { Class = ... }`
- **Signals**: `Signal = { Type = ... }`
- **Namespaces**: `#package PROJECT.NODE` for organizing multi-file projects.

### Validation & Schema

Validation is fully schema-driven using CUE.

- **Built-in Schema**: Covers standard MARTe classes (`StateMachine`, `GAM`, `DataSource`, `RealTimeApplication`, etc.).
- **Custom Schema**: Add a `.marte_schema.cue` file to your project root to extend or override definitions.

**Example `.marte_schema.cue`:**
```cue
package schema

#Classes: {
    MyCustomGAM: {
        Param1: int
        Param2?: string
        ...
    }
}
```

### Pragmas (Suppressing Warnings)

Use comments starting with `//!` to control validation behavior:

- `//!unused: Reason` - Suppress "Unused GAM" or "Unused Signal" warnings.
- `//!implicit: Reason` - Suppress "Implicitly Defined Signal" warnings.
- `//!cast(DefinedType, UsageType)` - Allow type mismatch between definition and usage (e.g. `//!cast(uint32, int32)`).
- `//!allow(unused)` - Global suppression for the file.

## Development

### Building
```bash
go build ./cmd/mdt
```

### Running Tests
```bash
go test ./...
```

## License
MIT
