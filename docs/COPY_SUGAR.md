# Copy / fan-out sugar (experimental)

> Branch `experimental/copy-and-fanout`. Semantics are settled and tested,
> but the syntax may still change before it lands on `main`.

Three build-time constructs remove the boilerplate around IOGAM copies:

1. **`from` wiring** in signal lists — zero-copy when the producer and the
   consumer share a DataSource.
2. **`copy` groups** — named or inline groups of copies with aliases,
   indexes, ranges and placement, lowered to IOGAMs (and synthesized
   DataSource buffers when needed).
3. **`fuse A with B`** — expression-GAM fusion for single-consumer
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

* `mdt build` prints a summary, e.g.
  `Copies: 4 copy pair(s), 2 direct wire(s), 1 copy buffer(s), 1 GAM pair(s) fused`.
* `mdt check` lowers sugar before validation (sugar counts as a signal
  producer).
* `mdt build -O` runs after lowering, so adjacent generated IOGAMs merge
  and DataSource ordering sees the final signal set. Named copy groups are
  ordinary IOGAMs and follow the normal optimizer rules.
* Graph/LSP support for the new syntax is not part of this branch.

## Not included in this branch

* `gam Name { in … out … }` pipeline blocks (the optional composite-macro
  form). They need port binding semantics on top of the copy/wiring rules
  above; the two mechanisms they expand to (`copy` groups + `from`) are
  available.
* LSP completion/hover/definition for `copy`/`from`.
