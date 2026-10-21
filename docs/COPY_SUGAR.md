# Copy / fan-out sugar (experimental)

> Branch `experimental/copy-and-fanout`. Semantics are settled and tested,
> but the syntax may still change before it lands on `main`.

Four build-time constructs remove the boilerplate around IOGAM copies:

1. **`from` wiring** in signal lists — zero-copy when the producer and the
   consumer share a DataSource.
2. **`copy` groups** — named or inline groups of copies with aliases,
   indexes, ranges and placement, lowered to IOGAMs (and synthesized
   DataSource buffers when needed).
3. **`gam` pipeline blocks** — reusable macro bundling GAMs and wiring
   behind named ports.
4. **`fuse A with B`** — expression-GAM fusion for single-consumer
   intermediate signals.

Everything is resolved at build time; the emitted configuration contains
only ordinary signals, IOGAMs and GAMDataSources. `mdt check` lowers the
sugar before validation, so sugar-backed configurations are validated as
if they were written out.

## `from` wiring

```marte
+Filter = {
  Class = FilterGAM
  InputSignals = {
    P1 = from Acq.T1                 // slot P1 reads DDB::T1
    Slot2 = from Acq[2]              // second output signal
    X = from Acq.T1 as Local         // rename the slot, keep the signal
  }
}
```

* No copy GAM is generated when the producer's DataSource is usable by the
  consumer: the entry becomes `{ DataSource = <producer DS>, Alias = <signal> }`.
* `Acq[n]` selects the n-th (1-based) output signal of `Acq`.
* A signal already carrying an `Alias` is resolved through the alias (the
  DataSource signal name), not the local slot name.
* Explicit `: type[dim]` overrides an otherwise missing type.

## `copy` groups

```marte
+Functions = {
  Class = ReferenceContainer

  copy Links {                               // named group → +Links IOGAM
    Acq.T1 -> Filter.In1 as FIn1             // destination alias
    Acq[2] as T2L -> Filter.In2 at 1         // source alias, placement
    Acq.T1 -> SINK::Telemetry                // DataSource destination
    Acq[1..2] -> Monitor[1..2]               // ranges, element-wise
  }

  +Acq = { … }
  +Filter = { Class = FilterGAM  InputSignals = { In1 = { DataSource = DDB } } }
}

+States = { … Functions = { Acq Links Filter } … }
```

Inline groups sit directly in the `Functions` list and expand in place:

```marte
Functions = { Acq copy { Acq.T2 -> Filter.In3 } Filter }
```

An anonymous group written in an object body (outside a `Functions` list)
is rejected: nothing would ever reference it. Give it a name and list that
name in the thread, or move it into the list.

### Endpoints

| Form | Meaning |
|---|---|
| `GAM.Signal` | signal by name (outputs for sources, inputs for destinations) |
| `GAM[n]`, `GAM[a..b]` | signal by 1-based index/range in that GAM's signal list |
| `DS::Signal` | a signal already declared in a DataSource |

### Modifiers

| Modifier | Side | Effect |
|---|---|---|
| `as NAME` | source | IOGAM input entry is named `NAME` with `Alias = <signal>` |
| `as NAME` | destination | destination slot is renamed to `NAME` with `Alias = <signal>` |
| `at N` | destination | generated declaration is placed at 1-based position `N` |
| `: type[dim]` | item | type for signals that declare none |
| `{ Name = value … }` | both | per-signal options emitted verbatim into the generated entry (`Type`, `NumberOfElements`, `Samples`, `Frequency`, `Ranges`, `Value`, `Trigger`, `Comparator`, …) |
| `(a, b) -> (c, d)` | both | grouped endpoints: a contiguous memory block (see below) |

Per-signal options follow the endpoint, before `->` for the source and
after the destination alias for the destination:

```marte
copy DecimateGAM {
  DDB1::PXI6259_Status_SAMPLES { Ranges = { { @dec_index, @dec_index } } }
    -> DDB1::PXI6259_Status
  DDB1::FAST_STOP { Type = uint32 } -> EPICS::MHVPS_STOP
  DDB3::WaveformPacketID -> SDNReply::ReplyWaveformAck { Trigger = 1 }
}
```

