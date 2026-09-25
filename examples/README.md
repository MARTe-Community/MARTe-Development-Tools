# Examples

This directory contains example projects demonstrating different features and usage patterns of `mdt`.

## Directory Structure

```
examples/
  simple/           # A basic, single-file application
  complex/          # A multi-file project with custom schema
  complex_func_list/# Multi-file project with package-routed functions/states
  big_project/      # A large real-world-style project
  syntax_showcase/  # Fully validated demo of the complete extended syntax
  advanced_features.marte  # Historical template/loop demo (superseded)
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
- **Namespaces**: Use of `package` to organize nodes.
- **Custom Schema**: `.marte_schema.cue` defines a custom class (`CustomController`) with specific metadata (`#meta.multithreaded`).
- **Validation**: Enforces strict typing and custom rules.

**Run:**
```bash
cd complex
make check
make build
```

### Logic and Templates

The `advanced_features.marte` file demonstrates the use of conditional blocks, loops, and reusable templates to create dynamic configurations.

Key features shown:
- `template` and `use` for component reuse.
- `if` / `else` / `else if` for conditional logic.
- `foreach` for bulk instantiation of objects.
- Expression-based dynamic node names.

**Try it:**
```bash
# Check the advanced configuration
./build/mdt check examples/advanced_features.marte

# Build the configuration to see the generated output
./build/mdt build examples/advanced_features.marte
```

### Syntax Showcase (Logic & Templates)

`syntax_showcase/` is the validated, complete demonstration of the
extended syntax: variables, literals, expressions, conditionals, loops,
templates, signal shorthand, pragmas and multi-file package routing.

**Templates:**
```marte
template MyDevice(ID: int, Gain: float = 1.0)
  ("+Device_" .. @ID) = {
    Class = MyDriver
    Gain = @Gain
    Address = (0x100 + @ID)
  }
end

+Hardware = {
  Class = ReferenceContainer
  use MyDevice Dev1(ID = 1)
  use MyDevice Dev2(ID = 2, Gain = 2.5)
}
```

**Loops & Conditionals:**
```marte
var EnableLog: bool = true

+DAQ = {
  Class = ReferenceContainer
  foreach Ch in { 1, 2, 3 }
    ("+Channel_" .. @Ch) = {
      Class = ADCChannel
      Index = @Ch
    }
  end

  if @EnableLog
    Logger = { Class = FileLogger }
  end
}
```

See [`syntax_showcase/README.md`](syntax_showcase/README.md) for the
exact validation commands, and
[`../docs/LANGUAGE_REFERENCE.md`](../docs/LANGUAGE_REFERENCE.md) for the
full language reference.
