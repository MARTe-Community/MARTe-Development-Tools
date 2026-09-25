# MARTe Extended Configuration Language Reference

Complete reference for the `.marte` configuration language as implemented
by `mdt`. Every construct shown here is exercised by a validated example in
[`examples/syntax_showcase/`](../examples/syntax_showcase/) — the commands
to reproduce the validation are listed there and at the end of this file.

Other documentation: [Configuration Guide](CONFIGURATION_GUIDE.md) covers
workflows, schemas, validation rules and editor integration; this file is
the syntax reference.

---

## 1. Files and packages

A `.marte` file may start with a package declaration:

```marte
#package MyApp.RTApp.Functions
```

- The `#` is optional: `package X` and `#package X` are equivalent. All
  directive keywords accept both forms.
- Package URIs are dot-separated identifiers and double as **placement
  paths**: a file's objects are attached to the node addressed by its
  package URI. `Showcase.RTApp.Functions` places top-level objects inside
  the `Functions` container of the application defined at `Showcase`
  (see `examples/syntax_showcase/{app,functions}.marte`).
- Variables defined at a package's root scope are visible in files of
  nested package scopes (`Showcase` variables are visible in
  `Showcase.RTApp.*` files).
- Two files must not define the same object name in the same scope.

## 2. Objects and fields

```marte
+Timer = {            // '+' object: instantiated node
  Class = LinuxTimer  // every object must have a Class field
  Frequency = 100     // field
}

$RtApp = {            // '$' object: template/class-like root
  Class = RealTimeApplication
  +Data = { Class = ReferenceContainer }
}
```

- `+Name` — public, instantiated object. `$Name` — template/class-like
  definition (mdt build emits the contents of the `$` root the project
  declares, e.g. `$RtApp`).
- Field names are bare identifiers; values are expressions (section 5).
- **Dynamic object names** — any expression evaluating to a string can
  name an object or field. Parenthesise it:

  ```marte
  ("+Channel" .. @ID) = {
    Class = ReferenceContainer
    Label = "chan" .. @ID
  }
  ```

- A `{` after `=` is a subnode when its first token is an object key,
  `DS::Signal` shorthand or a directive; otherwise it is an array value.

### 2.1 Dict concatenation

Object bodies ("dicts") merge with the same `..` operator used for
strings and lists. The operands' definitions concatenate; a field or
child redefined by the later operand overrides the earlier one:

```marte
+Merged = {
  Class = ReferenceContainer
  Common = "base"
} .. {
  Class = ReferenceContainer
  Common = "override"
  Extra = "added"
}
```

evaluates to `Common = "override"`, `Extra = "added"`.

## 3. Literals

| Kind    | Forms                                     | Example                     |
| ------- | ----------------------------------------- | --------------------------- |
| Integer | decimal, `0x` hex, `0b` binary, unary `-` | `42`, `0x1F`, `0b101`, `-7` |
| Float   | decimal point, scientific notation        | `2.5`, `1.0e6`, `-0.25`     |
| Boolean |                                           | `true`, `false`             |
| String  | double-quoted, supports `\"` `\\` `\n` `\t` `\r` | `"localhost"`               |


## 4. Arrays

Commas between elements are optional:

```marte
Functions = { AcqReader, UdpTap }
Functions = { AcqReader UdpTap }        // identical
Matrix = {
  { 1 2 3 }
  { 4 5 6 }
}
```

### Conditional array elements