`Type`/`NumberOfElements` options also drive type inference for
synthesized destination signals; other options are copied through
unchanged (their values are evaluated like any field).

### Grouped endpoints (memory blocks)

An IOGAM does not copy signal *pairs*: `Setup()` only requires that the
total input memory size equals the total output memory size, and `Execute()`
copies the whole input memory to the output memory. Entry order therefore
defines the byte mapping, which allows packing and unpacking:

```marte
copy Pack {
  (Src::A, Src::B, Src::C) -> (Dst::D, Dst::E)
}
```

With `A: uint32`, `B: uint8`, `C: uint8` (6 bytes) and `D: uint32`,
`E: uint16` (6 bytes), the generated IOGAM has three inputs and two outputs
and maps bytes 0–3 to `D` and bytes 4–5 to `E` in one memory copy. The
reverse form (`Dst::E -> (Src::B, Src::C)`) unpacks one signal into two.
Members may carry the same per-signal options; aliases are allowed too.

Validation when any group is present:

* every signal must have a determinable byte size (`Type` + elements), else
  the build fails with the position of the offending signal;
* each op must move the same number of bytes on both sides (`moves N input
  bytes into M output bytes` otherwise);
* the cumulative byte offset of every op must match on both sides, and the
  totals must match (an IOGAM copies one contiguous block);
* pairing order is the declaration order on each side.

The optimizer never reorders or merges a block IOGAM's entries: the byte
mapping is positional. `mdt build -O` still merges adjacent IOGAMs only when
their input/output counts are equal and the merge keeps whole blocks in
order.

### Lowering rules

* A pair whose source and destination are the same DataSource **and**
  signal is redundant: nothing is emitted and the pair is counted in the
  report.
* A destination slot that is not declared gets a synthesized
  `GAMDataSource` buffer (`MdtCopyBufN`, `AllowNoProducers = 1`) holding
  the signal with the resolved type; the GAM slot points at it.
* A group becomes one IOGAM (`+Links` for named, `+MdtCopyN` for inline)
  inserted at the group's position; the `Functions` list keeps a single
  reference. Copy pairs are emitted in item order (IOGAM copies are
  positional).
* Reading the same source signal for several destinations auto-aliases the
  extra reads (`T1`, `T12 = {… Alias = "T1"}`) so signal names stay unique.
* Group errors (unknown GAM/signal, index out of range, placement out of
  range, range-length mismatch) fail the build with the sugar position.

## `gam` pipeline blocks

A pipeline packages GAMs plus their wiring behind named ports:

```marte
gam Preprocess {
  in  Raw: float32
  out Clean: float32
  out Meter: float32

  +Scale = {
    Class = MathExpressionGAM
    InputSignals = { X = { DataSource = DDB  Type = float32 } }
    OutputSignals = { Scaled = { DataSource = DDB  Type = float32 } }
    Expression = "Scaled := X*0.5"
  }
  from Raw -> Scale.X

  +Limiter = {
    Class = FilterGAM
    InputSignals = { In = { DataSource = DDB  Type = float32 } }
    OutputSignals = { Out = { DataSource = DDB  Type = float32 } }
  }
  from Scale.Scaled -> Limiter.In
  from Limiter.Out -> Clean
  from Scale.Scaled -> Meter
}
```

Instantiate it as an object whose `Class` is the pipeline name and whose
fields bind the ports:

```marte
+Pre = {
  Class = Preprocess
  Raw   = Inputs::Raw       // any signal reference: DS::Sig or GAM.Sig
  Clean = DDB::Clean
  Meter = Telemetry::Meter
}

Functions = { Pre }         // expands to the body GAMs + wiring
```

Expansion rules:

* Body GAMs are emitted with the instance prefix (`+Pre_Scale`,
  `+Pre_Limiter`) into the instance's container; the instance object and
  the `gam` definition itself are not written.
