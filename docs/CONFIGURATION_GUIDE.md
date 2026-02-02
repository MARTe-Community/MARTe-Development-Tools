# MARTe Configuration Guide

This guide explains the syntax, features, and best practices for writing MARTe configurations using `mdt`.

## 1. Syntax Overview

MARTe configurations use a hierarchical object-oriented syntax.

### Objects (Nodes)
Objects are defined using `+` (public/instantiated) or `$` (template/class-like) prefixes. Every object **must** have a `Class` field.

```marte
+MyObject = {
    Class = MyClass
    Field1 = 100
    Field2 = "Hello"
}
```

### Fields and Values
- **Fields**: Alphanumeric identifiers (e.g., `Timeout`, `CycleTime`).
- **Values**:
  - Integers: `10`, `-5`, `0xFA`, `0b1011`
  - Floats: `3.14`, `1e-3`
  - Strings: `"Text"`
  - Booleans: `true`, `false`
  - References: `MyObject`, `MyObject.SubNode`
  - Arrays: `{ 1 2 3 }` or `{ "A" "B" }`

### Comments and Documentation
- Line comments: `// This is a comment`
- Docstrings: `//# This documents the following node`. These appear in hover tooltips.

```marte
//# This is the main application
+App = { ... }
```

## 2. Signals and Data Flow

Signals define how data moves between DataSources (drivers) and GAMs (algorithms).

### Defining Signals
Signals are typically defined in a `DataSource`. They must have a `Type`.

```marte
+MyDataSource = {
    Class = GAMDataSource
    Signals = {
        Signal1 = { Type = uint32 }
        Signal2 = { Type = float32 }
    }
}
```

### Using Signals in GAMs
GAMs declare inputs and outputs. You can refer to signals directly or alias them.

```marte
+MyGAM = {
    Class = IOGAM
    InputSignals = {
        Signal1 = {
            DataSource = MyDataSource
            Type = uint32 // Must match DataSource definition
        }
        MyAlias = {
            Alias = Signal2
            DataSource = MyDataSource
            Type = float32
        }
    }
}
```

### Threading Rules
**Validation Rule**: A DataSource that is **not** marked as multithreaded (default) cannot be used by GAMs running in different threads within the same State.

**Ordering Rule**: For `INOUT` signals (data dependency within a thread), the Producer GAM must appear **before** the Consumer GAM in the thread's `Functions` list. This ensures correct data flow within the cycle. This rule is skipped if the DataSource is marked as `multithreaded: true`.

To allow sharing, the DataSource class in the schema must have `#meta: multithreaded: true`.

## 3. Schemas and Validation

`mdt` validates your configuration against CUE schemas.

### Built-in Schema
Common classes (`RealTimeApplication`, `StateMachine`, `IOGAM`, etc.) are built-in.

### Custom Schemas
You can extend the schema by creating a `.marte_schema.cue` file in your project root.

**Example: Adding a custom GAM**

```cue
package schema

#Classes: {
    MyCustomGAM: {
        // Metadata for Validator/LSP
        #meta: {
            direction: "INOUT" // "IN", "OUT", "INOUT"
            multithreaded: false
        }
        
        // Fields
        Gain: float
        Offset?: float // Optional
        InputSignals: {...}
        OutputSignals: {...}
    }
}
```

## 4. Variables and Constants

You can define variables to parameterize your configuration.

### Variables (`#var`)
Variables can be defined at any level and can be overridden externally (e.g., via CLI).

```marte
//# Default timeout
#var Timeout: uint32 = 100

+MyObject = {
    Class = Timer
    Timeout = @Timeout
}
```

### Constants (`#let`)
Constants are like variables but **cannot** be overridden externally. They are ideal for internal calculations or fixed parameters.

```marte
//# Sampling period
#let Ts: float64 = 0.001

+Clock = {
    Class = HighResClock
    Period = @Ts
}
```

### Reference Syntax
Reference a variable or constant using `$` or `@`:

```marte
Field = $MyVar
// or
Field = @MyVar
```

### Expressions
You can use operators in field values. Supported operators:
- **Math**: `+`, `-`, `*`, `/`, `%`, `^` (XOR), `&`, `|` (Bitwise)
- **String Concatenation**: `..`
- **Parentheses**: `(...)` for grouping

```marte
Field1 = 10 + 20 * 2  // 50
Field2 = "Hello " .. "World"
Field3 = ($MyVar + 5) * 2
```

### Build Override
You can override variable values during build (only for `#var`):

```bash
mdt build -vMyVar=200 src/*.marte
```

### Docstrings
Docstrings (`//#`) work for variables and constants and are displayed in the LSP hover information.

## 5. Multi-file Projects

You can split your configuration into multiple files.

### Namespaces
Use `#package` to define where the file's content fits in the hierarchy.

**file1.marte**
```marte
#package MyApp.Controller
+MyController = { ... }
```

This places `MyController` under `MyApp.Controller`.

### Building
The `build` command merges all files.

```bash
mdt build -o final.marte src/*.marte
```

## 6. Pragmas (Suppressing Warnings)

If validation is too strict, you can suppress warnings using pragmas (`//!`).

- **Suppress Unused Warning**:
  ```marte
  +MyGAM = {
      Class = IOGAM
      //! ignore(unused): This GAM is triggered externally
  }
  ```

- **Suppress Implicit Signal Warning**:
  ```marte
  InputSignals = {
      //! ignore(implicit)
      ImplicitSig = { Type = uint32 }
  }
  ```

- **Type Casting**:
  ```marte
  Sig1 = {
      //! cast(uint32, int32): Intentional mismatch
      DataSource = DS
      Type = int32
  }
  ```

- **Global Suppression**:
  ```marte
  //! allow(unused)
  //! allow(implicit)
  ```

## 7. Validation Rules (Detail)

### Data Flow Validation
`mdt` checks for logical data flow errors:
- **Consumed before Produced**: If a GAM reads an INOUT signal that hasn't been written by a previous GAM in the same cycle, an error is reported.
- **Produced but not Consumed**: If a GAM writes an INOUT signal that is never read by subsequent GAMs, a warning is reported.
- **Initialization**: Providing a `Value` field in an `InputSignal` treats it as "produced" (initialized), resolving "Consumed before Produced" errors.

### Threading Rules
A DataSource that is **not** marked as multithreaded (default) cannot be used by GAMs running in different threads within the same State.

To allow sharing, the DataSource class in the schema must have `#meta: multithreaded: true`.

### Implicit vs Explicit Signals
- **Explicit**: Signal defined in `DataSource.Signals`.
- **Implicit**: Signal used in GAM but not defined in DataSource. `mdt` reports a warning unless suppressed.
- **Consistency**: All references to the same logical signal (same name in same DataSource) must share the same `Type` and size properties.