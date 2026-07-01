# TODO / Fix Plan

Audit findings from a full codebase review (parser/index, validator, builder/formatter,
LSP, CLI/graph/docs). Each item has a confidence/severity rating and file:line
references. Check items off as they're fixed.

## Bugs

- [ ] **#1 Builder reorders fields named `Class` to the front** — HIGH severity, confirmed
      `internal/builder/builder.go:500-502`

  ```go
  sort.SliceStable(fields, func(i, j int) bool {
      return fields[i].Def.(*parser.Field).Name == "Class" && fields[j].Def.(*parser.Field).Name != "Class"
  })
  ```

  Contradicts `specification.md:86` ("the relative order of defined fields must be
  maintained in the output"). Any field literally named `Class` gets silently moved to
  the top of the output block regardless of source order.

- [ ] **#2 Bitwise operator validator bug** — HIGH severity, reproduced live
      Type-checking of `&`, `|`, `^` expressions in `#let`/`#var` value validation
      mis-evaluates/mis-types bitwise operator expressions. Needs a minimal repro case
      written first (e.g. `#let X: uint32 = 5 & 3`) to pin down the exact faulty code path
      in `internal/validator/validator.go` before fixing.

- [ ] **#3 `mdt build -o <file>` redirects diagnostics to stdout** — MEDIUM severity, confirmed
      `cmd/mdt/main.go:253`
      `logger.SetOutput(os.Stdout)` is only called inside the `-o` flag branch of
      `runBuild`'s arg-parsing loop, so diagnostics silently switch from the documented
      default (stderr) to stdout specifically when `-o` is used. Inconsistent with
      default behavior and undocumented. Decide the intended behavior and make it
      consistent (likely: diagnostics should stay on stderr regardless of `-o`).

- [ ] **#4 LSP debounce doc mismatch** — LOW severity, confirmed
      `internal/lsp/server.go:376`

  ```go
  valTimers[uri] = time.AfterFunc(1000*time.Millisecond, func() {...})
  ```

  Actual debounce is 1000ms, but documented as 500ms in
  `docs/CODE_DOCUMENTATION.md` and in `CLAUDE.md` (written in this same audit pass —
  needs correcting there too). Either fix the docs to say 1000ms, or reduce the
  debounce to 500ms if that was the intended value — confirm with the LSP's actual
  UX target before choosing.

- [ ] **#5 LSP `valCancels` map only cleaned up on file close** — LOW severity, confirmed
      `internal/lsp/server.go:337,381,386,689,691`
      Entries are only removed in `HandleDidClose`, not after a validation run completes.
      Bounded (keyed per-URI, overwritten on each edit) so not a real leak, but worth a
      cleanup for correctness/hygiene.

## Performance

- [ ] **#6 `mdt graph --state` O(edges × datasources) nested loop** — MEDIUM severity, confirmed
      `internal/graph/graph.go:322-328`
      State-filtering does a nested loop over edges and datasources despite an O(1)
      reverse-lookup map (`dsIDMap`) already existing elsewhere in the same file — should
      use it instead.

- [x] **#7 LSP `Snapshot.Clone()` cost on every edit** — MEDIUM-HIGH severity, fixed (see #19)
      `internal/lsp/server.go` (Session → View → Snapshot pattern)
      Every edit triggers a full immutable clone of the View's index. For large projects
      this could be a real hot path; worth profiling before optimizing (e.g. structural
      sharing / copy-on-write for unchanged files) to confirm it's actually costly in
      practice.

- [ ] **#8 CUE validation depth limit of 3 truncates silently** — LOW-MEDIUM severity, confirmed
      `internal/validator/validator.go` (`nodeToMapWithDepth(node, 3)`, ~line 854)
      Deeply nested structures beyond depth 3 are silently truncated before CUE schema
      validation, meaning schema violations deeper than 3 levels go undetected with no
      warning. Either raise the limit, or emit a diagnostic when truncation occurs.

## Missing / Wrong Features

- [ ] **#9 INOUT direction validation may be one-sided** — MEDIUM confidence
      Needs a targeted repro/read of the `datasource_direction` check in
      `internal/validator/validator.go` to confirm whether both directions of an `INOUT`
      datasource are actually validated, or only one.

- [ ] **#10 Builder does not itself check for duplicate fields** — LOW priority (downgraded)
      `internal/builder/builder.go`
      The CLI's build flow already validates before building and `duplicate_field` is
      reported at `LevelError` (`internal/validator/validator.go:452,581`), so this is
      already blocked in practice via `mdt build`. Only relevant if the Builder is ever
      invoked as a library without validation first — low priority, consider only if
      that use case is real.

## Undocumented Functionality

- [ ] **#11 23 of 37 diagnostic tags undocumented in README** — confirmed via grep
      README.md's diagnostic tag table lists only 14 tags; `internal/validator/validator.go`
      implements 37 distinct tags via `v.report(...)`. Regenerate/expand the README table
      to cover all of them (tag name, level, meaning, suppressible via `//! ignore(tag)`).

- [ ] **#12 Signal shorthand syntax missing from formal grammar** — confirmed
      `specification.md` doesn't document `DataSource::SignalName[: Type[NumElements]] [= {...}]`
      shorthand syntax in its formal grammar section, even though it's implemented and
      used. Add it to `specification.md`.

- [ ] **#13 `mdt graph` flags undocumented in README** — confirmed
      `--state`, `--follow`, `--simplified` flags exist (see `internal/graph/graph.go`,
      `docs/GRAPH_GUIDE.md`) but are missing/incomplete in the top-level README's `mdt
graph` usage section.

- [ ] **#14 `-vVAR=VAL` auto-quoting undocumented** — confirmed
      CLI variable-override flag auto-quotes values under some condition that isn't
      explained anywhere in README/specification.md. Document the quoting rule.

## Follow-up on this audit's own artifacts

- [ ] Fix `CLAUDE.md`'s stale claims introduced during this same audit pass:
  - LSP debounce listed as "500ms" — should say 1000ms (see #4 above).
  - Diagnostic tag count/list — only mentions the original 14, should note the true
    count (37) or link to the expanded README table once #11 is done.

## Fixed

- [x] **#15 False "Unused GAM" warning for GAMs only referenced via a top-level `#let` list, when the same file also has an unrelated top-level `#if`/`#foreach`/`#template` block** — HIGH severity, fixed
  Root cause: `Validator.isPositionActive` (`internal/validator/validator.go`) determined
  whether a package-level reference's position was "active" by returning the
  active-state of whichever non-object `Fragment` happened to be *first* in the
  node's fragment list for that file, instead of checking whether the reference's
  position actually fell inside that fragment. Top-level conditional fragments
  (from `#if`/`#foreach`/`#template` blocks) get appended to the node's fragment
  list *before* the main unconditional fragment (`internal/index/index.go`'s
  `populateNode`), so any package-level reference — including the elements of a
  `#let [&GAM]` list — could spuriously inherit the (false) active-state of an
  unrelated, condition-false block elsewhere in the same file. This made every
  GAM only reachable through that `#let` appear unused.
  Fix: `indexNestedDefinitions` now records the real line span (`ObjectPos`/`EndPos`)
  of each conditional top-level fragment (previously left zero-valued), and
  `isPositionActive` was rewritten to only treat a position as inactive if it
  falls within an inactive *conditional* fragment's line range, defaulting to
  active otherwise. Regression test:
  `TestLetReferenceUnaffectedByUnrelatedTopLevelConditional` in
  `test/let_macro_test.go` (verified to fail pre-fix, pass post-fix).

- [x] **#16 LSP diagnostics flicker: active semantic warnings briefly vanish on every keystroke** — HIGH severity, fixed
  Root cause: `publishImmediateDiagnostics` (`internal/lsp/server.go`) is called
  synchronously on every `didOpen`/`didChange` to give fast feedback for parser
  (syntax) errors, ahead of the debounced semantic validation pass. It read
  `snap.ParserErrors()[uri]` and unconditionally published that list — including
  when the file currently has *no* parser errors, in which case it published an
  *empty* diagnostics list. This overwrote whatever semantic diagnostics (e.g.
  "Unused GAM") were currently displayed for that file. The subsequent debounced
  `runValidation` pass then recomputed the exact same semantic diagnostics as
  before the edit and, because of its hash-based dedup (`lastPublished`), correctly
  skipped re-publishing them — so the bogus empty publish from
  `publishImmediateDiagnostics` was never overwritten, leaving the client's
  diagnostics pane empty for up to the full debounce window even though the
  warning was still valid, on every single keystroke.
  Fix: `publishImmediateDiagnostics`'s guard changed from `if !ok { return }` to
  `if !ok || len(errs) == 0 { return }` — it now only publishes when there are
  actual parser errors to report, leaving currently-displayed diagnostics alone
  otherwise (the debounced `runValidation` pass remains the single source of
  truth that reconciles and republishes the full, correct diagnostic set,
  including clearing stale parser errors once fixed). Regression test:
  `TestNoFlickerOnEditWithoutParserErrors` in `test/lsp_server_test.go` (verified
  to fail pre-fix, pass post-fix).

- [x] **#17 Unhandled panics in LSP background goroutines crash the whole `mdt lsp` process** — HIGH severity, fixed
  Root cause: two goroutine sites had no `recover()`, so any panic inside them —
  e.g. a nil-pointer dereference triggered by an unusual/malformed project
  structure during validation — would take down the entire LSP server process,
  losing all editor state and requiring a manual restart: (1) the debounced
  validation closure spawned via `time.AfterFunc` in `triggerValidation`
  (`internal/lsp/server.go`), and (2) each worker goroutine's task-processing
  loop in `Validator.ValidateProject`'s worker pool (`internal/validator/validator.go`),
  where a `recover()` around the whole `for` loop would only have protected the
  worker from *its first* panic, silently killing that worker permanently
  afterward (since `recover()` must be re-armed per invocation, not just per
  goroutine).
  Fix: added `defer func() { if r := recover(); r != nil { log...} }()` at the top
  of the `time.AfterFunc` closure in `triggerValidation`, and wrapped *each
  individual task* (not the whole worker loop) inside the worker goroutine in
  `ValidateProject` with its own recover, so a panic on one node fails just that
  one validation task (logged) instead of killing the worker or the process.
  No dedicated regression test (impractical without test-only fault-injection
  hooks); verified via full build/vet/test pass.

- [x] **#18 CUE schema reloaded and re-unified from scratch on every single LSP validation pass** — MEDIUM-HIGH severity, fixed
  Root cause: `schema.LoadFullSchema(projectRoot)` re-parsed the embedded base
  schema plus system/home/project `.marte_schema.cue` override files and
  re-unified them via CUE on *every* call — and it's called from
  `validator.NewValidator` on every validation pass, i.e. on every LSP
  keystroke (after debounce). This is pure, expensive, entirely avoidable
  repeated work for schema files that essentially never change mid-session.
  Fix: `LoadFullSchema` now caches its result per `projectRoot`, keyed and
  invalidated by the mtimes of the underlying schema files (system, home,
  and project `.marte_schema.cue`), via a package-level
  `map[string]*fullSchemaCacheEntry` guarded by a mutex; a cache hit returns
  the same `*Schema` pointer, a miss (no entry, or any tracked file's mtime
  changed) recompiles via the extracted `loadFullSchemaUncached`. Regression
  test: `TestLoadFullSchemaCachesUntilFileChanges` in `test/schema_cache_test.go`
  (verifies repeated calls return the identical pointer when nothing changed,
  and a new pointer with updated content once a project schema file's mtime/content
  changes; verified to fail pre-fix, pass post-fix). Confirmed real-world impact:
  the full `go test ./test/...` suite's runtime dropped from ~7.5s to ~3.5s after
  this fix.

- [x] **#19 LSP `Snapshot.Clone()` redundantly resolves references twice on every edit** — MEDIUM-HIGH severity, fixed (closes #7)
  Root cause: `HandleDidOpen`/`HandleDidChange` (`internal/lsp/server.go`) called
  `Snapshot.Clone()` (`internal/lsp/cache/cache.go`), which deep-clones the
  `ProjectTree` *and* fully re-resolves all cross-references
  (`ProjectTree.Clone()` → `ResolveReferences(nil)`, `internal/index/index.go`) —
  before the handler then called `Tree().AddFile(...)` to apply the actual edit
  and re-resolved references *again* afterward. The first resolve pass, done on
  the pre-edit tree, was thrown away immediately once `AddFile` mutated the
  cloned tree — pure wasted work on every keystroke, and for large projects with
  many cross-references this doubled the cost of the single most frequent
  operation in the LSP.
  Fix: split `ProjectTree.Clone()` into `Clone()` (unchanged behavior: clone +
  resolve, for callers that need to read the tree immediately) and a new
  `CloneUnresolved()` (clone only, `Reference.Target` left nil, for callers about
  to mutate + resolve themselves right after) sharing a common
  `cloneStructure()` helper. Mirrored this in `cache.Snapshot` with `Clone()`
  (unchanged) and a new `CloneForEdit()`. `HandleDidOpen`/`HandleDidChange` now
  call `CloneForEdit()` instead of `Clone()`, with an explicit
  `newSnap.Tree().ResolveReferences(nil)` added to their (in-practice
  unreachable, since `parser.Parse()` never returns a nil config) parse-failure
  branches for defensive correctness. Regression test:
  `TestCrossFileReferencesResolveAfterEditsViaCloneForEdit` in
  `test/lsp_server_test.go`, exercising the full open → edit → cross-file
  "go to definition" flow through the lighter clone path (this is a pure
  performance refactor, not a bug fix, so this test guards against future
  regressions rather than failing pre-fix). Also verified via `make test-e2e`
  against the real compiled binary and a real `mdt lsp` subprocess.

- [x] **#20 "Go to definition"/rename on `DataSource::SignalName` shorthand syntax resolves to the wrong thing or produces an incorrect edit** — HIGH severity, fixed
  Root cause, across three layers, for the signal shorthand syntax (`internal/parser/ast.go`'s
  `SignalShorthand`, e.g. `MyDS::Signal1: float32`):
  1. The lexer (`internal/parser/lexer.go`'s `lexIdentifier`) scans the whole
     `DataSource::SignalName` run as a single identifier token, so `SignalName`'s
     own start position was never tracked separately from the token's overall
     start (i.e. `DataSource`'s position).
  2. `index.ProjectTree.addSignalShorthandChild` (`internal/index/index.go`) used
     that same (wrong) shared position — `d.Position` — as the synthesized signal
     child node's `Fragment.ObjectPos`, so clicking on the actual `SignalName` text
     in the editor didn't line up with where the node's match range was computed
     from. Worse, the synthetic `DataSource` field's `ReferenceValue` (`dsVal`)
     was constructed but never passed to `pt.IndexValue`, so — unlike an ordinary
     explicit `DataSource = X` field — it was never registered as a resolvable
     `Reference` at all.
  3. `ProjectTree.queryNode`'s field-matching used the synthetic field's literal
     name length (`len("DataSource")` = 10 chars) to compute its clickable range,
     which doesn't correspond to any real source text (the source text there is
     the actual `DataSource` identifier, e.g. `MyDS`, which is usually a different
     length) — so without a properly resolved `Reference` taking priority
     (`ProjectTree.Query` checks `FileReferences` before falling back to
     `queryNode`), clicking on the `DataSource` portion of the shorthand could
     spuriously match this synthetic field instead. That in turn made
     `lsp.HandleRename`'s `targetField` branch (`internal/lsp/server.go`) rename
     *every* field literally named `"DataSource"` in the enclosing container —
     an incorrect, overly broad edit — instead of performing a correct, targeted
     rename of just that one DataSource node's definition and references.
  Fix: (1) `parser.SignalShorthand` gained a `SignalNamePosition` field, computed
  in `parseSignalShorthand` by shifting the token's start position right past
  `"DataSource::"` (safe because this token can never span multiple lines).
  (2) `addSignalShorthandChild` now uses `d.SignalNamePosition` for the signal
  node's `Fragment.ObjectPos` (so `SignalName` clicks resolve to the right node,
  at the right range), and calls `pt.IndexValue(file, dsVal)` so the `DataSource`
  portion is registered as a proper `Reference`, exactly like an explicit
  `DataSource = X` field — which `Query()` checks *before* falling through to
  `queryNode`'s field-matching, so clicking the `DataSource` text now correctly
  resolves as a reference to that DataSource node, making
  `HandleRename` take the correct `targetNode`/`res.Reference` path (rename just
  that node's definition + all its references) instead of the overly broad
  `targetField` path. Regression tests in `test/signal_shorthand_test.go`:
  `TestSignalShorthandDataSourceAndSignalNameQueryResolveSeparately` (verifies
  `Query()` returns a `Reference` resolving to the right DataSource node for
  clicks on the DataSource portion, and a `Node` result for the SignalName
  portion) and `TestSignalShorthandRenameDataSourceOnlyAffectsThatDataSource`
  (verifies `HandleRename` on the DataSource portion produces exactly 2 edits —
  the DataSource's own definition and this shorthand's reference — leaving an
  unrelated second DataSource and both signal names untouched); both verified to
  fail pre-fix, pass post-fix.

## Ruled-out (false positives — do NOT re-investigate without new evidence)

- ~~Race condition in `ScanDirectory`~~ — disproven; `AddFile` calls are sequential in
  a single consumer loop, only file parsing is concurrent.
- ~~Bug at `internal/index/index.go:554`~~ (`if child.IsConditional && !false {`) —
  disproven; logically correct given `populateNode` only ever runs unconditionally
  in that context.
- ~~UTF-16 offset bug in LSP `offsetAt`~~ (`internal/lsp/server.go:722-747`) —
  disproven; correctly handles astral-plane surrogate pairs
  (`if r >= 0x10000 { col += 2 } else { col++ }`).

## Suggested order of attack

Start with the small, high-confidence, high-value fixes: **#1** (Class reordering),
**#2** (bitwise validator bug), **#4** (debounce doc, bundled with the CLAUDE.md
follow-up). Then tackle **#3** (stdout/stderr) and **#6** (graph perf) as isolated
medium-effort fixes. Leave **#7** (Snapshot.Clone cost) until profiled with real data.
Documentation items (**#11-#14**) can be done in a single pass alongside whichever
code fix touches the same area.
