# Examples

This directory contains example projects demonstrating different features and usage patterns of `mdt`.

## Directory Structure

```
examples/
  simple/           # A basic, single-file application
  complex/          # A multi-file project with custom schema
  README.md         # This file
```

## Running Examples

Prerequisite: `mdt` must be built (or installed). The Makefiles in the examples assume `mdt` is available at `../../build/mdt`.

### Simple Project

Demonstrates a minimal setup:
- Single `main.marte` file.
- Basic Thread and GAM definition.

**Run:**
```bash
cd simple
make check
make build
```

### Complex Project

Demonstrates advanced features:
- **Multi-file Structure**: `src/app.marte` (Logic) and `src/components.marte` (Data).
- **Namespaces**: Use of `#package` to organize nodes.
- **Custom Schema**: `.marte_schema.cue` defines a custom class (`CustomController`) with specific metadata (`#meta.multithreaded`).
- **Validation**: Enforces strict typing and custom rules.

**Run:**
```bash
cd complex
make check
make build
```
