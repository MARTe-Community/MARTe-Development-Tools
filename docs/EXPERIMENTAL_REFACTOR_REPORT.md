# Experimental copy-sugar refactor — report

Branch: `experimental/copy-and-fanout` (6 commits, off `main`).
Scope: build-time syntactic sugar for signal copies, fan-out and GAM
pipelines, plus a refactor of `examples/big_project` as a real-world
validation.

## Deliverables

| Feature | Syntax | Lowered to |
|---|---|---|
| Named copy group | `copy Links { Acq.T1 -> Filter.In1 }` + `Functions = { … Links … }` | `+Links = { Class = IOGAM … }` |
| Inline copy group | `Functions = { Acq copy { … } Filter }` | `+MdtCopyN` at that position |
| Endpoint selection | `GAM.Sig`, `GAM[n]`, `DDB::Sig` | input/output entries of the IOGAM |
| Aliases | `src as A`, `-> dst as B` | `A = { DataSource = …, Alias = "src" }` |
| Placement | `-> Filter.In at 1` | generated declaration at 1-based index 1 |
| Per-signal options | `src { Samples = @n  Ranges = { {0,0} } }`, `dst { Trigger = 1 }` | copied verbatim into the signal entry |
| Grouped endpoints | `(a, b) -> c`, `a -> (b, c)` | one memory-block copy (pack/unpack) with byte-size validation |
| Direct wiring | `In = from Acq.T1` | `In = { DataSource = DDB, Alias = "T1" }` (no GAM) |
| Pipeline macro | `gam P { in …, out … }` + `+X = { Class = P, ports }` | body GAMs (instance-prefixed) + scheduled wiring |
| Fusion | `fuse A with B` | inlined expression, `B` removed (single-consumer signals) |
| Buffers | automatic | `+MdtCopyBufN` (`GAMDataSource`, `AllowNoProducers = 1`) |

`mdt build` prints a summary (`Copies: … copy pair(s), … direct wire(s), …
pipeline(s) expanded`), and `mdt check` lowers the sugar before validation.

## Configuration line counts

| Project | Before | After | Δ |
|---|---:|---:|---:|
| `examples/big_project` source (36 `.marte` files) | 9 482 | 7 598 | **−1 884 (−19.9 %)** |
| ↳ built configuration | 7 772 | 7 772 | 0 |
| `examples/complex` source | 66 | 181 | +115 |
| ↳ built configuration | 62 | 264 | +202 |

Largest per-file reductions:

| File | Before | After | Δ |
|---|---:|---:|---:|
| `thread1/thread_data_broker.marte` | 574 | 93 | −481 |
| `thread5/dio_dan_writer.marte` | 996 | 556 | −440 |
| `thread4/adc_2_dan.marte` | 1083 | 688 | −395 |
| `thread3/datasync.marte` | 473 | 222 | −251 |
| `thread2/sdn.marte` | 263 | 162 | −101 |
| `timers.marte` | 196 | 133 | −63 |
| `thread6/synch.marte` | 149 | 88 | −61 |
| `thread3/epics.marte` | 68 | 17 | −51 |
| `thread1/references_generator.marte` | 137 | 118 | −19 |
| `thread6/fhps_a.marte` | 118 | 107 | −11 |
| `thread6/fhps_b.marte` | 118 | 107 | −11 |

The other 25 files are unchanged.

## Refactored items in `big_project`

| Metric | Value |
|---|---:|
| IOGAMs replaced by named copy groups | **23 (all)** |
| Copy items written in source | **388** |
| Active items in the default build | 369 |
| Items in inactive `#if @sdn_enabled` groups | 19 (3 groups) |
| Aliased entries (`as`) | 29 |
| Grouped (memory-block) items | 2 (`PulseStartTime`, `PulseStopTime`: 64-bit → 2×32-bit) |

Groups by the options they use:

| Category | Groups |
|---|---:|
| plain (DataSource only) | 4 |
| `Type` only | 4 |
| `Samples` / `Frequency` | 6 |
| `Ranges` | 4 |
| mixed (`Type`+`Trigger`, `Type`+`NumberOfElements`+`Value`, …) | 5 |
| **total** | **23** |

Option kinds present (a group can use several):

| Option | Groups |
|---|---:|
| `Type` | 6 |
| `Frequency` | 6 |
| `Ranges` | 5 |
| `Samples` | 3 |
| `Trigger` | 2 |
| `NumberOfElements` | 2 |
| `Value` | 2 |

Emitted configuration: 20 active IOGAM objects with **740 signal entries**,
6 GAMDataSources, 40 threads, 7 states.

## Feature usage across the examples

| Feature | big_project | complex | Total |
|---|---:|---:|---:|
| Named copy groups | 22 | 1 | 23 |
| Pipelines (`gam` + instance) | 1 | 1 | 2 |
| `from` wiring | 0 | 1 | 1 |
| `fuse` pairs | 0 | 1 | 1 |
| Synthesized buffers | 0 | 1 | 1 |
| Index/placement modifiers | 24 aliases | `Acq[2]`, `at 1` | — |

