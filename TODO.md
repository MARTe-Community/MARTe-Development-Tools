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

- [ ] **#7 LSP `Snapshot.Clone()` cost on every edit** — MEDIUM-HIGH severity
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