`#if`/`#else` blocks flatten inline inside array literals (the active
branch's elements are spliced into the enclosing list):

```marte
#let AcqFunctions: [&GAM] = {
  AcqReader,
  #if @udp_streamer
    UdpTap,
  #end
}
```

Elements of inactive branches are skipped. Nested conditionals are
allowed.

## 5. Expressions and operators

Operators, lowest to highest binding:

| Level | Operators         | Notes                     |
| ----- | ----------------- | ------------------------- |
| 1     | `\|\|`            | logical or                |
| 2     | `&&`              | logical and               |
| 3     | `==` `!=`         | equality                  |
| 4     | `<` `>` `<=` `>=` | comparison                |
| 5     | `..`              | concatenation (see below) |
| 6     | `\|`              | bitwise or                |
| 7     | `&`               | bitwise and               |
| 8     | `+` `-`           | arithmetic                |
| 9     | `*` `/` `%`       | arithmetic                |

Unary `-` and `!` bind tightest. Parentheses group as usual.

**Concatenation (`..`)** works on three kinds of operands:

- strings — `"chan" .. @ID` produces `"chan3"`;
- lists — `{ 1, 2 } .. { 3, 4 }` produces `{ 1 2 3 4 }`;
- dicts (object bodies) — `{ A = 1 } .. { B = 2 }` merges both, the
  later operand winning on key conflicts (see section 2.1).

`..` binds looser than `+`/`-`/`*` and tighter than comparisons, so
`"v" .. @n + 1` means `"v" .. (@n + 1)`. Parenthesising is still
recommended for readability.

Arithmetic, comparison, logical and bitwise operators work everywhere —
field values, directive conditions and variable initializers alike.


## 6. Variables and constants

```marte
var udp_streamer: bool = true          // overridable variable
let Period_us: float = (1.0e6 / 1.0e6) // constant, requires a value
```

- The type annotation (`: type`) is **mandatory** for both `var` and
  `let`; `let` additionally requires an initial value.
- Types are CUE type expressions: built-ins `string`, `int`, `uint`,
  `float`, `bool`; width-suffixed aliases (`uint32` → `uint`,
  `float64` → `float`, …); union types `int|uint`; reference types
  `&GAM` (reference to a class, checked as a string) and open reference
  lists `[&GAM]` (list of references). Bare class names (`GAM` without
  `&`) are accepted as references and type-checked as strings.
  Regex-constrained strings are written `string =~ "^localhost"`.
- Referencing: `@Name` (the `@` prefix is required at use sites).
- Variables are scoped: package root scope, nested package scopes, and
  node scopes (see section 1). Redefining the same variable name within
  one scope is an error, even across files.
- Command-line override: `mdt check -vName=value ...` rebinds a `var`
  before validation; `let` constants cannot be overridden.

## 7. Conditionals

```marte
#if (@ChannelBudget > 2)
  Profile = "wide"
#else if (@ChannelBudget == 2)
  Profile = "narrow"
#else
  Profile = "single"
#end
```

- Both `#else if` (two words) and `#elseif` (one word) are accepted;
  the two-word form is the recommended spelling.
- Conditions may be any expression; unresolved variables in a condition
  are reported as errors.
- Conditionals may appear wherever a definition may appear (file top
  level, inside objects, inside `#foreach` bodies) and inside array
  literals (section 4).
- Directive keywords work with or without the `#` (`if` … `end`).

## 8. Loops: `#foreach`

```marte
#foreach i in { 1, 2, 3, 4 }
  #if (@i <= @NumChannels)
    use AcqChannel Chan(ID = @i, Gain = (0.5 * @i))
  #end
#end

#foreach idx name in { Alpha, Beta }
  ("+Pair" .. @idx) = {
    Class = ReferenceContainer
    Partner = @name
  }
#end
```

- One variable: `#foreach value in <array-expression>`.
- Two variables (space-separated, no comma): `#foreach index value in …`
  binds the element index and the element.
- The iterable is an array literal or an expression evaluating to one.
- Loop and template variables are fully substituted in build output:
  `use AcqChannel Chan(ID = @i, Gain = (0.5 * @i))` emits objects with
  the evaluated `Index`, `Scale` and dynamic names.
- Validation is stable in multi-file projects and under `-v` overrides.

## 9. Templates: `#template` / `#use`

```marte
template AcqChannel(ID: int, Gain: float = 1.0)
  ("+Channel" .. @ID) = {
    Class = ReferenceContainer
    Scale = @Gain
  }
end

#foreach i in { 1, 2 }
  use AcqChannel Chan(ID = @i, Gain = (0.5 * @i))
#end
```

- Parameters are `Name: Type` and may declare defaults; trailing
  parameters with defaults may be omitted at the use site.
- `#use TemplateName InstanceName(key = value, …)` — the instance name
  is required; the argument list is optional for parameter-less
  templates. `use T I` without parentheses is valid.
- The template body usually creates objects with dynamic names derived
  from parameters (above); the instance name itself identifies the
  expansion.
- Parameter and loop-variable substitution in the expanded body works
  for object names and field values alike.
- Avoid naming non-signal template fields `Type`: every `Type` field is
  validated as a MARTe2 signal type (`uint32`, `float32`, …), so
  `Type = "Special"` raises `Invalid Type`. Use a different field name
  for template metadata.

## 10. Signal shorthand

Inside signal containers (`InputSignals`, `OutputSignals`, `Signals`):

```marte
InputSignals = {
  Timer::Counter: uint32 = {
    Frequency = 100
  }
}
OutputSignals = {
  DDB1::CounterCopy: uint32[1] as Copy
  MyDS::RawSignal2: uint32[4] as LocalName2 = {
    Gain = 1.0
  }
}
```

Full form: `DataSource::Signal [: Type [Dim]] [as Alias] [= { extra fields }]`

- `: Type` — signal type (`uint32`, `float32`, …).
- `[Dim]` — number of elements for vector signals.
- `as Alias` — local rename of the signal.
- `= { … }` — additional signal properties (`Frequency`, `Gain`, `Value`, …).

### 10.1 DataSource signal definition sugar

Inside a data source's `Signals` block, a signal may be declared with the
same `Name: Type` form instead of a nested `Type = …` object:

```marte
+DDB1 = {
  Class = GAMDataSource
  Signals = {
    CounterCopy: uint32
    Waveform: float32[4]
    Timed: uint32 = {
      Frequency = 100
      IPName = "IP"
    }
  }
}
```

- `Name: Type` is equivalent to `Name = { Type = Type }`.
- `Name: Type[Dim]` adds `NumberOfElements = Dim`.
- `= { … }` is optional and only needed for additional fields
  (`Frequency`, `IPName`, …); its fields are merged into the signal.
- The sugar is only accepted inside `Signals` blocks; elsewhere
  `Name: Type` is a syntax error.
- Build output always uses the expanded `Name = { Type = … }` form, and
  `mdt fmt` round-trips the sugar unchanged.

## 11. Comments, docstrings and pragmas

```marte
// plain line comment

//# Attached to the next definition; shown by LSP hover and docs.
+Timer = { … }

//! unused: Time is produced but not consumed here.
//! implicit: unknown upstream signal
//! not_produced: filled by hardware
//! not_consumed: monitored only
//! cast(uint32, int32): intentional widening
//! ignore(implicit)
//! allow(unused): global suppression for this file
```

- `//#` docstrings attach to the following definition.
- `//!` pragmas suppress specific warnings; `pragma(arg): reason` and
  global `//! allow(kind)` forms are both accepted.

## 12. Multi-file projects and the CLI

| Command                                              | Purpose                             |
| ---------------------------------------------------- | ----------------------------------- |
| `mdt check [-P dir] [-p project] [-j vars.json] [-vVAR=VAL] files…` | validate                            |
| `mdt build [-P dir] [-j vars.json] [-o out] [-vVAR=VAL] files…`     | evaluate + merge to a MARTe2 config |
| `mdt fmt files…`                                     | canonical formatting                |
| `mdt graph`                                          | signal-flow graph (DOT/HTML)        |
| `mdt lsp`                                            | language server                     |

- `mdt check some-file.marte` validates **that file in isolation**:
  cross-file objects and variables are not loaded and are reported as
  unresolved. Validate multi-file projects with an explicit file list or
  `-P <dir>` (recursively collects `*.marte`).
- `-P <dir>` and a file list both work for `check` and `build`; passing
  a bare directory as an input behaves like `-P <dir>` (the directory is
  scanned recursively for `*.marte` files).
- `-j vars.json` loads variable overrides from a flat JSON file
  (`{ "Name": value, … }`). Later `-j` files override earlier ones;
  `-vVAR=value` overrides everything. Values keep their JSON types
  (numbers stay numbers, strings stay quoted, arrays become lists);
  nested objects are rejected.

## 13. Validation status of the shipped examples

| Example                                      | Command                                                                       | Result                        |
| -------------------------------------------- | ----------------------------------------------------------------------------- | ----------------------------- |
| `examples/syntax_showcase` (whole directory) | `mdt check -P .`                    | 1 expected error: `external.marte` deliberately mismatches IOGAM input/output signal sizes |
| `examples/syntax_showcase/loops.marte`       | `mdt check loops.marte`             | 1 expected single-file diagnostic (cross-file `@NumChannels`) |
| `examples/simple/main.marte`                 | `mdt check main.marte`              | 1 implicit-signal warning    |
| `examples/complex_func_list`                 | `mdt check app.marte functions.marte states.marte` | 2 implicit-signal warnings |
| `examples/advanced_features.marte`           | `mdt check advanced_features.marte` | fails (stale tail, see below) |

`advanced_features.marte` is the historical demo; its trailing
`if @x … Assign = T` block no longer validates ("only defined inside a
conditional block") and its build output does not substitute loop
variables. `examples/syntax_showcase/` supersedes it as the validated
reference for templates and loops.

## 14. External structured data: `with json/csv`

Load an external JSON or CSV file and bind it to a name for a block:

```marte
with json("data/epics_cfg.json") as epics_cfg begin
  +EpicsSignals = {
    Class = ReferenceContainer
    Signals = {
      foreach name, object in @epics_cfg.signals do
        @name = {
          Class = ReferenceContainer
          Type = @object.Type
          PVName = @object.PVName
        }
      end
    }
  }
end
```

- `with <format>("path") as <name>` — supported formats: `json`, `csv`.
  The path expression may use variables and `..`; relative paths resolve
  against the directory of the file containing the block.
- `begin … end` and `{ … }` are both accepted as body delimiters.
- JSON mapping: object → dict, array → list, integral number → int,
  other numbers → float, string → string, bool → bool, `null` → key
  omitted. CSV: the header row names the columns, every row becomes a
  dict of strings, and the file yields a list of those dicts.
- **Member access**: `@epics_cfg.signals`, `@object.Type`, chains
  (`@a.b.c`). Accessing a missing member yields an empty value.
- **Dict iteration**: `foreach key, value in @map` (also
  `foreach key, value in @map do … end`). Keys iterate in sorted order.
- The whole document or any member can be assigned to a field; dicts
  render as `{ key = value … }` blocks in build output.
- A missing or malformed file is reported as
  `with <format>("path"): …` at the block position.

- **Editor support**: hovering a signal usage such as `EpicsSignals::Stat`
  shows the datasource, the signal type and its extra properties (e.g.
  `PVName`) even when they come from the loaded document, and
  go-to-definition jumps to the signal's definition site. This works
  immediately after a document is opened (expansions are part of the
  editor's snapshot, not delayed until validation). Inlay hints for a
  shorthand show only the resolved type — the datasource is already
  written in `DS::Signal`, so it is not repeated as a hint.
- Objects may be declared with or without a `+`/`$` prefix
  (`GAM = { … }` is equivalent to `+GAM = { … }` for structural
  detection like GAM/datasource classification).

A validated example lives in `examples/syntax_showcase/external.marte`
(with `data/epics_cfg.json`).