`big_project` pipeline: `gam AdcPublication` binds 19 source signals once and
contains the `PushADCtoDAN` and `PushADCtoEpics` copy groups; the conditional
`PushADCtoUDP` stays a copy group inside `#if @udp_streamer`.
`complex` pipeline: `gam Preprocess` (2 internal GAMs, 3 ports, scheduled
wiring, port fan-out).

## Equivalence verification

`TestBigProjectRefactorEquivalence` builds both projects (≈1.1 s) and
compares a canonical semantic model:

- every object and its `Class`;
- all fields with canonicalised values;
- every signal entry (name, `DataSource`, `Alias`, and options such as
  `Type`, `Samples`, `Ranges`, `Value`, `Trigger`);
- ordered thread `Functions` lists;
- IOGAM `InputSignals`/`OutputSignals` in order (copy pairing is positional).

Ignored: definition order inside containers and `Alias` quoting
(`Alias = Status` vs `Alias = "Status"`), neither of which is semantic.
`git diff` of the two built files is 166 lines, all of that kind.

Both projects report `No issues found` under `mdt check -P`.

## Optimizer interaction (`-O`)

| Project | Result |
|---|---|
| `big_project` | reordered 374 signals in 9 DataSources, grouped 46 GAM signal lists, kept the order of 20 external-layout sources |
| `big_project_sugar` | merged 1 IOGAM, reordered 375 signals in 9 DataSources, grouped 42 GAM lists, kept 20 external-layout sources |

The extra merge in the refactored project comes from adjacent generated
copies sharing a source signal (the lowering pass auto-aliases duplicate
reads); the original hand-written IOGAMs are not adjacent in a way the
optimizer can merge.

## Implementation and test inventory

| Area | Files | Lines |
|---|---|---:|
| Parser (copy groups, options, grouped endpoints) | `internal/parser/copy_sugar.go` | 410 |
| Parser (pipelines) | `internal/parser/pipeline.go` | 101 |
| Lowering (wiring, copy groups, blocks, buffers) | `internal/builder/lower.go` | 1 358 |
| Pipeline expansion | `internal/builder/pipeline.go` | 430 |
| Expression-GAM fusion | `internal/builder/fuse.go` | 428 |
| Docs | `docs/COPY_SUGAR.md` | 262 |
| Tests (unit/integration) | `copy_sugar_test.go` 664, `pipeline_test.go` 190, `big_project_refactor_test.go` 237, `conditional_blocks_test.go` 83 | 1 174 |
| Tests (e2e) | `test/e2e/copy_sugar_test.go` | 98 |

Test count: 27 new tests on the branch (20 copy/options/groups/fusion,
3 pipeline, 1 conditional-activation regression, 1 big-project equivalence,
1 optimizer block-order, 1 e2e).
Full suites: `go test ./test/...` green (≈3.7 s), `go test ./test/e2e/…`
green (≈1.7 s), `go vet` clean.

## Defects found and fixed during the refactor

1. **Independent `#if` blocks deactivated each other** in the builder's
   activation pass; only the last branch survived. Regression test
   `TestMultipleIfBlocksBothActive`.
2. **Generated GAMs were inserted into the wrong fragment** (first active
   fragment of the node instead of the sugar's own file), moving them to
   another position and producing duplicate-field reports.
3. **Generated nodes did not inherit the fragment's conditional flag**, so
   copies inside `#if` were treated as unconditional.
4. **Stale definition indices** when several copy groups live in one
   fragment: each insertion shifted the following ones. Now located by
   identity.
5. **Duplicate source reads** inside one copy group collided in the IOGAM
   signal map; they are now auto-aliased (`T1`, `T12 { Alias = "T1" }`).
6. **Redundant identity copies** are dropped instead of emitted.
7. **`1..3` lexed as a malformed number** (`1.`), breaking index ranges.
8. **Fusion ignored aliased consumers**, fusing a signal that another GAM
   read through `Alias`. Fixed by resolving the alias name.
9. **Pipeline endpoints**: DataSource references were instance-prefixed,
   generated names were double-prefixed, and named groups of instances not
   referenced by a `Functions` list were dropped.

## Limitations

- Grouped endpoints are validated for total byte size and per-op offsets,
  but not for type-level reinterpretation (a `uint16` output may cover two
  `uint8` inputs by design — that is the packing use case).
- Pipeline instances inside `#if`/`#foreach` are not expanded (generated
  references would be unconditional).
- No nested pipeline instantiation.
- No LSP completion/hover/definition or graph support for the new syntax.
- Alias quoting/field order in generated entries can differ from
  hand-written ones (semantically identical).

## Reproduction

```bash
go build -o build/mdt ./cmd/mdt

# line counts
find examples/big_project -name '*.marte' | xargs wc -l | tail -1
find examples/big_project_sugar -name '*.marte' | xargs wc -l | tail -1

# validation and equivalence
./build/mdt check -P examples/big_project
./build/mdt check -P examples/big_project_sugar
go test ./test/ -run 'TestBigProjectRefactorEquivalence|TestCopy|TestPipeline|TestFuse' -v
```