* Each `from` line becomes a copy group. It is scheduled **before the GAM
  that consumes its destination** (or after its producer when it only
  feeds an output port), so the declaration order in the body does not
  have to match execution order. Copies between internal GAMs are renamed
  with the instance prefix; port endpoints are replaced by their bound
  signals.
* `Functions = { Pre }` is replaced by the same order (`copy groups`
  included), so the runtime sequence matches the expansion.
* Unbound ports fail the build. An instance can be referenced from several
  threads (it expands once per instance definition).
* Pipelines nest one level (a pipeline body may contain copy groups and
  GAMs; instantiating another pipeline inside a body is not supported in
  this branch).

## `fuse A with B`

```marte
Functions = { Pre fuse Pre with Post Post }
```

Both GAMs must be expression GAMs (`MathExpressionGAM`-like, with an
`Expression` field) and be adjacent, in order. For every signal produced by
`A` and consumed by `B`:

* it must have **exactly one** consumer (`B`), otherwise fusion is skipped;
* `A`'s expression must assign it (`Y := …`);
* `B`'s expression references are replaced by the parenthesised
  assignment.

The result keeps `A`'s position with the union of the inputs, the union of
the outputs minus the intermediates, and a combined expression
(`Z := X+1; W := (X*2)+1`). `B` is removed. Nothing changes when a
condition fails; the build proceeds without fusion.

## Tooling

* `mdt refactor [-P dir] [--check] [-n] files…` rewrites existing
  configurations to this syntax automatically: every IOGAM becomes a named
  copy group (1:1 when the signal counts match, grouped memory blocks when
  they do not and the byte sizes are known), every `DataSource` signal entry
  becomes `Signal: Type[Dim] = { … }`, and every GAM signal entry becomes
  `DS::Signal: Type[Dim] as Alias = { … }`. The rewrite is idempotent,
  preserves `//!` pragmas (they stay attached to the generated copy items)
  and reports the number of rewritten declarations per file. `--check` is
  suitable for CI; `-n` prints instead of writing.
  Verification is on by default: both file sets are built and the outputs
  compared semantically (objects, classes, fields, signal entries and ordered
  function lists). If they differ the tool writes nothing — the caller keeps
  the original files, so the refactor is effectively reverted — and exits
  non-zero. `--no-verify` skips the comparison.

* `mdt build` prints a summary, e.g.
  `Copies: 7 copy pair(s), 1 direct wire(s), 1 GAM pair(s) fused, 1 pipeline(s) expanded`.
* `mdt check` lowers sugar before validation (sugar counts as a signal
  producer).
* `mdt build -O` runs after lowering, so adjacent generated IOGAMs merge
  and DataSource ordering sees the final signal set. Named copy groups are
  ordinary IOGAMs and follow the normal optimizer rules.
* `examples/complex/` is written entirely in the new syntax: a pipeline
  macro, a fan-out copy group with aliases/indexes, `from` wiring and a
  fused expression chain.
* Graph/LSP support for the new syntax is not part of this branch.

## Editor support

The LSP understands the sugar:

* **Diagnostics**: the tree is lowered on a clone before validation, so copies
  count as signal producers, pipeline ports are checked, and sugar errors
  (unknown endpoint, byte-size mismatch, unbound port) appear as
  `mdt-sugar` diagnostics at the item position.
* **Hover** on `DS::Signal` / `GAM.Signal` shows the DataSource, class, type
  and dimensions; hovering `Class = <pipeline>` lists the macro's ports.
* **Go-to-definition** on an endpoint jumps to the signal declaration (or the
  GAM entry), and on a pipeline class to the `gam` definition.
* **Completion** after `->` and `from ` suggests GAM names (and DataSource
  names, inserting `::`).

## Not included in this branch

* Nested pipeline instantiation (a `gam` body instantiating another
  pipeline).
* Inlay hints for copy items.
* Pipeline instances inside `#if`/`#foreach` blocks: expansion produces
  unconditional references to conditional definitions, so instantiate
  pipelines outside conditional blocks (copy groups work there).
