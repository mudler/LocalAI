# Task 12 report: MCP admin tools for failover chains

## What I implemented

Three MCP admin tools mirroring the Task 9 REST endpoints:

- `list_failover_chains` (read-only) → `GET /api/failover`
- `pin_failover_target` (mutating) → `POST /api/failover/:chain/pin`
- `unpin_failover_target` (mutating) → `DELETE /api/failover/:chain/pin`

Files changed, by layer:

- **Tool identity**: `pkg/mcp/localaitools/tools.go` — added `ToolListFailoverChains` (read-only block), `ToolPinFailoverTarget`/`ToolUnpinFailoverTarget` (mutating block + `mutatingToolNames`).
- **DTOs**: `pkg/mcp/localaitools/dto.go` — added `FailoverTargetInfo` and `FailoverChainInfo` (LLM-facing subset of `failover.TargetStatus`/`failover.ChainStatus`).
- **Interface**: `pkg/mcp/localaitools/client.go` — added `ListFailoverChains`, `PinFailoverTarget`, `UnpinFailoverTarget` to `LocalAIClient`.
- **In-process impl**: `pkg/mcp/localaitools/inproc/client.go` — added `Failover *failover.Manager` field and the three methods (nil-safe: `ListFailoverChains` returns `[]`, Pin/Unpin return `"failover is not running"`).
- **HTTP impl**: `pkg/mcp/localaitools/httpapi/client.go` + `httpapi/routes.go` — added `routeFailover = "/api/failover"` and the three methods, using `c.do`.
- **Tool registration**: new file `pkg/mcp/localaitools/tools_failover.go` (`registerFailoverTools`), wired into `pkg/mcp/localaitools/server.go`.
- **Prompts**: `prompts/20_tools.md` (read-only + mutating one-liners) and `prompts/10_safety.md` (both mutating names added to the confirmation-rule list).
- **Wiring the manager into the in-process client**: `core/application/application.go` and `core/application/startup.go` — see "Adaptation: startup ordering" below; this was the one place I had to deviate from a literal reading of the brief.
- **Tests**: `coverage_test.go`, `server_test.go`, `fakes_test.go`, `inproc/client_test.go`, `httpapi/client_test.go` — all as specified, plus `core/http/endpoints/mcp/localai_assistant_test.go` (see adaptations).

`parity_test.go` was left untouched — the brief didn't specify a new parity spec for failover, and the file's existing specs are all hand-picked equality checks for specific methods (ListGalleries, GallerySearch, ImportModelURI, SystemInfo); it isn't a generic "every method" loop, so nothing there needed updating for the build to stay green.

## Adaptations from the brief (read the real code, deviated where it disagreed)

1. **`jsonResult`/`errorResult` return one value, not three.** The brief's `tools_failover.go` snippet writes `return jsonResult(chains)` as if it were the handler's whole 3-tuple return. The real helpers in `pkg/mcp/localaitools/errors.go` are:
   ```go
   func errorResult(err error) *mcp.CallToolResult
   func jsonResult(v any) *mcp.CallToolResult
   ```
   Matching `tools_aliases.go`'s actual pattern, every handler returns `jsonResult(x), nil, nil` / `errorResult(err), nil, nil` — three explicit values. I used that form throughout `tools_failover.go`.

2. **Mutating tool descriptions reference safety rule 1.** `.agents/localai-assistant-mcp.md`'s checklist says "Mutating tools must reference safety rule 1 in the description," and `tools_aliases.go`'s `set_alias` does this ("Requires user confirmation per safety rule 1."). The brief's descriptions for `pin_failover_target`/`unpin_failover_target` didn't include this phrase, so I added it to match the established convention and the checklist.

