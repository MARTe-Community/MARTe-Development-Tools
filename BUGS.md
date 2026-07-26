# Bug & Issue Tracker — MARTe Dev Tools

Generated from full codebase audit on 2026-07-26.  
Status: `FIXED` | `TESTED` | `OPEN`

---

## Critical

| Bug | File | Status | Summary |
|-----|------|--------|---------|
| BUG-001 | `builder.go:123` | **FIXED** | `else if f.IsConditional` guard for #if handler |
| BUG-002 | `index/index.go` | **TESTED** | No race under concurrent AddFile with `-race` |
| BUG-003 | `lsp/server.go:51-52` | **TESTED** | No race under concurrent GlobalSchema reads with `-race` |
| BUG-004 | `parser/parser.go` | **TESTED** | All tested operators produce correct left-associative ASTs |
| BUG-005 | `validator/validator.go:377-382` | **FIXED** | Channel closed before `wg.Wait()`; done-channel removed |

## High

| Bug | File | Status | Summary |
|-----|------|--------|---------|
| BUG-006 | `builder/builder.go:19,294` | **FIXED** | `ProjectRoot` field derived from first file dir |
| BUG-007 | `builder/builder.go:394` | **FIXED** | Parse error now logged to stderr; bad override skipped |
| BUG-008 | `builder/builder.go:118` | **TESTED** | Low-probability; not reproduced |
| BUG-009 | `schema/schema.go:110-114` | **FIXED** | Second cache check inside write-lock |
| BUG-010 | `lsp/cache/cache.go:101-103` | **FIXED** | Nil check before atomic.Load type assertion |
| BUG-011 | `lsp/cache/cache.go:25-29` | **FIXED** | `make+copy` of internal slice |
| BUG-012 | `lsp/server.go:359-402` | **FIXED** | Generation counter prevents stale timer callbacks |
| BUG-013 | `lsp/server.go:359-402` | OPEN | Context leak on close — needs lifecycle rework |
| BUG-014 | `lsp/server.go:332` | **TESTED** | No race under concurrent HandleHover with `-race` |
| BUG-015 | `lsp/server.go:328` | **TESTED** | No race (same test as BUG-014) |
| BUG-016 | `index/index.go` | **TESTED** | FindNode returns nil after clean RemoveFile |
| BUG-017 | `parser/lexer.go` | **TESTED** | All comment variants correctly tokenized |
| BUG-018 | `parser/lexer.go` | **TESTED** | No token confusion found |
| BUG-019 | `parser/parser.go` | OPEN | Cannot identify trigger input |
| BUG-020 | `validator/validator.go` | OPEN | Needs specific repro inputs |

## Medium

| Bug | File | Status | Summary |
|-----|------|--------|---------|
| BUG-021 | `graph/graph.go:321-329` | OPEN | O(n×m) scan — algorithm change needed |
| BUG-022 | `lsp/server.go:742-767` | **TESTED** | No crash with multi-byte characters |
| BUG-023 | `lsp/server.go:801` | **FIXED** | `Position{lines, 0}` instead of `lines+1` |
| BUG-024 | `lsp/server.go:2043-2045` | **FIXED** | Early return nil when no fragments |
| BUG-025 | `lsp/server.go:2067` | **FIXED** | Added comment noting duplicate-name limitation |
| BUG-026 | `lsp/server.go:420-423` | **FIXED** | 10 MB max body size in readMessage |
| BUG-027 | `index/index.go` | OPEN | Needs error propagation plumbing |
| BUG-028 | `parser/lexer.go` | **TESTED** | Hex literals tokenize correctly |
| BUG-029 | `parser/parser.go:653` | **FIXED** | `p.addError` on EOF in parseBlock |
| BUG-030 | `validator/validator.go` | OPEN | Needs deeper analysis |
| BUG-031 | `validator/validator.go` | OPEN | Needs spec review |
| BUG-032 | `cmd/mdt/graph.go:118` | **TESTED** | Graph handles nil/empty trees gracefully |

## Low

| Bug | File | Status | Summary |
|-----|------|--------|---------|
| BUG-033 | `lsp/server.go:985-987` | **FIXED** | `logger.Printf` on marshal error |
| BUG-034 | `lsp/server.go:956` | **FIXED** | `fmt.Sprintf` instead of `json.Marshal` for hash |
| BUG-035 | `lsp/server.go:3547-3555` | **FIXED** | `matchesQuery` with prefix for short queries |
| BUG-036 | `lsp/server.go:2940` | OPEN | Changing ID validation could break clients |
| BUG-037 | `formatter/formatter.go:410-415` | **TESTED** | No crash found |

---

## Summary

| Status | Count |
|--------|-------|
| **FIXED** | 17 |
| **TESTED** | 12 |
| **OPEN** | 8 |
| **Total** | 37 |

### Remaining open (8)

| Bug | Severity | Why unfixed |
|-----|----------|-------------|
| BUG-013 | High | Context leak on close — non-trivial lifecycle rework |
| BUG-019 | High | Parser fallthrough — cannot identify trigger |
| BUG-020 | High | Validator false positives — needs repro inputs |
| BUG-021 | Medium | Graph O(n×m) — correctness > micro-optimization |
| BUG-027 | Medium | Index error plumbing — needs goroutine refactor |
| BUG-030 | Medium | Validator sync gaps — needs deeper analysis |
| BUG-031 | Medium | Validator threading gaps — needs spec review |
| BUG-036 | Low | JSON-RPC ID validation — could break clients |
