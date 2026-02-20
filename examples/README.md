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

### Logic and Templates

The `advanced_features.marte` file demonstrates the use of conditional blocks, loops, and reusable templates to create dynamic configurations.

Key features shown:
- `#template` and `#use` for component reuse.
- `#if` / `#else` for conditional logic.
- `#foreach` for bulk instantiation of objects.
- Expression-based dynamic node names.

**Try it:**
```bash
# Check the advanced configuration
./build/mdt check examples/advanced_features.marte

# Build the configuration to see the generated output
./build/mdt build examples/advanced_features.marte
```

### Advanced Features (Logic & Templates)

The tool supports dynamic configuration generation:

**Templates:**
```marte
#template MyDevice(ID: int, Type: string = "Default")
    "+Device_" .. @ID = {
        Class = "MyDriver"
        Type = @Type
        Address = 0x100 + @ID
    }
#end

+Hardware = {
    Class = ReferenceContainer
    #use MyDevice Dev1 (ID = 1)
    #use MyDevice Dev2 (ID = 2, Type = "Special")
}
```

**Loops & Conditionals:**
```marte
#var Channels: array = { 1 2 3 }
#var EnableLog: bool = true

+DAQ = {
    Class = ReferenceContainer
    #foreach Ch in $Channels
        "+Channel_" .. @Ch = {
            Class = ADCChannel
            Index = @Ch
        }
    #end
    
    #if $EnableLog
        Logger = { Class = FileLogger }
    #end
}
```