3. **Startup ordering: `assistantClient.Failover` can't be set where the brief implies.** The brief says to set the field "where the client is constructed (from `application.FailoverManager()`)." I found that construction site (`core/application/application.go`'s `start()`, called from `core/application/startup.go`'s `New()` at line 74) — but `application.failoverManager` is only built later in the same `New()` function, at line 259, **after** `start()` (and therefore the assistant-client construction) has already returned. Calling `a.FailoverManager()` inside `start()` would have captured a permanent `nil`.

   Fix: added an `assistantClient *localaiInproc.Client` field to `Application` (set in `start()` when the assistant client is built), then in `startup.go`, right after `application.failoverManager = failover.New(...)`, added:
   ```go
   if application.assistantClient != nil {
       application.assistantClient.Failover = application.failoverManager
   }
   ```
   This is safe because `assistantClient` is a pointer already captured by value inside the `LocalAIClient` interface passed to `holder.Initialize()` — mutating a field on it after the fact is visible through the interface. Verified with `go test ./core/application/...` and `go test ./core/http/endpoints/mcp/...`.

4. **`stubClient` in `core/http/endpoints/mcp/localai_assistant_test.go`** implements `localaitools.LocalAIClient` for that package's own tests and isn't in the brief's file list, but the interface change broke its build. Added the three stub methods (empty list, nil errors) to keep it compiling — same pattern as its existing stubs for `GetRouterCorpusStats` etc.

5. **inproc failover test fixture**: the brief says to "build a `failover.Manager` over a small in-memory source with one chain." `failover.ConfigSource` (`GetModelConfig`/`GetAllModelsConfigs`) is exported, but the concrete fake used by `core/services/failover`'s own tests (`fakeSource`, `chainCfg`, `t()`) is unexported and package-local, so I wrote a minimal `fakeFailoverSource` directly in `inproc/client_test.go` implementing the same two-method interface over a `map[string]config.ModelConfig`, seeded with a `chain` config (`config.FailoverConfig{Targets: [...]}`) plus two local targets `a`/`b`.

## TDD evidence

**RED** — after writing all test-file changes (`coverage_test.go`, `server_test.go`, `fakes_test.go`, plus new specs in `inproc/client_test.go` and `httpapi/client_test.go` were added later, see below), I temporarily reverted the implementation files (`git stash push` on `tools.go`, `dto.go`, `client.go`, `server.go`, `inproc/client.go`, `httpapi/client.go`, `httpapi/routes.go`, prompts, and the two `core/application` files; moved `tools_failover.go` out of the package) and ran:

```
$ go test ./pkg/mcp/localaitools/... 2>&1 | tail -30
# github.com/mudler/LocalAI/pkg/mcp/localaitools [github.com/mudler/LocalAI/pkg/mcp/localaitools.test]
pkg/mcp/localaitools/fakes_test.go:62:32: undefined: FailoverChainInfo
pkg/mcp/localaitools/fakes_test.go:400:63: undefined: FailoverChainInfo
pkg/mcp/localaitools/fakes_test.go:405:11: undefined: FailoverChainInfo
pkg/mcp/localaitools/coverage_test.go:49:2: undefined: ToolListFailoverChains
pkg/mcp/localaitools/coverage_test.go:71:2: undefined: ToolPinFailoverTarget
pkg/mcp/localaitools/coverage_test.go:72:2: undefined: ToolUnpinFailoverTarget
pkg/mcp/localaitools/server_test.go:95:2: undefined: ToolListFailoverChains
pkg/mcp/localaitools/server_test.go:159:4: undefined: ToolListFailoverChains
pkg/mcp/localaitools/server_test.go:160:4: undefined: ToolPinFailoverTarget
pkg/mcp/localaitools/server_test.go:161:4: undefined: ToolUnpinFailoverTarget
pkg/mcp/localaitools/server_test.go:161:4: too many errors
FAIL	github.com/mudler/LocalAI/pkg/mcp/localaitools [build failed]
ok  	github.com/mudler/LocalAI/pkg/mcp/localaitools/httpapi	0.035s
ok  	github.com/mudler/LocalAI/pkg/mcp/localaitools/inproc	0.167s
```
This matches the brief's Step 1 expectation ("Expected: compile failure"). I then `git stash pop` and restored `tools_failover.go` to get back to the implemented state.

**GREEN** — after restoring the implementation and adding the remaining `inproc/client_test.go` / `httpapi/client_test.go` specs:

