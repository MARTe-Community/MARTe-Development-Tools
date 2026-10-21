# Build Optimisations (`mdt build -O`)

`mdt build --optimize` (short `-O`) post-processes the merged configuration
before writing it, with the goal of producing a cheaper MARTe runtime
without changing observable behaviour.

```bash
mdt build -P . -O -o optimized.marte
# stdout/stderr:
# Optimisations: merged 2 IOGAM(s), reordered 6 signal(s) in 1 DataSource(s)
```

The optimisations run on the fully evaluated tree (variables and overrides
already applied), after validation. The plain build is unchanged unless
`-O` is passed.

## What MARTe does with these structures

* **IOGAM** is a copy GAM: it copies its `InputSignals` list onto its
  `OutputSignals` list **positionally** (i-th input → i-th output). Every
  IOGAM in a thread is one more function invocation in the executor, each
  with its own signal-copy setup.
* A GAM reading several signals from the same DataSource performs one copy
  per signal. Copies that address **adjacent memory** in the DataSource's
  signal storage can be coalesced into a single, larger copy; copies that
  jump around cannot. Signal order inside a DataSource is declaration
  order, so declaration order determines how good (or bad) it is.
* IOGAM copies read from one DataSource and write to another; running
  copies that share a DataSource back-to-back keeps the source memory hot
  and lets the copy loop stay contiguous per DataSource.

## Implemented transformations

### 1. Merge adjacent IOGAMs

Consecutive IOGAM references in a `Functions` list are folded into the
first one: the signal lists are concatenated in the same order, so the
copies happen in exactly the sequence they would have anyway, but with one
function invocation instead of N.

Guards — a run is merged only when:

* the referenced nodes are `IOGAM` objects,
* they are defined in a single source fragment, carry no doc comment, and
  have no extra fields besides `Class`/`InputSignals`/`OutputSignals`
  (merging must not silently drop configuration),
* the signal names do not collide between the merged GAMs,
* **every** `Functions` list that mentions any GAM of the run contains the
  same run, in the same order, adjacent. A state that references only part
  of the run keeps its original behaviour, so such runs are skipped.
  Shared `let functions: [&GAM] = {…}` lists are rewritten once and affect
  every thread that uses them, which is safe because they are the same
  list.

IOGAMs separated by a non-copy GAM are never merged: moving copies across
a computing GAM could reorder reads and writes.

### 2. Drop identity copy GAMs

An IOGAM whose every pair copies a signal onto itself (same DataSource,
same signal name, same type and element count) performs no work. Such a
GAM is removed together with its references. GAMs referenced from more
than one `Functions` list are left alone, as are GAMs with doc comments.

### 3. DataSource signal ordering (fewer, larger copies)

**Only DataSources whose declaration order is free are reordered** — an
allowlist, because for most classes the order is dictated by an external
API or the hardware:

| Reordered | Never reordered |
|---|---|
| `GAMDataSource` (internal storage) | `FileReader` / `FileWriter` — signal order is the binary layout on disk |
| `EPICSCAInput` / `EPICSCAOutput` / `EPICSCAClient` (addressed by PV name) | `SDNSubscriber` / `SDNPublisher` — layout fixed by the SDN payload |
| `EPICSPVAInput` / `EPICSPVAOutput` / `EPICSPVA` (addressed by PV name) | hardware data sources (`NI6259*`, `NI6368*`, `MDS*`, …) — channel map comes from the hardware configuration |
|  | any unknown/custom class — conservative default |

Namespaced class names (`DAN::Foo`) are matched on their last segment.
Sources left alone are counted in the report
(“kept the signal order of N data source(s) with an external layout”).

Signals inside each DataSource's `Signals` block are ordered by **first
consumer** in the execution order (states → threads → `Functions`), with
the signal name as tie-breaker. Signals read by the same GAM end up next
to each other, so the runtime's per-signal copies can be coalesced into
contiguous blocks. Signals that are never consumed keep their relative
order after the consumed ones.

### 4. IOGAM copy grouping

IOGAM pairs are grouped by the **source DataSource** of the input signal.
The permutation is applied to `InputSignals` and `OutputSignals`
identically, so positional pairing (i-th input → i-th output) is
preserved. Copies reading the same DataSource then run consecutively.

Because IOGAM copies are ordered operations, grouping is skipped when a
pair writes a signal that another pair reads (e.g. `A→B` and `B→C` in the
same IOGAM): moving those copies would change what each pair observes.

### 5. GAM signal grouping

For non-IOGAM GAMs, `InputSignals`/`OutputSignals` entries are grouped by
DataSource and name. Entries are named, so their order is not semantic;
grouping keeps per-DataSource copies contiguous.

## Safety rules

* Optimisation runs **after** validation of the plain configuration; the
  optimizer never changes field values, signal types, sizes or classes.
* Every transform is guarded; anything ambiguous (multi-fragment GAMs,
  doc comments, extra fields, partially shared function lists, dependent
  copy pairs, DataSources with an external layout) is left untouched and
  simply not counted as an optimisation.
* The optimized output is itself a valid configuration: `mdt check` on it
  produces the same diagnostics as on the plain output (modulo error
  counts that drop because GAMs were merged).

## Investigated but not implemented

These paths were evaluated and deliberately left out; each changes
semantics or needs runtime information the builder does not have:

* **Merging non-adjacent IOGAMs.** Moving a copy across a computing GAM
  can reorder reads/writes of the same signal. Only strictly adjacent
  runs are merged.
* **Copy propagation through IOGAM chains** (A→B, B→C ⇒ A→C when B is
  otherwise unused). Correct in principle, but it needs a whole-program
  check that B is never consumed elsewhere, including by states not in
  the build, and it changes the produced signal set. A candidate for a
  future `-O2`.
* **Removing unused DataSource signals / GAMs.** Signals may be read by
  external tools or other states; GAM outputs may be producers for signals
  consumed elsewhere. The validator warns (`Unused Signal`), but silently
  deleting signals is unsafe.
* **Reordering GAMs within a thread.** Execution order is observable
  (state machines, feedback loops); only the order *inside* a signal list
  is touched.
* **Constant folding / expression evaluation.** Already happens in every
  build: `mdt build` emits evaluated values, not expressions.
* **Splitting a GAM into per-DataSource copies.** Would duplicate GAM
  execution and is only profitable with runtime measurements.