```
$ go test ./pkg/mcp/localaitools/... -count=1 -v 2>&1 | grep -E "SUCCESS|FAIL|ok  "
SUCCESS! -- 53 Passed | 0 Failed | 0 Pending | 0 Skipped
ok  	github.com/mudler/LocalAI/pkg/mcp/localaitools	0.133s
SUCCESS! -- 24 Passed | 0 Failed | 0 Pending | 0 Skipped
ok  	github.com/mudler/LocalAI/pkg/mcp/localaitools/httpapi	0.037s
SUCCESS! -- 16 Passed | 0 Failed | 0 Pending | 0 Skipped
ok  	github.com/mudler/LocalAI/pkg/mcp/localaitools/inproc	0.171s
```

Additional verification (build scope per implementer-rules, plus the two packages touched indirectly by the interface change):

```
$ go build ./core/... ./pkg/mcp/... ./tests/...
(clean, no output)

$ go test ./pkg/mcp/localaitools/... ./core/application/... ./core/http/endpoints/mcp/... -count=1
ok  	github.com/mudler/LocalAI/pkg/mcp/localaitools	0.155s
ok  	github.com/mudler/LocalAI/pkg/mcp/localaitools/httpapi	0.045s
ok  	github.com/mudler/LocalAI/pkg/mcp/localaitools/inproc	0.176s
ok  	github.com/mudler/LocalAI/core/application	0.265s
ok  	github.com/mudler/LocalAI/core/http/endpoints/mcp	0.237s

$ gofmt -l <all touched .go files>
(empty — clean)

$ go vet ./core/application/... ./pkg/mcp/...
(clean, no output)
```

## Files changed

- `pkg/mcp/localaitools/tools.go`
- `pkg/mcp/localaitools/dto.go`
- `pkg/mcp/localaitools/client.go`
- `pkg/mcp/localaitools/server.go`
- `pkg/mcp/localaitools/tools_failover.go` (new)
- `pkg/mcp/localaitools/inproc/client.go`
- `pkg/mcp/localaitools/httpapi/client.go`
- `pkg/mcp/localaitools/httpapi/routes.go`
- `pkg/mcp/localaitools/prompts/20_tools.md`
- `pkg/mcp/localaitools/prompts/10_safety.md`
- `pkg/mcp/localaitools/coverage_test.go`
- `pkg/mcp/localaitools/server_test.go`
- `pkg/mcp/localaitools/fakes_test.go`
- `pkg/mcp/localaitools/inproc/client_test.go`
- `pkg/mcp/localaitools/httpapi/client_test.go`
- `core/application/application.go`
- `core/application/startup.go`
- `core/http/endpoints/mcp/localai_assistant_test.go`

## Self-review

- Completeness: all three tools registered, gated correctly (`ToolListFailoverChains` in the read-only catalog; both mutating tools skipped when `Options.DisableMutating`), both client implementations covered, prompts updated, safety-rule coverage test (`TestPromptsContainSafetyAnchors`'s "names every mutating tool" spec) passes automatically since it reads `mutatingToolNames`.
- Quality/YAGNI: DTOs intentionally drop `ConsecutiveOK`/`LastProbe`/`ActiveSince` — internal probe bookkeeping the LLM has no use for when deciding to pin/unpin; documented why in the doc comment.
- Nil-safety: both inproc failover methods and the httpapi Pin/Unpin exercise the "no failover configured" path in tests (inproc has explicit specs for it; httpapi's behavior when unconfigured is identical to any other client error — REST returns 404, `c.do` surfaces `*HTTPError`, no new code path needed there).
- Existing patterns followed: constant grouping/comments, `errorResult`/`jsonResult` triple-return, `c.do` signature, `url.PathEscape` on the chain path segment, fake-client recording pattern, Ginkgo `Describe`/`It` structure matching the alias specs.
- Pristine output: `gofmt -l` and `go vet` clean across all touched files.

## Concerns

- None blocking. The startup-ordering fix (adaptation 3) is the only piece that goes beyond a single-file, mechanical change — it touches two `core/application` files instead of the "one field set inline" the brief describes. I verified it with both `core/application` and `core/http/endpoints/mcp` package tests, and confirmed via read of `startup.go` that `New()` is the sole caller of `start()` and that `failoverManager` is not read anywhere between `start()` and its own assignment, so there's no other place relying on it being nil momentarily.
