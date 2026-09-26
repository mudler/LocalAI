# Model Failover Chains Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A model config can declare an ordered `failover` chain of target models; LocalAI serves each request from the highest-priority healthy target, retries uncommitted failures on the next target, probes targets, fails back with hysteresis, and publishes switch events.

**Architecture:** A new `core/services/failover` package holds a `Manager` (per-target health state machine, per-chain active target, probes, event bus). The HTTP request middleware resolves a chain to a target the way it resolves an alias, and a retry wrapper inside `SetModelAndConfig` re-runs the request on the next target while the response is uncommitted. Realtime pipeline stages resolve chains per call through `Manager.Do`. REST, SSE, a realtime server event, metrics and MCP tools expose the state.

**Tech Stack:** Go, echo v4, Ginkgo v2 + Gomega, OpenTelemetry metrics, gRPC backend interface (`pkg/grpc`).

**Spec:** `docs/superpowers/specs/2026-09-26-model-failover-chains-design.md`

## Global Constraints

- Worktree: `/home/mudler/_git/LocalAI/.wt/failover-chains`, branch `feat/failover-chains`. All paths below are relative to it.
- Commit trailer: `Assisted-by: Claude:claude-opus-5-5`. Never add `Co-Authored-By` or `Signed-off-by` (the human adds the DCO sign-off). Subjects use the repo's conventional style, for example `feat(failover): ...`.
- `docs/superpowers/` is excluded by the local `.git/info/exclude`; add files there with `git add -f`.
- Logging: `github.com/mudler/xlog`. Use `any`, never `interface{}`. Comments explain why, not what.
- Defaults, exactly: probe interval `15s`, probe timeout `5s`, trip errors `1`, trip window `30s`, recovery probes `3`, min dwell `60s`.
- Response headers, exactly: `X-LocalAI-Served-Model`, `X-LocalAI-Failover` (values `fallback`, `degraded`).
- Event names, exactly: SSE `snapshot`, `chain.switched`, `target.state`; realtime `localai.model.failover`. Reasons: `trip`, `recovery`, `manual`, `degraded`, `missing`, `initial`.
- Metrics, exactly: `localai_failover_switches_total{chain,from,to,reason}`, `localai_failover_target_up{target}`.
- Coverage baseline (`coverage-baseline.txt`, 54.2) must not go down. Never edit the baseline.
- Docs change ships in the same PR (`docs/content/features/model-failover.md`).
- Tests need generated protos: run `make protogen-go` once in the worktree before the first `go test`. E2E tests need `make build-mock-backend`.
- Run a package's tests with: `go run github.com/onsi/ginkgo/v2/ginkgo -v ./<pkg>` (or `go test ./<pkg>/...`).

## Review Focus

1. A handler that mutates the parsed request on attempt 1 must not leak that mutation into attempt 2: each attempt re-binds from the replayed body (Task 8, spec "gives each attempt a fresh request").
2. A client that disconnects mid-request must not trip the target or trigger a retry (Task 8, spec "does not retry when the client cancelled").
3. A 4xx (bad request, context overflow) must not retry and must not trip the target (Task 8, spec "does not retry or trip on 4xx").
4. A degraded chain (all targets down) must try every target in priority order and return the last target's error (Task 8, spec "degraded").
5. A multipart upload (transcription) must be readable again by the second attempt (Task 8, spec "replays a multipart body").

---

## File Structure

| File | Responsibility |
|---|---|
| `core/config/model_config_failover.go` (new) | `FailoverConfig` types, defaults, `IsFailover`, `validateFailover`, `WarmFailoverTargets` |
| `core/config/model_config.go` (modify) | `Failover` field, call `validateFailover` from `Validate`, `ProxyConfig.ResolveAPIKey` |
| `core/config/model_config_loader.go` (modify) | `ValidateFailoverTargets`, load-time pruning and usecase warning |
| `core/config/meta/registry.go`, `types.go` (modify) | `failover` section and field metadata |
| `core/services/failover/types.go` (new) | states, reasons, events, status DTOs, `KindOf`, `MergePinned` |
| `core/services/failover/classify.go` (new) | `IsRetryable` |
| `core/services/failover/manager.go` (new) | `Manager`: sync, state machine, plan/attempt, pin, events, status, `Do` |
| `core/services/failover/schedule.go` (new) | `Prober` interface, `Run`, `Tick`, probe scheduling |
| `core/services/failover/prober.go` (new) | `DefaultProber`: remote HTTP and local gRPC probes |
| `core/services/failover/metrics.go` (new) | OTel counter and gauge |
| `core/services/failover/trace.go` (new) | `RecordAttemptTrace` |
| `core/trace/backend_trace.go` (modify) | `BackendTraceFailover` type |
| `core/application/{application.go,startup.go,watchdog.go,failover.go}` | wiring, warm pinning/preload |
| `core/http/middleware/failover.go` (new) | chain resolution, retry wrapper, writer, headers |
| `core/http/middleware/request.go` (modify) | call resolution, wrap `SetModelAndConfig` |
| `core/http/app.go` (modify) | `SetFailoverManager` |
| `core/http/endpoints/localai/failover.go` (new) | REST + SSE handlers |
| `core/http/routes/localai.go` (modify) | routes |
| `core/http/endpoints/localai/api_instructions.go` (modify) | instruction entry |
| `core/http/endpoints/openai/realtime_model.go`, `realtime.go`, `realtime_failover.go` (new), `types/failover.go` (new), `types/server_events.go` | realtime per-call resolution and events |
| `pkg/mcp/localaitools/...` | MCP tools |
| `tests/e2e/...` | e2e specs, mock-backend load-failure trigger, fake upstream `/v1/models` |
| `docs/content/features/model-failover.md` (new) + cross-links | docs |

---

### Task 1: Config schema, per-config validation, field metadata

**Files:**
- Create: `core/config/model_config_failover.go`
- Modify: `core/config/model_config.go` (field next to `Alias` at line ~76; `Validate()` at line ~1593, before the alias block at ~1650)
- Modify: `core/config/meta/types.go` (`DefaultSections()`), `core/config/meta/registry.go` (next to the `alias` entry at ~414)
- Test: `core/config/model_config_failover_test.go`, `core/config/meta/registry_test.go`

**Interfaces:**
- Produces: `config.FailoverConfig`, `config.FailoverTarget`, `(ModelConfig).IsFailover() bool`, `(FailoverConfig).ProbeInterval()/ProbeTimeout()/TripWindow()/MinDwell() time.Duration`, `(FailoverConfig).TripErrors()/RecoveryProbes() int`, `(ModelConfig).WarmFailoverTargets() []string`, field `ModelConfig.Failover *FailoverConfig`.

- [ ] **Step 1: Write the failing tests**

`core/config/model_config_failover_test.go` (package `config`, like `model_config_test.go`):

```go
package config

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("ModelConfig failover", func() {
	chain := func(targets ...string) ModelConfig {
		c := ModelConfig{Name: "chain", Failover: &FailoverConfig{}}
		for _, t := range targets {
			c.Failover.Targets = append(c.Failover.Targets, FailoverTarget{Model: t})
		}
		return c
	}

	It("parses the YAML block and applies defaults", func() {
		var c ModelConfig
		Expect(yaml.Unmarshal([]byte(`
name: assistant-llm
failover:
  targets:
    - model: argus-llm
    - model: gemma-local
      warm: true
  recovery:
    probes: 5
`), &c)).To(Succeed())
		Expect(c.IsFailover()).To(BeTrue())
		Expect(c.Failover.Targets).To(Equal([]FailoverTarget{{Model: "argus-llm"}, {Model: "gemma-local", Warm: true}}))
		Expect(c.Failover.ProbeInterval()).To(Equal(15 * time.Second))
		Expect(c.Failover.ProbeTimeout()).To(Equal(5 * time.Second))
		Expect(c.Failover.TripErrors()).To(Equal(1))
		Expect(c.Failover.TripWindow()).To(Equal(30 * time.Second))
		Expect(c.Failover.RecoveryProbes()).To(Equal(5))
		Expect(c.Failover.MinDwell()).To(Equal(60 * time.Second))
		Expect(c.WarmFailoverTargets()).To(Equal([]string{"gemma-local"}))
	})

	It("accepts a valid chain", func() {
		c := chain("a", "b")
		ok, err := c.Validate()
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
	})

	DescribeTable("rejects invalid chains",
		func(mutate func(*ModelConfig), want string) {
			c := chain("a", "b")
			mutate(&c)
			ok, err := c.Validate()
			Expect(ok).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring(want)))
		},
		Entry("alias and failover", func(c *ModelConfig) { c.Alias = "x" }, "both alias and failover"),
		Entry("backend set", func(c *ModelConfig) { c.Backend = "llama-cpp" }, "must not set backend"),
		Entry("one target", func(c *ModelConfig) { c.Failover.Targets = c.Failover.Targets[:1] }, "at least 2 targets"),
		Entry("empty target", func(c *ModelConfig) { c.Failover.Targets[1].Model = "" }, "no model"),
		Entry("self target", func(c *ModelConfig) { c.Failover.Targets[1].Model = "chain" }, "cannot list itself"),
		Entry("duplicate target", func(c *ModelConfig) { c.Failover.Targets[1].Model = "a" }, "twice"),
		Entry("bad duration", func(c *ModelConfig) { c.Failover.Probe.Interval = "soon" }, "invalid probe.interval"),
		Entry("negative errors", func(c *ModelConfig) { c.Failover.Trip.Errors = -1 }, "trip.errors"),
		Entry("no name", func(c *ModelConfig) { c.Name = "" }, "requires a name"),
	)
})
```

In `core/config/meta/registry_test.go`, next to the alias assertions (lines 13-31), add:

```go
	It("registers the failover section", func() {
		reg := meta.DefaultRegistry()
		Expect(reg).To(HaveKey("failover.targets"))
		Expect(reg["failover.targets"].Section).To(Equal("failover"))
		var ids []string
		for _, s := range meta.DefaultSections() {
			ids = append(ids, s.ID)
		}
		Expect(ids).To(ContainElement("failover"))
	})
```

(Match the surrounding style: if the file uses `It` inside an existing `Describe`, put this `It` there.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./core/config/... 2>&1 | tail -20`
Expected: compile failure, `undefined: FailoverConfig`.

- [ ] **Step 3: Implement the config types**

`core/config/model_config_failover.go`:

```go
package config

import (
	"fmt"
	"time"
)

// FailoverConfig turns a model config into a failover chain: requests for the
// chain name are served by its highest-priority healthy target. See
// core/services/failover for the runtime side.
type FailoverConfig struct {
	Targets  []FailoverTarget `yaml:"targets" json:"targets"`
	Probe    FailoverProbe    `yaml:"probe,omitempty" json:"probe,omitempty"`
	Trip     FailoverTrip     `yaml:"trip,omitempty" json:"trip,omitempty"`
	Recovery FailoverRecovery `yaml:"recovery,omitempty" json:"recovery,omitempty"`
}

type FailoverTarget struct {
	Model string `yaml:"model" json:"model"`
	// Warm keeps a local target loaded and exempt from eviction, so a switch
	// does not wait for a cold load.
	Warm bool `yaml:"warm,omitempty" json:"warm,omitempty"`
}

type FailoverProbe struct {
	Interval string `yaml:"interval,omitempty" json:"interval,omitempty"`
	Timeout  string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

type FailoverTrip struct {
	Errors int    `yaml:"errors,omitempty" json:"errors,omitempty"`
	Window string `yaml:"window,omitempty" json:"window,omitempty"`
}

type FailoverRecovery struct {
	Probes   int    `yaml:"probes,omitempty" json:"probes,omitempty"`
	MinDwell string `yaml:"min_dwell,omitempty" json:"min_dwell,omitempty"`
}

const (
	DefaultFailoverProbeInterval  = 15 * time.Second
	DefaultFailoverProbeTimeout   = 5 * time.Second
	DefaultFailoverTripErrors     = 1
	DefaultFailoverTripWindow     = 30 * time.Second
	DefaultFailoverRecoveryProbes = 3
	DefaultFailoverMinDwell       = 60 * time.Second
)

// IsFailover reports whether this config is a failover chain.
func (c ModelConfig) IsFailover() bool { return c.Failover != nil }

func (f FailoverConfig) ProbeInterval() time.Duration {
	return durationOr(f.Probe.Interval, DefaultFailoverProbeInterval)
}
func (f FailoverConfig) ProbeTimeout() time.Duration {
	return durationOr(f.Probe.Timeout, DefaultFailoverProbeTimeout)
}
func (f FailoverConfig) TripWindow() time.Duration {
	return durationOr(f.Trip.Window, DefaultFailoverTripWindow)
}
func (f FailoverConfig) MinDwell() time.Duration {
	return durationOr(f.Recovery.MinDwell, DefaultFailoverMinDwell)
}
func (f FailoverConfig) TripErrors() int {
	if f.Trip.Errors <= 0 {
		return DefaultFailoverTripErrors
	}
	return f.Trip.Errors
}
func (f FailoverConfig) RecoveryProbes() int {
	if f.Recovery.Probes <= 0 {
		return DefaultFailoverRecoveryProbes
	}
	return f.Recovery.Probes
}

// WarmFailoverTargets returns the targets marked warm, in chain order.
func (c ModelConfig) WarmFailoverTargets() []string {
	if c.Failover == nil {
		return nil
	}
	var out []string
	for _, t := range c.Failover.Targets {
		if t.Warm {
			out = append(out, t.Model)
		}
	}
	return out
}

func durationOr(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// validateFailover checks what a chain can check without other configs.
// Target existence is checked by ModelConfigLoader.ValidateFailoverTargets.
func (c *ModelConfig) validateFailover() error {
	if c.Name == "" {
		return fmt.Errorf("failover config requires a name")
	}
	if c.IsAlias() {
		return fmt.Errorf("model %q cannot set both alias and failover", c.Name)
	}
	if c.Backend != "" || c.Model != "" {
		return fmt.Errorf("failover config %q must not set backend or parameters.model: a chain is a pure redirect", c.Name)
	}
	f := c.Failover
	if len(f.Targets) < 2 {
		return fmt.Errorf("failover chain %q needs at least 2 targets", c.Name)
	}
	seen := map[string]bool{}
	for _, t := range f.Targets {
		switch {
		case t.Model == "":
			return fmt.Errorf("failover chain %q has a target with no model", c.Name)
		case t.Model == c.Name:
			return fmt.Errorf("failover chain %q cannot list itself", c.Name)
		case seen[t.Model]:
			return fmt.Errorf("failover chain %q lists %q twice", c.Name, t.Model)
		}
		seen[t.Model] = true
	}
	for key, v := range map[string]string{
		"probe.interval":     f.Probe.Interval,
		"probe.timeout":      f.Probe.Timeout,
		"trip.window":        f.Trip.Window,
		"recovery.min_dwell": f.Recovery.MinDwell,
	} {
		if v == "" {
			continue
		}
		if d, err := time.ParseDuration(v); err != nil || d <= 0 {
			return fmt.Errorf("failover chain %q: invalid %s %q", c.Name, key, v)
		}
	}
	if f.Trip.Errors < 0 {
		return fmt.Errorf("failover chain %q: trip.errors must not be negative", c.Name)
	}
	if f.Recovery.Probes < 0 {
		return fmt.Errorf("failover chain %q: recovery.probes must not be negative", c.Name)
	}
	return nil
}
```

In `core/config/model_config.go`, add the field directly after `Alias` (line ~76):

```go
	// Failover makes this config a failover chain over other models. Like an
	// alias it has no backend of its own.
	Failover *FailoverConfig `yaml:"failover,omitempty" json:"failover,omitempty"`
```

In `Validate()`, directly before the `if c.IsAlias() {` block (line ~1650), add:

```go
	if c.IsFailover() {
		if err := c.validateFailover(); err != nil {
			return false, err
		}
		return true, nil
	}
```

If `Validate()` rejects configs without a backend earlier than line 1650 (the artifact check at ~1619 applies to aliases), move this block above that point so a chain returns before any backend-specific check.

- [ ] **Step 4: Register field metadata**

In `core/config/meta/types.go` `DefaultSections()`, add after the `alias` line:

```go
		{ID: "failover", Label: "Failover", Icon: "shuffle", Order: 6},
```

(If `shuffle` is not an icon used elsewhere in `DefaultSections()`, reuse `git-merge`.)

In `core/config/meta/registry.go`, after the `alias` entry, add:

```go
		// --- Failover ---
		"failover.targets": {
			Section:     "failover",
			Label:       "Failover targets",
			Description: "Ordered list of models that serve this chain. The first healthy target serves each request; later targets take over when it fails. Mark a local target warm to keep it loaded.",
			Component:   "json-editor",
			Order:       0,
		},
		"failover.probe.interval": {
			Section: "failover", Label: "Probe interval", Component: "input", Order: 1, Advanced: true,
			Description: "How often an idle target is checked, as a duration (default 15s).", Placeholder: "15s",
		},
		"failover.probe.timeout": {
			Section: "failover", Label: "Probe timeout", Component: "input", Order: 2, Advanced: true,
			Description: "How long one probe may take (default 5s).", Placeholder: "5s",
		},
		"failover.trip.errors": {
			Section: "failover", Label: "Errors to trip", Component: "number", Order: 3, Advanced: true,
			Description: "Failures within the trip window that mark a target down (default 1).",
		},
		"failover.trip.window": {
			Section: "failover", Label: "Trip window", Component: "input", Order: 4, Advanced: true,
			Description: "Window in which failures are counted (default 30s).", Placeholder: "30s",
		},
		"failover.recovery.probes": {
			Section: "failover", Label: "Recovery probes", Component: "number", Order: 5, Advanced: true,
			Description: "Consecutive real test requests a target must pass before it is used again (default 3).",
		},
		"failover.recovery.min_dwell": {
			Section: "failover", Label: "Minimum time on fallback", Component: "input", Order: 6, Advanced: true,
			Description: "Minimum time on a lower target before traffic moves back to a recovered higher one (default 60s).", Placeholder: "60s",
		},
```

Run `go test ./core/config/meta/...`. `TestAllFieldsHaveRegistryEntries` lists every reflected path that has no entry. If it names paths different from the seven above (for example `failover.targets[].model`), register those exact paths with matching labels in the same section, and remove entries it reports as unknown.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./core/config/... 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add core/config
git commit -m "feat(config): add failover chain block to model configs

A chain is a model config with an ordered list of target models, probe,
trip and recovery settings. Like an alias it has no backend.

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 2: Cross-config validation in the loader and admin paths

**Files:**
- Modify: `core/config/model_config_loader.go` (new methods next to `ValidateAliasTarget` at ~487; load-time check after the alias check at ~824-840)
- Modify: `core/services/modeladmin/config.go` (next to the `ValidateAliasTarget` calls at ~169 and ~286), `core/http/endpoints/localai/import_model.go` (~186)
- Test: `core/config/model_config_loader_test.go`

**Interfaces:**
- Consumes: Task 1 types.
- Produces: `(*ModelConfigLoader).ValidateFailoverTargets(cfg *ModelConfig) error`, `(*ModelConfigLoader).FailoverTargetsShareUsecase(cfg *ModelConfig) bool`.

- [ ] **Step 1: Write the failing tests**

Append to `core/config/model_config_loader_test.go` (the file seeds `loader.configs` directly, see line ~304):

```go
var _ = Describe("ModelConfigLoader failover validation", func() {
	var loader *ModelConfigLoader
	chain := func(targets ...string) *ModelConfig {
		c := &ModelConfig{Name: "chain", Failover: &FailoverConfig{}}
		for _, t := range targets {
			c.Failover.Targets = append(c.Failover.Targets, FailoverTarget{Model: t})
		}
		return c
	}

	BeforeEach(func() {
		loader = NewModelConfigLoader("")
		loader.configs["a"] = ModelConfig{Name: "a", Backend: "llama-cpp", KnownUsecaseStrings: []string{"chat"}}
		loader.configs["b"] = ModelConfig{Name: "b", Backend: "llama-cpp", KnownUsecaseStrings: []string{"chat"}}
		loader.configs["tts"] = ModelConfig{Name: "tts", Backend: "piper", KnownUsecaseStrings: []string{"tts"}}
		loader.configs["alias-b"] = ModelConfig{Name: "alias-b", Alias: "b"}
		loader.configs["other-chain"] = *chain("a", "b")
		loader.configs["alias-chain"] = ModelConfig{Name: "alias-chain", Alias: "other-chain"}
		for k, c := range loader.configs {
			c.KnownUsecases = GetUsecasesFromYAML(c.KnownUsecaseStrings)
			loader.configs[k] = c
		}
	})

	It("accepts existing targets and alias targets", func() {
		Expect(loader.ValidateFailoverTargets(chain("a", "alias-b"))).To(Succeed())
	})
	It("rejects a missing target", func() {
		Expect(loader.ValidateFailoverTargets(chain("a", "nope"))).To(MatchError(ContainSubstring("does not exist")))
	})
	It("rejects a nested chain, directly or through an alias", func() {
		Expect(loader.ValidateFailoverTargets(chain("a", "other-chain"))).To(MatchError(ContainSubstring("chains do not nest")))
		Expect(loader.ValidateFailoverTargets(chain("a", "alias-chain"))).To(MatchError(ContainSubstring("chains do not nest")))
	})
	It("reports whether targets share a usecase", func() {
		Expect(loader.FailoverTargetsShareUsecase(chain("a", "b"))).To(BeTrue())
		Expect(loader.FailoverTargetsShareUsecase(chain("a", "tts"))).To(BeFalse())
	})
})
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./core/config/... 2>&1 | tail -5`
Expected: `undefined: ... ValidateFailoverTargets`.

- [ ] **Step 3: Implement**

In `core/config/model_config_loader.go`:

```go
// failoverUsecases are the single usecases a chain can share. Checking one
// flag at a time avoids treating "chat+tts" and "tts" as unrelated.
var failoverUsecases = []ModelConfigUsecase{
	FLAG_CHAT, FLAG_COMPLETION, FLAG_EMBEDDINGS, FLAG_RERANK, FLAG_IMAGE,
	FLAG_TRANSCRIPT, FLAG_TTS, FLAG_SOUND_GENERATION, FLAG_VAD, FLAG_VIDEO,
	FLAG_SOUND_CLASSIFICATION,
}

// ValidateFailoverTargets checks that every target of a chain exists and is
// not itself a chain. Alias targets are allowed and resolve one hop.
func (bcl *ModelConfigLoader) ValidateFailoverTargets(cfg *ModelConfig) error {
	return validateFailoverTargets(cfg, bcl.GetModelConfig)
}

// FailoverTargetsShareUsecase reports whether all targets of a chain have at
// least one usecase in common. A false result is only a warning: usecases are
// often inferred.
func (bcl *ModelConfigLoader) FailoverTargetsShareUsecase(cfg *ModelConfig) bool {
	return failoverTargetsShareUsecase(cfg, bcl.GetModelConfig)
}

func validateFailoverTargets(cfg *ModelConfig, lookup func(string) (ModelConfig, bool)) error {
	if cfg == nil || !cfg.IsFailover() {
		return nil
	}
	for _, t := range cfg.Failover.Targets {
		target, ok := lookup(t.Model)
		if !ok {
			return fmt.Errorf("failover chain %q: target %q does not exist", cfg.Name, t.Model)
		}
		if target.IsAlias() {
			if resolved, ok := lookup(target.Alias); ok {
				target = resolved
			}
		}
		if target.IsFailover() {
			return fmt.Errorf("failover chain %q: target %q is a chain (chains do not nest)", cfg.Name, t.Model)
		}
	}
	return nil
}

func failoverTargetsShareUsecase(cfg *ModelConfig, lookup func(string) (ModelConfig, bool)) bool {
	if cfg == nil || !cfg.IsFailover() {
		return true
	}
	var targets []ModelConfig
	for _, t := range cfg.Failover.Targets {
		target, ok := lookup(t.Model)
		if !ok {
			return true // missing targets are reported by validateFailoverTargets
		}
		if target.IsAlias() {
			if resolved, ok := lookup(target.Alias); ok {
				target = resolved
			}
		}
		targets = append(targets, target)
	}
	for _, u := range failoverUsecases {
		all := true
		for i := range targets {
			if !targets[i].HasUsecases(u) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}
```

In `loadModelConfigsFromPath`, directly after the alias warning loop (~824-840), add a pass over `bcl.configs`. Check whether that code runs with `bcl.Mutex` held: if it does, pass a lock-free lookup (`func(n string) (ModelConfig, bool) { c, ok := bcl.configs[n]; return c, ok }`) instead of `bcl.GetModelConfig`, otherwise it deadlocks.

```go
	for name, cfg := range bcl.configs {
		if !cfg.IsFailover() {
			continue
		}
		c := cfg
		if err := validateFailoverTargets(&c, lookup); err != nil {
			if strict {
				return fmt.Errorf("invalid model config %q: %w", name, err)
			}
			xlog.Error("skipping invalid failover chain", "model", name, "error", err)
			delete(bcl.configs, name)
			continue
		}
		if !failoverTargetsShareUsecase(&c, lookup) {
			xlog.Warn("failover chain targets share no known usecase", "model", name)
		}
	}
```

Use the strict-mode variable already in scope in that function (it is the one used at line ~813 for `invalid model config`). If the function has no strict flag at that point, drop the `if strict` branch.

In `core/services/modeladmin/config.go` (both sites) and `core/http/endpoints/localai/import_model.go`, directly after each `ValidateAliasTarget(...)` call, add the equivalent call and return the error the same way the alias error is returned:

```go
	if err := s.Loader.ValidateFailoverTargets(&cfg); err != nil {
		return /* same error shape as the ValidateAliasTarget branch above */ err
	}
```

Copy the exact receiver and variable names from the adjacent `ValidateAliasTarget` call. Do not change what the alias branch returns.

- [ ] **Step 4: Run tests**

Run: `go test ./core/config/... ./core/services/modeladmin/... 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add core/config core/services/modeladmin core/http/endpoints/localai/import_model.go
git commit -m "feat(config): validate failover chain targets across configs

Reject chains whose targets are missing or are chains, at load and on
create or edit, and warn when the targets share no usecase.

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 3: Failover package types and error classification

**Files:**
- Create: `core/services/failover/failover_suite_test.go`, `types.go`, `classify.go`, `classify_test.go`, `types_test.go`

**Interfaces:**
- Produces: types `TargetState`, `ChainState`, `Kind`, `Reason`, `EventType`, `Event`, `TargetStatus`, `ChainStatus`; constants listed below; `KindOf(cfg config.ModelConfig) Kind`; `MergePinned(pinned, warm []string) []string`; `IsRetryable(err error, status int) bool`.

- [ ] **Step 1: Write the failing tests**

`core/services/failover/failover_suite_test.go`:

```go
package failover

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFailover(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Failover test suite")
}
```

`core/services/failover/classify_test.go`:

```go
package failover

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

var _ = DescribeTable("IsRetryable",
	func(err error, status int, want bool) {
		Expect(IsRetryable(err, status)).To(Equal(want))
	},
	Entry("nil error, no status", nil, 0, false),
	Entry("held 503", nil, http.StatusServiceUnavailable, true),
	Entry("held 500", nil, http.StatusInternalServerError, true),
	Entry("held 501", nil, http.StatusNotImplemented, false),
	Entry("client cancel", context.Canceled, 0, false),
	Entry("wrapped client cancel", fmt.Errorf("predict: %w", context.Canceled), 0, false),
	Entry("deadline", context.DeadlineExceeded, 0, true),
	Entry("echo 502", echo.NewHTTPError(http.StatusBadGateway, "x"), 0, true),
	Entry("echo 400", echo.NewHTTPError(http.StatusBadRequest, "x"), 0, false),
	Entry("echo 404", echo.NewHTTPError(http.StatusNotFound, "x"), 0, false),
	Entry("grpc unavailable", grpcstatus.Error(codes.Unavailable, "x"), 0, true),
	Entry("grpc internal", grpcstatus.Error(codes.Internal, "x"), 0, true),
	Entry("grpc deadline", grpcstatus.Error(codes.DeadlineExceeded, "x"), 0, true),
	Entry("grpc unknown", grpcstatus.Error(codes.Unknown, "x"), 0, true),
	Entry("grpc invalid argument", grpcstatus.Error(codes.InvalidArgument, "x"), 0, false),
	Entry("cloud-proxy upstream 503", errors.New("cloud-proxy: upstream 503: no healthy nodes"), 0, true),
	Entry("cloud-proxy upstream 429 stays 4xx", errors.New("cloud-proxy: upstream 429: slow down"), 0, false),
	Entry("context overflow", errors.New("the request exceeds the available context size"), 0, false),
	Entry("dial error", errors.New("dial tcp 10.0.0.1:8080: connect: connection refused"), 0, true),
)
```

`core/services/failover/types_test.go`:

```go
package failover

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("KindOf", func() {
	It("treats proxy backends as remote", func() {
		Expect(KindOf(config.ModelConfig{Backend: "cloud-proxy"})).To(Equal(KindRemote))
		Expect(KindOf(config.ModelConfig{Backend: "localai-proxy"})).To(Equal(KindRemote))
		Expect(KindOf(config.ModelConfig{Backend: "llama-cpp"})).To(Equal(KindLocal))
	})
})

var _ = Describe("MergePinned", func() {
	It("adds warm targets without duplicates", func() {
		Expect(MergePinned([]string{"a", "b"}, []string{"b", "c"})).To(Equal([]string{"a", "b", "c"}))
		Expect(MergePinned(nil, nil)).To(BeEmpty())
	})
})
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./core/services/failover/... 2>&1 | tail -5`
Expected: compile failure (`undefined: IsRetryable`).

- [ ] **Step 3: Implement**

`core/services/failover/types.go`:

```go
// Package failover serves a model name from an ordered chain of target
// models, moving to the next target when one fails and back when it
// recovers.
package failover

import (
	"slices"
	"time"

	"github.com/mudler/LocalAI/core/config"
)

type TargetState string

const (
	StateHealthy    TargetState = "healthy"
	StateDown       TargetState = "down"
	StateRecovering TargetState = "recovering"
	StateMissing    TargetState = "missing"
)

type ChainState string

const (
	ChainPrimary  ChainState = "primary"
	ChainFallback ChainState = "fallback"
	ChainDegraded ChainState = "degraded"
)

type Kind string

const (
	KindLocal  Kind = "local"
	KindRemote Kind = "remote"
)

type Reason string

const (
	ReasonTrip     Reason = "trip"
	ReasonRecovery Reason = "recovery"
	ReasonManual   Reason = "manual"
	ReasonDegraded Reason = "degraded"
	ReasonMissing  Reason = "missing"
	ReasonInitial  Reason = "initial"
)

type EventType string

const (
	EventChainSwitched EventType = "chain.switched"
	EventTargetState   EventType = "target.state"
)

// Event is one change of a target state or of a chain's active target.
type Event struct {
	Type   EventType `json:"type"`
	Chain  string    `json:"chain,omitempty"`
	Target string    `json:"target,omitempty"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	State  string    `json:"state,omitempty"`
	Reason Reason    `json:"reason"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

type TargetStatus struct {
	Model         string      `json:"model"`
	Kind          Kind        `json:"kind"`
	Warm          bool        `json:"warm"`
	State         TargetState `json:"state"`
	ConsecutiveOK int         `json:"consecutive_ok"`
	LastProbe     *time.Time  `json:"last_probe,omitempty"`
	LastError     string      `json:"last_error,omitempty"`
}

type ChainStatus struct {
	Name        string         `json:"name"`
	State       ChainState     `json:"state"`
	Active      string         `json:"active"`
	ActiveSince time.Time      `json:"active_since"`
	Pinned      *string        `json:"pinned"`
	Targets     []TargetStatus `json:"targets"`
}

// KindOf decides how a target is probed: proxy backends forward to another
// server and are checked over HTTP, everything else runs in this instance.
func KindOf(cfg config.ModelConfig) Kind {
	switch cfg.Backend {
	case "cloud-proxy", "localai-proxy":
		return KindRemote
	}
	return KindLocal
}

// MergePinned adds warm failover targets to the config-pinned model list, so
// the watchdog never evicts them.
func MergePinned(pinned, warm []string) []string {
	out := slices.Clone(pinned)
	for _, w := range warm {
		if !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}
```

`core/services/failover/classify.go`:

```go
package failover

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// cloud-proxy translate mode reports upstream failures as plain text, so the
// status is only visible in the message.
var upstreamStatusRe = regexp.MustCompile(`upstream (\d{3})`)

// Errors that the next target would reject in the same way.
var requestErrorMarkers = []string{
	"exceeds the available context size",
	"is larger than the max context size",
	"maximum context length",
}

// IsRetryable reports whether a failed attempt should move to the next
// target. status is the HTTP status a handler wrote, or 0 when it returned err
// without writing.
func IsRetryable(err error, status int) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	if status != 0 {
		return retryableStatus(status)
	}
	if err == nil {
		return false
	}
	var he *echo.HTTPError
	if errors.As(err, &he) {
		return retryableStatus(he.Code)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if st, ok := grpcstatus.FromError(err); ok {
		switch st.Code() {
		case codes.Unavailable, codes.Internal, codes.DeadlineExceeded, codes.Unknown:
			return !isRequestError(st.Message())
		default:
			return false
		}
	}
	msg := err.Error()
	if m := upstreamStatusRe.FindStringSubmatch(msg); m != nil {
		code, _ := strconv.Atoi(m[1])
		return retryableStatus(code)
	}
	// Anything else is usually a dial or load failure of this target.
	return !isRequestError(msg)
}

func retryableStatus(code int) bool {
	return code >= 500 && code != http.StatusNotImplemented
}

func isRequestError(msg string) bool {
	for _, m := range requestErrorMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./core/services/failover/... 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add core/services/failover
git commit -m "feat(failover): add types and retryable error classification

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 4: Manager state machine, plans, pins, events

**Files:**
- Create: `core/services/failover/manager.go`, `core/services/failover/manager_test.go`, `core/services/failover/fakes_test.go`

**Interfaces:**
- Consumes: Task 1 config types, Task 3 types and `IsRetryable`.
- Produces (used by Tasks 5-12):
  - `type ConfigSource interface { GetModelConfig(string) (config.ModelConfig, bool); GetAllModelsConfigs() []config.ModelConfig }`
  - `type Clock interface { Now() time.Time }`
  - `func New(src ConfigSource, opts ...Option) *Manager`; options `WithClock(Clock)`, `WithProber(Prober)`, `WithOnWarmChanged(func([]string))`; the `Prober` interface (declared at the end of `manager.go`, implemented in Task 6)
  - `(*Manager).Sync()`, `Reevaluate()`, `Plan(chain string) (*Attempt, error)`, `ReportFailure(target string, err error)`, `ReportSuccess(target string)`, `Pin(chain, target string) error`, `Unpin(chain string) error`, `Status() []ChainStatus`, `ChainStatus(name string) (ChainStatus, bool)`, `Subscribe(buffer int) (<-chan Event, func())`, `WarmTargets() []string`, `Do(ctx, chain string, fn func(ctx context.Context, target string, commit func()) error) error`
  - `(*Attempt).Chain() string`, `Target() string`, `Primary() string`, `Degraded() bool`, `Fail(err error) bool`, `Report(err error)`, `Succeed()`
  - errors `ErrChainNotFound`, `ErrTargetNotInChain`, `ErrNoTarget`

- [ ] **Step 1: Write test fakes**

`core/services/failover/fakes_test.go`:

```go
package failover

import (
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type fakeSource struct {
	mu   sync.Mutex
	cfgs map[string]config.ModelConfig
}

func newFakeSource(cfgs ...config.ModelConfig) *fakeSource {
	s := &fakeSource{cfgs: map[string]config.ModelConfig{}}
	for _, c := range cfgs {
		s.cfgs[c.Name] = c
	}
	return s
}
func (s *fakeSource) Put(c config.ModelConfig) { s.mu.Lock(); s.cfgs[c.Name] = c; s.mu.Unlock() }
func (s *fakeSource) Delete(name string)      { s.mu.Lock(); delete(s.cfgs, name); s.mu.Unlock() }
func (s *fakeSource) GetModelConfig(n string) (config.ModelConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cfgs[n]
	return c, ok
}
func (s *fakeSource) GetAllModelsConfigs() []config.ModelConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]config.ModelConfig, 0, len(s.cfgs))
	for _, c := range s.cfgs {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func local(name string) config.ModelConfig  { return config.ModelConfig{Name: name, Backend: "llama-cpp"} }
func remote(name string) config.ModelConfig { return config.ModelConfig{Name: name, Backend: "cloud-proxy"} }

// chainCfg builds a chain; fc may be nil for defaults.
func chainCfg(name string, fc *config.FailoverConfig, targets ...config.FailoverTarget) config.ModelConfig {
	f := config.FailoverConfig{}
	if fc != nil {
		f = *fc
	}
	f.Targets = targets
	return config.ModelConfig{Name: name, Failover: &f}
}

func t(model string) config.FailoverTarget     { return config.FailoverTarget{Model: model} }
func warmT(model string) config.FailoverTarget { return config.FailoverTarget{Model: model, Warm: true} }

// drain returns the events buffered so far without blocking.
func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}
```

- [ ] **Step 2: Write the failing manager tests**

`core/services/failover/manager_test.go`:

```go
package failover

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var errBoom = errors.New("dial tcp: connection refused")

var _ = Describe("Manager", func() {
	var (
		clock *fakeClock
		src   *fakeSource
		m     *Manager
	)

	BeforeEach(func() {
		clock = newFakeClock()
		src = newFakeSource(remote("a"), local("b"), chainCfg("chain", nil, t("a"), t("b")))
		m = New(src, WithClock(clock))
	})

	switched := func(evs []Event) []Event {
		var out []Event
		for _, e := range evs {
			if e.Type == EventChainSwitched {
				out = append(out, e)
			}
		}
		return out
	}

	It("plans the primary first on a fresh chain", func() {
		att, err := m.Plan("chain")
		Expect(err).ToNot(HaveOccurred())
		Expect(att.Target()).To(Equal("a"))
		Expect(att.Primary()).To(Equal("a"))
		Expect(att.Degraded()).To(BeFalse())
		st, ok := m.ChainStatus("chain")
		Expect(ok).To(BeTrue())
		Expect(st.State).To(Equal(ChainPrimary))
		Expect(st.Targets[0].Kind).To(Equal(KindRemote))
		Expect(st.Targets[1].Kind).To(Equal(KindLocal))
	})

	It("returns ErrChainNotFound for an unknown chain", func() {
		_, err := m.Plan("nope")
		Expect(errors.Is(err, ErrChainNotFound)).To(BeTrue())
	})

	It("trips on the first failure by default and switches with an event", func() {
		events, cancel := m.Subscribe(16)
		defer cancel()
		att, _ := m.Plan("chain")
		Expect(att.Fail(errBoom)).To(BeTrue())
		Expect(att.Target()).To(Equal("b"))
		st, _ := m.ChainStatus("chain")
		Expect(st.Active).To(Equal("b"))
		Expect(st.State).To(Equal(ChainFallback))
		Expect(st.Targets[0].State).To(Equal(StateDown))
		Expect(st.Targets[0].LastError).To(ContainSubstring("connection refused"))
		sw := switched(drain(events))
		Expect(sw).To(HaveLen(1))
		Expect(sw[0]).To(MatchFields(IgnoreExtras, Fields{
			"Chain": Equal("chain"), "From": Equal("a"), "To": Equal("b"),
			"State": Equal("fallback"), "Reason": Equal(ReasonTrip),
		}))
	})

	It("counts failures inside the trip window only", func() {
		src.Put(chainCfg("chain", &config.FailoverConfig{Trip: config.FailoverTrip{Errors: 2, Window: "30s"}}, t("a"), t("b")))
		m.Sync()
		m.ReportFailure("a", errBoom)
		clock.Advance(31 * time.Second)
		m.ReportFailure("a", errBoom)
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateHealthy))
		clock.Advance(time.Second)
		m.ReportFailure("a", errBoom)
		st, _ = m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateDown))
	})

	It("fails back only after recovery probes and min_dwell", func() {
		m.Plan("chain")
		m.ReportFailure("a", errBoom)
		for i := 0; i < 3; i++ {
			m.ReportSuccess("a") // a real success counts like a passed inference probe
		}
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateHealthy))
		Expect(st.Active).To(Equal("b"), "min_dwell has not passed")
		clock.Advance(61 * time.Second)
		events, cancel := m.Subscribe(16)
		defer cancel()
		m.Reevaluate()
		st, _ = m.ChainStatus("chain")
		Expect(st.Active).To(Equal("a"))
		Expect(switched(drain(events))[0].Reason).To(Equal(ReasonRecovery))
	})

	It("moves up at once when the active target itself goes down", func() {
		src.Put(chainCfg("chain", nil, t("a"), t("b"), t("c")))
		src.Put(local("c"))
		m.Sync()
		m.ReportFailure("a", errBoom) // active: b
		for i := 0; i < 3; i++ {
			m.ReportSuccess("a") // a healthy again, but dwell not passed
		}
		m.ReportFailure("b", errBoom) // b down: go to a now, not c
		st, _ := m.ChainStatus("chain")
		Expect(st.Active).To(Equal("a"))
	})

	It("goes degraded when all targets are down and plans all of them in priority order", func() {
		m.ReportFailure("a", errBoom)
		m.ReportFailure("b", errBoom)
		st, _ := m.ChainStatus("chain")
		Expect(st.State).To(Equal(ChainDegraded))
		att, err := m.Plan("chain")
		Expect(err).ToNot(HaveOccurred())
		Expect(att.Degraded()).To(BeTrue())
		Expect(att.Target()).To(Equal("a"))
		Expect(att.Fail(errBoom)).To(BeTrue())
		Expect(att.Target()).To(Equal("b"))
		Expect(att.Fail(errBoom)).To(BeFalse())
	})

	It("pins a target regardless of health", func() {
		Expect(m.Pin("chain", "b")).To(Succeed())
		att, _ := m.Plan("chain")
		Expect(att.Target()).To(Equal("b"))
		Expect(att.Fail(errBoom)).To(BeFalse(), "a pin allows only the pinned target")
		st, _ := m.ChainStatus("chain")
		Expect(*st.Pinned).To(Equal("b"))
		Expect(st.Active).To(Equal("b"))
		Expect(m.Unpin("chain")).To(Succeed())
		st, _ = m.ChainStatus("chain")
		Expect(st.Pinned).To(BeNil())
		Expect(errors.Is(m.Pin("chain", "zzz"), ErrTargetNotInChain)).To(BeTrue())
		Expect(errors.Is(m.Pin("nope", "a"), ErrChainNotFound)).To(BeTrue())
	})

	It("shares target health across chains", func() {
		src.Put(chainCfg("chain2", nil, t("a"), t("b")))
		m.Sync()
		m.ReportFailure("a", errBoom)
		s1, _ := m.ChainStatus("chain")
		s2, _ := m.ChainStatus("chain2")
		Expect(s1.Active).To(Equal("b"))
		Expect(s2.Active).To(Equal("b"))
	})

	It("marks a removed target missing and leaves it out of plans", func() {
		m.Plan("chain")
		src.Delete("a")
		m.Sync()
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateMissing))
		att, _ := m.Plan("chain")
		Expect(att.Target()).To(Equal("b"))
		Expect(att.Fail(errBoom)).To(BeFalse())
	})

	It("resets a chain whose target list changed", func() {
		m.ReportFailure("a", errBoom)
		src.Put(local("c"))
		src.Put(chainCfg("chain", nil, t("c"), t("b")))
		m.Sync()
		st, _ := m.ChainStatus("chain")
		Expect(st.Active).To(Equal("c"))
	})

	It("reports warm local targets and ignores warm on remote ones", func() {
		var got []string
		m = New(src, WithClock(clock), WithOnWarmChanged(func(w []string) { got = w }))
		src.Put(chainCfg("chain", nil, warmT("a"), warmT("b")))
		m.Sync()
		Expect(got).To(Equal([]string{"b"}))
		Expect(m.WarmTargets()).To(Equal([]string{"b"}))
	})

	It("closes a subscription on cancel", func() {
		events, cancel := m.Subscribe(1)
		cancel()
		_, ok := <-events
		Expect(ok).To(BeFalse())
		cancel() // idempotent
	})

	Describe("Do", func() {
		It("retries on the next target until commit", func() {
			var tried []string
			err := m.Do(context.Background(), "chain", func(_ context.Context, target string, commit func()) error {
				tried = append(tried, target)
				if target == "a" {
					return errBoom
				}
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(tried).To(Equal([]string{"a", "b"}))
		})

		It("does not retry after commit but still trips the target", func() {
			var tried []string
			err := m.Do(context.Background(), "chain", func(_ context.Context, target string, commit func()) error {
				tried = append(tried, target)
				commit()
				return errBoom
			})
			Expect(err).To(MatchError(errBoom))
			Expect(tried).To(Equal([]string{"a"}))
			st, _ := m.ChainStatus("chain")
			Expect(st.Targets[0].State).To(Equal(StateDown))
		})

		It("does not retry or trip on a non-retryable error", func() {
			bad := errors.New("the request exceeds the available context size")
			err := m.Do(context.Background(), "chain", func(_ context.Context, _ string, _ func()) error { return bad })
			Expect(err).To(MatchError(bad))
			st, _ := m.ChainStatus("chain")
			Expect(st.Targets[0].State).To(Equal(StateHealthy))
		})
	})
})
```

The tests use `MatchFields`, so add `. "github.com/onsi/gomega/gstruct"` to the imports.

- [ ] **Step 3: Run to verify failure**

Run: `go test ./core/services/failover/... 2>&1 | tail -5`
Expected: compile failure (`undefined: New`).

- [ ] **Step 4: Implement the manager**

`core/services/failover/manager.go`:

```go
package failover

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
)

var (
	ErrChainNotFound    = errors.New("failover chain not found")
	ErrTargetNotInChain = errors.New("target is not in this failover chain")
	ErrNoTarget         = errors.New("failover chain has no usable target")
)

// ConfigSource is the part of ModelConfigLoader the manager reads.
type ConfigSource interface {
	GetModelConfig(name string) (config.ModelConfig, bool)
	GetAllModelsConfigs() []config.ModelConfig
}

type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type Option func(*Manager)

func WithClock(c Clock) Option    { return func(m *Manager) { m.clock = c } }
func WithProber(p Prober) Option  { return func(m *Manager) { m.prober = p } }

// WithOnWarmChanged is called outside the manager lock when the set of warm
// local targets changes. The application pins and preloads them.
func WithOnWarmChanged(fn func(warm []string)) Option { return func(m *Manager) { m.onWarm = fn } }

// Manager tracks health per target and the active target per chain.
type Manager struct {
	mu          sync.Mutex
	src         ConfigSource
	clock       Clock
	prober      Prober
	onWarm      func([]string)
	targets     map[string]*targetState
	chains      map[string]*chainState
	subs        map[int]chan Event
	nextSub     int
	warm        []string
	warmPending bool
	closed      bool
}

type targetState struct {
	name          string
	kind          Kind
	warm          bool
	state         TargetState
	failures      []time.Time
	consecutiveOK int
	downSince     time.Time
	lastProbe     time.Time
	lastActivity  time.Time
	lastError     string
	// params come from the first chain, in name order, that lists the target.
	params config.FailoverConfig
}

func (ts *targetState) cold() bool { return ts.kind == KindLocal && !ts.warm }

type chainState struct {
	name        string
	cfg         config.FailoverConfig
	targets     []string
	active      int
	activeSince time.Time
	pinned      string
	state       ChainState
}

func New(src ConfigSource, opts ...Option) *Manager {
	m := &Manager{
		src:     src,
		clock:   realClock{},
		targets: map[string]*targetState{},
		chains:  map[string]*chainState{},
		subs:    map[int]chan Event{},
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Sync reconciles chains with the config source. There is no config-change
// hook in the loader, so this runs on every tick and on a lookup miss.
func (m *Manager) Sync() {
	m.mu.Lock()
	m.syncLocked()
	warm, deliver := m.takeWarmLocked()
	m.mu.Unlock()
	if deliver && m.onWarm != nil {
		m.onWarm(warm)
	}
}

func (m *Manager) syncLocked() {
	now := m.clock.Now()
	seenChains := map[string]bool{}
	claimed := map[string]bool{}
	for _, c := range m.src.GetAllModelsConfigs() {
		if !c.IsFailover() {
			continue
		}
		seenChains[c.Name] = true
		names := make([]string, 0, len(c.Failover.Targets))
		for _, t := range c.Failover.Targets {
			names = append(names, t.Model)
		}
		ch := m.chains[c.Name]
		if ch == nil || !slices.Equal(ch.targets, names) {
			pinned := ""
			if ch != nil && slices.Contains(names, ch.pinned) {
				pinned = ch.pinned
			}
			ch = &chainState{name: c.Name, targets: names, activeSince: now, state: ChainPrimary, pinned: pinned}
			m.chains[c.Name] = ch
		}
		ch.cfg = *c.Failover
		for _, t := range c.Failover.Targets {
			ts := m.targets[t.Model]
			if ts == nil {
				ts = &targetState{name: t.Model, state: StateHealthy}
				m.targets[t.Model] = ts
			}
			if !claimed[t.Model] {
				claimed[t.Model] = true
				ts.params = *c.Failover
				ts.warm = false
			}
			tc, ok := m.lookupTarget(t.Model)
			if !ok {
				m.setTargetLocked(ts, StateMissing, ReasonMissing, "target config not found")
				continue
			}
			ts.kind = KindOf(tc)
			if t.Warm && ts.kind == KindLocal {
				ts.warm = true
			}
			if ts.state == StateMissing {
				m.setTargetLocked(ts, StateHealthy, ReasonRecovery, "")
			}
		}
	}
	for name := range m.chains {
		if !seenChains[name] {
			delete(m.chains, name)
		}
	}
	for name := range m.targets {
		if !claimed[name] {
			delete(m.targets, name)
		}
	}
	for _, ch := range m.chains {
		m.recomputeLocked(ch, "")
	}
	var warm []string
	for name, ts := range m.targets {
		if ts.warm {
			warm = append(warm, name)
		}
	}
	sort.Strings(warm)
	if !slices.Equal(warm, m.warm) {
		m.warm = warm
		m.warmPending = true
	}
}

func (m *Manager) takeWarmLocked() ([]string, bool) {
	if !m.warmPending {
		return nil, false
	}
	m.warmPending = false
	return slices.Clone(m.warm), true
}

// lookupTarget returns the config that serves a target, one alias hop deep.
func (m *Manager) lookupTarget(name string) (config.ModelConfig, bool) {
	c, ok := m.src.GetModelConfig(name)
	if ok && c.IsAlias() {
		return m.src.GetModelConfig(c.Alias)
	}
	return c, ok
}

func (m *Manager) chainLocked(name string) *chainState {
	if ch := m.chains[name]; ch != nil {
		return ch
	}
	m.syncLocked()
	return m.chains[name]
}

// WarmTargets returns the warm local targets, sorted.
func (m *Manager) WarmTargets() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.warm)
}

// Reevaluate recomputes every chain. Dwell-based fail-back needs no event, so
// the scheduler calls this on every tick.
func (m *Manager) Reevaluate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.chains {
		m.recomputeLocked(ch, "")
	}
}

func (m *Manager) setTargetLocked(ts *targetState, to TargetState, reason Reason, errMsg string) {
	if ts.state == to {
		return
	}
	from := ts.state
	now := m.clock.Now()
	ts.state = to
	switch to {
	case StateDown:
		ts.downSince = now
		ts.consecutiveOK = 0
		ts.failures = nil
	case StateRecovering, StateHealthy:
		ts.consecutiveOK = 0
		ts.failures = nil
	}
	m.emitLocked(Event{Type: EventTargetState, Target: ts.name, From: string(from), To: string(to), Reason: reason, Error: errMsg, At: now})
}

// recomputeLocked picks the active target. override replaces the reason of a
// resulting switch (pin and unpin are always "manual").
func (m *Manager) recomputeLocked(ch *chainState, override Reason) {
	now := m.clock.Now()
	prev := ch.active
	next := prev
	reason := ReasonTrip
	best := -1
	for i, name := range ch.targets {
		if ts := m.targets[name]; ts != nil && ts.state == StateHealthy {
			best = i
			break
		}
	}
	switch {
	case ch.pinned != "":
		next = slices.Index(ch.targets, ch.pinned)
		reason = ReasonManual
	case best == -1:
		// Nothing is healthy: keep the active target, Plan tries all of them.
	case best > prev:
		next = best // the active target is not healthy
	case best < prev:
		cur := m.targets[ch.targets[prev]]
		curHealthy := cur != nil && cur.state == StateHealthy
		if !curHealthy {
			next = best
		} else if now.Sub(ch.activeSince) >= ch.cfg.MinDwell() {
			next = best
			reason = ReasonRecovery
		}
	}
	if override != "" {
		reason = override
	}
	var state ChainState
	switch {
	case ch.pinned == "" && best == -1:
		state = ChainDegraded
	case next == 0:
		state = ChainPrimary
	default:
		state = ChainFallback
	}
	switch {
	case next != prev:
		ch.active = next
		ch.activeSince = now
		m.emitLocked(Event{Type: EventChainSwitched, Chain: ch.name, From: ch.targets[prev], To: ch.targets[next], State: string(state), Reason: reason, At: now})
	case state == ChainDegraded && ch.state != ChainDegraded:
		m.emitLocked(Event{Type: EventChainSwitched, Chain: ch.name, From: ch.targets[prev], To: ch.targets[next], State: string(state), Reason: ReasonDegraded, At: now})
	}
	ch.state = state
}

func (m *Manager) recomputeForLocked(target string) {
	for _, ch := range m.chains {
		if slices.Contains(ch.targets, target) {
			m.recomputeLocked(ch, "")
		}
	}
}

// Attempt walks the targets of one request in order.
type Attempt struct {
	m        *Manager
	chain    string
	primary  string
	degraded bool
	targets  []string
	i        int
}

// Plan returns the attempt order for one request: the active target, then the
// other healthy targets. A degraded chain tries every target in priority
// order; a pinned chain only the pinned target.
func (m *Manager) Plan(chain string) (*Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := m.chainLocked(chain)
	if ch == nil {
		return nil, fmt.Errorf("%w: %q", ErrChainNotFound, chain)
	}
	att := &Attempt{m: m, chain: ch.name, primary: ch.targets[0], degraded: ch.state == ChainDegraded}
	usable := func(name string) bool {
		ts := m.targets[name]
		if ts == nil || ts.state == StateMissing {
			return false
		}
		return att.degraded || ts.state == StateHealthy
	}
	switch {
	case ch.pinned != "":
		att.targets = []string{ch.pinned}
	case att.degraded:
		for _, name := range ch.targets {
			if usable(name) {
				att.targets = append(att.targets, name)
			}
		}
	default:
		active := ch.targets[ch.active]
		if usable(active) {
			att.targets = append(att.targets, active)
		}
		for _, name := range ch.targets {
			if name != active && usable(name) {
				att.targets = append(att.targets, name)
			}
		}
	}
	if len(att.targets) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNoTarget, chain)
	}
	return att, nil
}

func (a *Attempt) Chain() string   { return a.chain }
func (a *Attempt) Target() string  { return a.targets[a.i] }
func (a *Attempt) Primary() string { return a.primary }
func (a *Attempt) Degraded() bool  { return a.degraded }

// Fail records err against the current target and moves to the next one. It
// returns false when no target is left.
func (a *Attempt) Fail(err error) bool {
	a.m.ReportFailure(a.Target(), err)
	if a.i+1 >= len(a.targets) {
		return false
	}
	a.i++
	return true
}

// Report records err against the current target without moving on: the
// response was already committed, so nothing is left to retry.
func (a *Attempt) Report(err error) { a.m.ReportFailure(a.Target(), err) }

func (a *Attempt) Succeed() { a.m.ReportSuccess(a.Target()) }

func (m *Manager) ReportFailure(target string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.targets[target]
	if ts == nil {
		return
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	m.recordFailureLocked(ts, msg)
	m.recomputeForLocked(target)
}

func (m *Manager) recordFailureLocked(ts *targetState, msg string) {
	now := m.clock.Now()
	ts.lastError = msg
	switch ts.state {
	case StateRecovering:
		m.setTargetLocked(ts, StateDown, ReasonTrip, msg)
	case StateHealthy:
		cut := now.Add(-ts.params.TripWindow())
		kept := ts.failures[:0]
		for _, f := range ts.failures {
			if f.After(cut) {
				kept = append(kept, f)
			}
		}
		ts.failures = append(kept, now)
		if len(ts.failures) >= ts.params.TripErrors() {
			m.setTargetLocked(ts, StateDown, ReasonTrip, msg)
		}
	}
}

func (m *Manager) ReportSuccess(target string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.targets[target]
	if ts == nil {
		return
	}
	ts.lastActivity = m.clock.Now()
	m.recordPassLocked(ts)
	m.recomputeForLocked(target)
}

// recordPassLocked counts a served request or a passed inference probe.
func (m *Manager) recordPassLocked(ts *targetState) {
	switch ts.state {
	case StateHealthy:
		ts.failures = nil
		return
	case StateMissing:
		return
	case StateDown:
		if ts.cold() {
			// Cold targets are never probed; a served request is proof enough.
			m.setTargetLocked(ts, StateHealthy, ReasonRecovery, "")
			return
		}
		m.setTargetLocked(ts, StateRecovering, ReasonRecovery, "")
	}
	ts.consecutiveOK++
	if ts.consecutiveOK >= ts.params.RecoveryProbes() {
		m.setTargetLocked(ts, StateHealthy, ReasonRecovery, "")
	}
}

func (m *Manager) Pin(chain, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := m.chainLocked(chain)
	if ch == nil {
		return fmt.Errorf("%w: %q", ErrChainNotFound, chain)
	}
	if !slices.Contains(ch.targets, target) {
		return fmt.Errorf("%w: %q", ErrTargetNotInChain, target)
	}
	ch.pinned = target
	m.recomputeLocked(ch, ReasonManual)
	return nil
}

func (m *Manager) Unpin(chain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := m.chainLocked(chain)
	if ch == nil {
		return fmt.Errorf("%w: %q", ErrChainNotFound, chain)
	}
	ch.pinned = ""
	m.recomputeLocked(ch, ReasonManual)
	return nil
}

// Status returns every chain, sorted by name.
func (m *Manager) Status() []ChainStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncLocked()
	names := make([]string, 0, len(m.chains))
	for name := range m.chains {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ChainStatus, 0, len(names))
	for _, name := range names {
		out = append(out, m.statusLocked(m.chains[name]))
	}
	return out
}

func (m *Manager) ChainStatus(name string) (ChainStatus, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := m.chainLocked(name)
	if ch == nil {
		return ChainStatus{}, false
	}
	return m.statusLocked(ch), true
}

func (m *Manager) statusLocked(ch *chainState) ChainStatus {
	cs := ChainStatus{Name: ch.name, State: ch.state, Active: ch.targets[ch.active], ActiveSince: ch.activeSince}
	if ch.pinned != "" {
		p := ch.pinned
		cs.Pinned = &p
	}
	for _, name := range ch.targets {
		st := TargetStatus{Model: name}
		if ts := m.targets[name]; ts != nil {
			st.Kind, st.Warm, st.State = ts.kind, ts.warm, ts.state
			st.ConsecutiveOK, st.LastError = ts.consecutiveOK, ts.lastError
			if !ts.lastProbe.IsZero() {
				lp := ts.lastProbe
				st.LastProbe = &lp
			}
		}
		cs.Targets = append(cs.Targets, st)
	}
	return cs
}

// Subscribe returns a buffered event channel and a cancel func. A subscriber
// that does not keep up loses events rather than blocking the manager.
func (m *Manager) Subscribe(buffer int) (<-chan Event, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan Event, buffer)
	if m.closed {
		close(ch)
		return ch, func() {}
	}
	id := m.nextSub
	m.nextSub++
	m.subs[id] = ch
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if c, ok := m.subs[id]; ok {
				delete(m.subs, id)
				close(c)
			}
		})
	}
}

func (m *Manager) emitLocked(ev Event) {
	for _, c := range m.subs {
		select {
		case c <- ev:
		default:
			xlog.Warn("failover: dropping event for a slow subscriber", "type", ev.Type, "chain", ev.Chain, "target", ev.Target)
		}
	}
}

func (m *Manager) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for id, c := range m.subs {
		close(c)
		delete(m.subs, id)
	}
}

// Do runs fn against the chain's targets in plan order. fn calls commit once
// output has reached the client; after that a failure is not retried.
func (m *Manager) Do(ctx context.Context, chain string, fn func(ctx context.Context, target string, commit func()) error) error {
	att, err := m.Plan(chain)
	if err != nil {
		return err
	}
	for {
		var committed atomic.Bool
		err := fn(ctx, att.Target(), func() { committed.Store(true) })
		switch {
		case err == nil:
			att.Succeed()
			return nil
		case ctx.Err() != nil || !IsRetryable(err, 0):
			return err
		case committed.Load():
			att.Report(err)
			return err
		case !att.Fail(err):
			return err
		}
	}
}

// Prober checks targets. Implemented by DefaultProber (prober.go).
type Prober interface {
	// Liveness is the cheap steady-state check.
	Liveness(ctx context.Context, target config.ModelConfig, kind Kind, warm bool) error
	// Inference sends one minimal real request to confirm recovery.
	Inference(ctx context.Context, target config.ModelConfig, kind Kind, warm bool) error
}
```

- [ ] **Step 5: Run tests**

Run: `go test -race ./core/services/failover/... 2>&1 | tail -10`
Expected: PASS, no race reports.

- [ ] **Step 6: Commit**

```bash
git add core/services/failover
git commit -m "feat(failover): add chain manager with trip, fail-back and pins

Health is tracked per target and the active target per chain. Fail-back
waits for recovery probes and a minimum time on the fallback.

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 5: Probe scheduling

**Files:**
- Create: `core/services/failover/schedule.go`, `core/services/failover/schedule_test.go`

**Interfaces:**
- Consumes: Task 4 `Manager`, `Prober`, internal helpers `recordFailureLocked`, `recordPassLocked`, `setTargetLocked`, `recomputeForLocked`, `lookupTarget`, `close`.
- Produces: `(*Manager).Run(ctx)`, `(*Manager).Tick(ctx)`.

- [ ] **Step 1: Write the failing tests**

`core/services/failover/schedule_test.go`:

```go
package failover

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type probeCall struct {
	target    string
	inference bool
}

type fakeProber struct {
	mu    sync.Mutex
	calls []probeCall
	fail  map[string]error // target -> error returned by every probe
}

func (p *fakeProber) record(target string, inference bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, probeCall{target, inference})
	return p.fail[target]
}
func (p *fakeProber) Liveness(_ context.Context, c config.ModelConfig, _ Kind, _ bool) error {
	return p.record(c.Name, false)
}
func (p *fakeProber) Inference(_ context.Context, c config.ModelConfig, _ Kind, _ bool) error {
	return p.record(c.Name, true)
}
func (p *fakeProber) take() []probeCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.calls
	p.calls = nil
	return out
}

var _ = Describe("Manager probes", func() {
	var (
		clock  *fakeClock
		src    *fakeSource
		prober *fakeProber
		m      *Manager
		ctx    = context.Background()
	)

	BeforeEach(func() {
		clock = newFakeClock()
		prober = &fakeProber{fail: map[string]error{}}
		src = newFakeSource(remote("a"), local("b"), local("cold"),
			chainCfg("chain", nil, t("a"), warmT("b")))
		m = New(src, WithClock(clock), WithProber(prober))
	})

	It("probes idle targets on the first tick and not again before the interval", func() {
		m.Tick(ctx)
		Expect(prober.take()).To(ConsistOf(probeCall{"a", false}, probeCall{"b", false}))
		clock.Advance(5 * time.Second)
		m.Tick(ctx)
		Expect(prober.take()).To(BeEmpty())
	})

	It("skips the liveness probe for a target with recent traffic", func() {
		m.Tick(ctx)
		prober.take()
		clock.Advance(14 * time.Second)
		m.ReportSuccess("a")
		clock.Advance(2 * time.Second)
		m.Tick(ctx)
		Expect(prober.take()).To(ConsistOf(probeCall{"b", false}))
	})

	It("trips a target whose liveness probe fails", func() {
		prober.fail["a"] = errBoom
		m.Tick(ctx)
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateDown))
		Expect(st.Active).To(Equal("b"))
	})

	It("recovers through liveness, then inference probes, then fails back after dwell", func() {
		prober.fail["a"] = errBoom
		m.Tick(ctx)
		delete(prober.fail, "a")
		prober.take()

		clock.Advance(15 * time.Second)
		m.Tick(ctx) // liveness passes: recovering
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateRecovering))

		for i := 0; i < 3; i++ {
			clock.Advance(15 * time.Second)
			m.Tick(ctx)
		}
		calls := prober.take()
		Expect(calls).To(ContainElement(probeCall{"a", true}))
		st, _ = m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateHealthy))
		Expect(st.Active).To(Equal("a"), "60s min_dwell passed during the 4 ticks")
	})

	It("sends a recovering target back down when an inference probe fails", func() {
		m.ReportFailure("a", errBoom)
		clock.Advance(15 * time.Second)
		m.Tick(ctx) // liveness passes: recovering
		prober.fail["a"] = errBoom
		clock.Advance(15 * time.Second)
		m.Tick(ctx)
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateDown))
	})

	It("never probes a down cold target and restores it after min_dwell", func() {
		src.Put(chainCfg("chain", nil, t("cold"), warmT("b")))
		m.Sync()
		m.ReportFailure("cold", errBoom)
		prober.take()
		clock.Advance(30 * time.Second)
		m.Tick(ctx)
		for _, c := range prober.take() {
			Expect(c.target).ToNot(Equal("cold"))
		}
		st, _ := m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateDown))
		clock.Advance(31 * time.Second)
		m.Tick(ctx)
		st, _ = m.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(StateHealthy))
	})

	It("probes a target shared by two chains once per tick", func() {
		src.Put(chainCfg("chain2", nil, t("a"), warmT("b")))
		m.Tick(ctx)
		calls := prober.take()
		n := 0
		for _, c := range calls {
			if c.target == "a" {
				n++
			}
		}
		Expect(n).To(Equal(1))
	})

	It("closes subscriptions when Run stops", func() {
		events, _ := m.Subscribe(1)
		rctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { m.Run(rctx); close(done) }()
		cancel()
		Eventually(done).Should(BeClosed())
		Eventually(events).Should(BeClosed())
	})
})
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./core/services/failover/... 2>&1 | tail -5`
Expected: compile failure (`m.Tick undefined`).

- [ ] **Step 3: Implement**

`core/services/failover/schedule.go`:

```go
package failover

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
)

// Run drives probes and dwell-based fail-back until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	m.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			m.close()
			return
		case <-ticker.C:
			m.Tick(ctx)
		}
	}
}

// Tick runs one pass: sync configs, run due probes, recompute chains. It is
// exported so tests can drive the manager without a real ticker.
func (m *Manager) Tick(ctx context.Context) {
	m.Sync()
	var wg sync.WaitGroup
	for _, j := range m.dueProbes() {
		wg.Add(1)
		go func(j probeJob) {
			defer wg.Done()
			m.runProbe(ctx, j)
		}(j)
	}
	wg.Wait()
	m.Reevaluate()
}

type probeJob struct {
	target    string
	cfg       config.ModelConfig
	kind      Kind
	warm      bool
	inference bool
	timeout   time.Duration
}

func (m *Manager) dueProbes() []probeJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.Now()
	var jobs []probeJob
	for _, ts := range m.targets {
		interval := ts.params.ProbeInterval()
		inference := false
		switch ts.state {
		case StateMissing:
			continue
		case StateHealthy:
			// A served request is as good as a liveness probe.
			if now.Sub(ts.lastActivity) < interval || now.Sub(ts.lastProbe) < interval {
				continue
			}
		case StateDown:
			if ts.cold() {
				// Loading a cold model only to probe it could evict others.
				if now.Sub(ts.downSince) >= ts.params.MinDwell() {
					m.setTargetLocked(ts, StateHealthy, ReasonRecovery, "")
					m.recomputeForLocked(ts.name)
				}
				continue
			}
			if now.Sub(ts.lastProbe) < interval {
				continue
			}
		case StateRecovering:
			if ts.cold() || now.Sub(ts.lastProbe) < interval {
				continue
			}
			inference = true
		}
		if m.prober == nil {
			continue
		}
		cfg, ok := m.lookupTarget(ts.name)
		if !ok {
			continue
		}
		ts.lastProbe = now
		jobs = append(jobs, probeJob{
			target: ts.name, cfg: cfg, kind: ts.kind, warm: ts.warm,
			inference: inference, timeout: ts.params.ProbeTimeout(),
		})
	}
	return jobs
}

func (m *Manager) runProbe(ctx context.Context, j probeJob) {
	pctx, cancel := context.WithTimeout(ctx, j.timeout)
	defer cancel()
	var err error
	if j.inference {
		err = m.prober.Inference(pctx, j.cfg, j.kind, j.warm)
	} else {
		err = m.prober.Liveness(pctx, j.cfg, j.kind, j.warm)
	}
	if ctx.Err() != nil {
		return // shutting down: a cancelled probe says nothing about the target
	}
	m.applyProbe(j, err)
}

func (m *Manager) applyProbe(j probeJob, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.targets[j.target]
	if ts == nil || ts.state == StateMissing {
		return
	}
	if err != nil {
		if ts.state == StateDown {
			ts.lastError = err.Error()
		} else {
			m.recordFailureLocked(ts, err.Error())
		}
		m.recomputeForLocked(ts.name)
		return
	}
	switch ts.state {
	case StateHealthy:
		ts.lastActivity = m.clock.Now()
		ts.failures = nil
	case StateDown:
		m.setTargetLocked(ts, StateRecovering, ReasonRecovery, "")
	case StateRecovering:
		if j.inference {
			m.recordPassLocked(ts)
		}
	}
	m.recomputeForLocked(ts.name)
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./core/services/failover/... 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add core/services/failover
git commit -m "feat(failover): schedule liveness and recovery probes

Idle targets get a liveness probe each interval, recovering targets an
inference probe. Cold local targets are never loaded to be probed.

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 6: Default prober (remote HTTP and local gRPC)

**Files:**
- Create: `core/services/failover/prober.go`, `core/services/failover/prober_test.go`
- Modify: `core/config/model_config.go` (add `ResolveAPIKey` next to `ProxyConfig`, line ~238-310)
- Test: `core/config/model_config_failover_test.go` (add ResolveAPIKey specs)
- Modify: `docs/superpowers/specs/2026-09-26-model-failover-chains-design.md` (cold-local row: "the model file exists"; see Step 5)

**Interfaces:**
- Consumes: `Prober` (Task 4), `KindRemote/KindLocal`.
- Produces: `type LoadFunc func(ctx context.Context, cfg config.ModelConfig) (grpc.Backend, error)`, `func NewProber(load LoadFunc, modelPath string) *DefaultProber`, `func UpstreamBase(raw string) (string, error)`, `func UpstreamModel(cfg config.ModelConfig) string`, `(config.ProxyConfig).ResolveAPIKey() (string, error)`.

- [ ] **Step 1: Write the failing tests**

Add to `core/config/model_config_failover_test.go`:

```go
var _ = Describe("ProxyConfig.ResolveAPIKey", func() {
	It("reads the env var", func() {
		GinkgoT().Setenv("FAILOVER_TEST_KEY", "k1")
		Expect(ProxyConfig{APIKeyEnv: "FAILOVER_TEST_KEY"}.ResolveAPIKey()).To(Equal("k1"))
	})
	It("fails on an unset env var", func() {
		_, err := ProxyConfig{APIKeyEnv: "FAILOVER_TEST_UNSET_KEY"}.ResolveAPIKey()
		Expect(err).To(HaveOccurred())
	})
	It("reads and trims the key file", func() {
		f := filepath.Join(GinkgoT().TempDir(), "key")
		Expect(os.WriteFile(f, []byte(" k2\n"), 0o600)).To(Succeed())
		Expect(ProxyConfig{APIKeyFile: f}.ResolveAPIKey()).To(Equal("k2"))
	})
	It("returns empty when nothing is set", func() {
		Expect(ProxyConfig{}.ResolveAPIKey()).To(Equal(""))
	})
})
```

(add `os` and `path/filepath` imports)

`core/services/failover/prober_test.go`:

```go
package failover

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

type fakeUpstream struct {
	mu     sync.Mutex
	srv    *httptest.Server
	models []string
	status int
	paths  []string
	auth   string
}

func newFakeUpstream() *fakeUpstream {
	u := &fakeUpstream{status: http.StatusOK}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.paths = append(u.paths, r.Method+" "+r.URL.Path)
		u.auth = r.Header.Get("Authorization")
		status, models := u.status, u.models
		u.mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		if r.URL.Path == "/v1/models" {
			var data []map[string]string
			for _, m := range models {
				data = append(data, map[string]string{"id": m})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	return u
}

type fakeBackend struct {
	grpc.Backend
	healthy    bool
	predictErr error
	predicted  bool
}

func (b *fakeBackend) HealthCheck(context.Context) (bool, error) { return b.healthy, nil }
func (b *fakeBackend) Predict(context.Context, *pb.PredictOptions, ...ggrpc.CallOption) (*pb.Reply, error) {
	b.predicted = true
	return &pb.Reply{}, b.predictErr
}

var _ = Describe("DefaultProber", func() {
	var (
		up  *fakeUpstream
		p   *DefaultProber
		ctx = context.Background()
	)

	BeforeEach(func() {
		up = newFakeUpstream()
		DeferCleanup(up.srv.Close)
		p = NewProber(nil, "")
	})

	proxied := func(name, upstreamModel string, usecases ...string) config.ModelConfig {
		c := config.ModelConfig{Name: name, Backend: "cloud-proxy", KnownUsecaseStrings: usecases}
		c.KnownUsecases = config.GetUsecasesFromYAML(usecases)
		c.Proxy.UpstreamURL = up.srv.URL + "/v1/chat/completions"
		c.Proxy.UpstreamModel = upstreamModel
		return c
	}

	DescribeTable("UpstreamBase",
		func(in, want string) {
			got, err := UpstreamBase(in)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("full endpoint", "https://h:8080/v1/chat/completions", "https://h:8080"),
		Entry("path prefix", "https://h/api/v1/chat/completions", "https://h/api"),
		Entry("bare host", "https://h", "https://h"),
		Entry("bare host slash", "https://h/", "https://h"),
	)

	It("passes liveness when the upstream lists the model", func() {
		up.models = []string{"big-llm"}
		Expect(p.Liveness(ctx, proxied("argus-llm", "big-llm"), KindRemote, false)).To(Succeed())
		Expect(up.paths).To(ContainElement("GET /v1/models"))
	})

	It("uses the target name when upstream_model is empty", func() {
		up.models = []string{"argus-llm"}
		Expect(p.Liveness(ctx, proxied("argus-llm", ""), KindRemote, false)).To(Succeed())
	})

	It("fails liveness when the model is not listed or the upstream errors", func() {
		up.models = []string{"other"}
		Expect(p.Liveness(ctx, proxied("argus-llm", ""), KindRemote, false)).To(MatchError(ContainSubstring("does not list")))
		up.status = http.StatusServiceUnavailable
		Expect(p.Liveness(ctx, proxied("argus-llm", ""), KindRemote, false)).To(MatchError(ContainSubstring("503")))
	})

	It("sends the API key as a bearer token", func() {
		GinkgoT().Setenv("FAILOVER_PROBE_KEY", "sekret")
		up.models = []string{"argus-llm"}
		c := proxied("argus-llm", "")
		c.Proxy.APIKeyEnv = "FAILOVER_PROBE_KEY"
		Expect(p.Liveness(ctx, c, KindRemote, false)).To(Succeed())
		Expect(up.auth).To(Equal("Bearer sekret"))
	})

	DescribeTable("remote inference hits the usecase endpoint",
		func(usecase, path string) {
			Expect(p.Inference(ctx, proxied("m", "", usecase), KindRemote, false)).To(Succeed())
			Expect(up.paths).To(ContainElement("POST " + path))
		},
		Entry("chat", "chat", "/v1/chat/completions"),
		Entry("embeddings", "embeddings", "/v1/embeddings"),
		Entry("transcription", "transcript", "/v1/audio/transcriptions"),
		Entry("tts", "tts", "/v1/audio/speech"),
	)

	It("uses HealthCheck for warm local liveness and Predict for local chat inference", func() {
		b := &fakeBackend{healthy: true}
		p = NewProber(func(context.Context, config.ModelConfig) (grpc.Backend, error) { return b, nil }, "")
		c := config.ModelConfig{Name: "gemma", Backend: "llama-cpp", KnownUsecaseStrings: []string{"chat"}}
		c.KnownUsecases = config.GetUsecasesFromYAML(c.KnownUsecaseStrings)
		Expect(p.Liveness(ctx, c, KindLocal, true)).To(Succeed())
		b.healthy = false
		Expect(p.Liveness(ctx, c, KindLocal, true)).To(HaveOccurred())
		Expect(p.Inference(ctx, c, KindLocal, true)).To(Succeed())
		Expect(b.predicted).To(BeTrue())
		b.predictErr = errors.New("boom")
		Expect(p.Inference(ctx, c, KindLocal, true)).To(HaveOccurred())
	})

	It("checks the model file for cold local liveness without loading", func() {
		dir := GinkgoT().TempDir()
		p = NewProber(func(context.Context, config.ModelConfig) (grpc.Backend, error) {
			Fail("cold liveness must not load the model")
			return nil, nil
		}, dir)
		c := config.ModelConfig{Name: "cold", Backend: "llama-cpp"}
		c.Model = "weights.gguf"
		Expect(p.Liveness(ctx, c, KindLocal, false)).To(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, "weights.gguf"), []byte("x"), 0o600)).To(Succeed())
		Expect(p.Liveness(ctx, c, KindLocal, false)).To(Succeed())
		c.Model = "org/some-hf-repo" // no extension: downloaded on demand
		Expect(p.Liveness(ctx, c, KindLocal, false)).To(Succeed())
	})
})
```

If `c.Model` is not directly assignable (it lives in an embedded struct), set it through that struct, e.g. `c.PredictionOptions.Model = "weights.gguf"`; use whatever `validateFailover` reads as `c.Model`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./core/config/... ./core/services/failover/... 2>&1 | tail -5`
Expected: compile failure.

- [ ] **Step 3: Implement `ResolveAPIKey`**

In `core/config/model_config.go` after the `ProxyConfig` constants:

```go
// ResolveAPIKey returns the upstream key from api_key_env or api_key_file, or
// "" when neither is set. The cloud-proxy backend applies the same rules.
func (p ProxyConfig) ResolveAPIKey() (string, error) {
	switch {
	case p.APIKeyEnv != "":
		v, ok := os.LookupEnv(p.APIKeyEnv)
		if !ok {
			return "", fmt.Errorf("proxy api_key_env %q is not set", p.APIKeyEnv)
		}
		return v, nil
	case p.APIKeyFile != "":
		b, err := os.ReadFile(p.APIKeyFile)
		if err != nil {
			return "", fmt.Errorf("proxy api_key_file: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return "", nil
}
```

- [ ] **Step 4: Implement the prober**

`core/services/failover/prober.go`:

```go
package failover

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// LoadFunc returns the backend for a local target, loading it if needed.
type LoadFunc func(ctx context.Context, cfg config.ModelConfig) (grpc.Backend, error)

// DefaultProber probes remote targets over the upstream's OpenAI-compatible
// API and local targets through their gRPC backend.
type DefaultProber struct {
	HTTP      *http.Client
	Load      LoadFunc
	ModelPath string
}

func NewProber(load LoadFunc, modelPath string) *DefaultProber {
	return &DefaultProber{HTTP: &http.Client{}, Load: load, ModelPath: modelPath}
}

func (p *DefaultProber) Liveness(ctx context.Context, cfg config.ModelConfig, kind Kind, warm bool) error {
	switch {
	case kind == KindRemote:
		return p.remoteLiveness(ctx, cfg)
	case warm:
		return p.localHealth(ctx, cfg)
	}
	return p.coldLiveness(cfg)
}

func (p *DefaultProber) Inference(ctx context.Context, cfg config.ModelConfig, kind Kind, warm bool) error {
	if kind == KindRemote {
		return p.remoteInference(ctx, cfg)
	}
	return p.localInference(ctx, cfg)
}

// UpstreamBase strips the endpoint path from a cloud-proxy upstream_url:
// everything from "/v1" on, so a path prefix before it survives.
func UpstreamBase(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid upstream_url %q", raw)
	}
	path := u.Path
	if i := strings.Index(path, "/v1"); i >= 0 {
		path = path[:i]
	}
	return u.Scheme + "://" + u.Host + strings.TrimSuffix(path, "/"), nil
}

// UpstreamModel is the model name the upstream knows the target by.
func UpstreamModel(cfg config.ModelConfig) string {
	if cfg.Proxy.UpstreamModel != "" {
		return cfg.Proxy.UpstreamModel
	}
	return cfg.Name
}

func (p *DefaultProber) authorize(req *http.Request, cfg config.ModelConfig) error {
	key, err := cfg.Proxy.ResolveAPIKey()
	if err != nil || key == "" {
		return err
	}
	if cfg.Proxy.Provider == config.ProxyProviderAnthropic {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+key)
	return nil
}

func (p *DefaultProber) do(req *http.Request, cfg config.ModelConfig) (*http.Response, error) {
	if err := p.authorize(req, cfg); err != nil {
		return nil, err
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("upstream %s: HTTP %d", req.URL.Path, resp.StatusCode)
	}
	return resp, nil
}

func (p *DefaultProber) remoteLiveness(ctx context.Context, cfg config.ModelConfig) error {
	base, err := UpstreamBase(cfg.Proxy.UpstreamURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return err
	}
	resp, err := p.do(req, cfg)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&list); err != nil {
		return fmt.Errorf("upstream /v1/models: %w", err)
	}
	want := UpstreamModel(cfg)
	for _, d := range list.Data {
		if d.ID == want {
			return nil
		}
	}
	return fmt.Errorf("upstream does not list model %q", want)
}

func (p *DefaultProber) remoteInference(ctx context.Context, cfg config.ModelConfig) error {
	base, err := UpstreamBase(cfg.Proxy.UpstreamURL)
	if err != nil {
		return err
	}
	model := UpstreamModel(cfg)
	ping := []map[string]string{{"role": "user", "content": "ping"}}
	switch {
	case cfg.HasUsecases(config.FLAG_CHAT) || cfg.HasUsecases(config.FLAG_COMPLETION):
		if cfg.Proxy.Provider == config.ProxyProviderAnthropic {
			return p.postJSON(ctx, cfg, base+"/v1/messages", map[string]any{"model": model, "max_tokens": 1, "messages": ping})
		}
		return p.postJSON(ctx, cfg, base+"/v1/chat/completions", map[string]any{"model": model, "max_tokens": 1, "messages": ping})
	case cfg.HasUsecases(config.FLAG_EMBEDDINGS):
		return p.postJSON(ctx, cfg, base+"/v1/embeddings", map[string]any{"model": model, "input": "ping"})
	case cfg.HasUsecases(config.FLAG_TRANSCRIPT):
		return p.postTranscription(ctx, cfg, base, model)
	case cfg.HasUsecases(config.FLAG_TTS):
		return p.postJSON(ctx, cfg, base+"/v1/audio/speech", map[string]any{"model": model, "input": "ok"})
	}
	// Image, video and other costly usecases: liveness is the confirmation.
	return p.remoteLiveness(ctx, cfg)
}

func (p *DefaultProber) postJSON(ctx context.Context, cfg config.ModelConfig, endpoint string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.do(req, cfg)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.Body.Close()
}

func (p *DefaultProber) postTranscription(ctx context.Context, cfg config.ModelConfig, base, model string) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", model)
	fw, err := mw.CreateFormFile("file", "probe.wav")
	if err != nil {
		return err
	}
	_, _ = fw.Write(silenceWAV())
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/audio/transcriptions", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := p.do(req, cfg)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.Body.Close()
}

// silenceWAV is 200 ms of 16 kHz mono 16-bit silence.
func silenceWAV() []byte {
	const rate, samples = 16000, 3200
	data := samples * 2
	b := make([]byte, 44+data)
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+data))
	copy(b[8:], "WAVE")
	copy(b[12:], "fmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1) // PCM
	binary.LittleEndian.PutUint16(b[22:], 1) // mono
	binary.LittleEndian.PutUint32(b[24:], rate)
	binary.LittleEndian.PutUint32(b[28:], rate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(data))
	return b
}

func (p *DefaultProber) localHealth(ctx context.Context, cfg config.ModelConfig) error {
	if p.Load == nil {
		return errors.New("failover: no backend loader configured")
	}
	// Load returns the running backend, or starts it again after a crash.
	b, err := p.Load(ctx, cfg)
	if err != nil {
		return err
	}
	ok, err := b.HealthCheck(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("backend health check failed")
	}
	return nil
}

func (p *DefaultProber) localInference(ctx context.Context, cfg config.ModelConfig) error {
	if p.Load == nil {
		return errors.New("failover: no backend loader configured")
	}
	b, err := p.Load(ctx, cfg)
	if err != nil {
		return err
	}
	switch {
	case cfg.HasUsecases(config.FLAG_CHAT) || cfg.HasUsecases(config.FLAG_COMPLETION):
		_, err = b.Predict(ctx, &pb.PredictOptions{Prompt: "ping", Tokens: 1})
		return err
	case cfg.HasUsecases(config.FLAG_EMBEDDINGS):
		_, err = b.Embeddings(ctx, &pb.PredictOptions{Embeddings: "ping"})
		return err
	}
	// A backend process that answers HealthCheck rarely fails only for TTS or
	// transcription, so a real request adds little here.
	return p.localHealth(ctx, cfg)
}

// coldLiveness checks the model file without loading the model.
func (p *DefaultProber) coldLiveness(cfg config.ModelConfig) error {
	f := cfg.Model
	if f == "" || p.ModelPath == "" || strings.Contains(f, "://") {
		return nil
	}
	path := f
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.ModelPath, f)
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) && filepath.Ext(f) == "" {
			return nil // a repository id, downloaded on demand
		}
		return fmt.Errorf("model file %s: %w", f, err)
	}
	return nil
}
```

Check the `pb.PredictOptions` field names (`Prompt`, `Tokens`, `Embeddings`) in `pkg/grpc/proto` and adjust only the field names if they differ.

- [ ] **Step 5: Align the spec with cold liveness**

In the spec's probe table, change the "local, cold" liveness cell to: "the model file exists (skipped for URLs and repository ids). The model is never loaded only to probe it." (The installed-backend check is left out: the loader installs backends on demand, so a missing backend is not a health signal.) `git add -f` the spec.

- [ ] **Step 6: Run tests**

Run: `go test -race ./core/config/... ./core/services/failover/... 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add core/config core/services/failover
git add -f docs/superpowers/specs
git commit -m "feat(failover): probe remote targets over HTTP and local ones over gRPC

Remote liveness uses /v1/models, which every OpenAI-compatible upstream
serves. Recovery sends one minimal request for the target's usecase.

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 7: Application wiring, warm targets, metrics, traces

**Files:**
- Create: `core/services/failover/metrics.go`, `core/services/failover/trace.go`, `core/application/failover.go`
- Modify: `core/services/failover/manager.go` (`emitLocked` records metrics), `core/application/application.go` (field + accessor), `core/application/startup.go` (construct + run), `core/application/watchdog.go` (`SyncPinnedModelsToWatchdog`), `core/trace/backend_trace.go` (new type)
- Test: `core/services/failover/metrics_test.go`

**Interfaces:**
- Consumes: Tasks 4-6.
- Produces: `(*application.Application).FailoverManager() *failover.Manager`, `failover.RegisterMetrics(m *Manager)`, `failover.RecordAttemptTrace(enabled bool, chain, target string, err error)`, `trace.BackendTraceFailover`.

- [ ] **Step 1: Write the failing test**

`core/services/failover/metrics_test.go`:

```go
package failover

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("metrics", func() {
	It("registers and records without a meter provider", func() {
		src := newFakeSource(remote("a"), local("b"), chainCfg("chain", nil, t("a"), t("b")))
		m := New(src, WithClock(newFakeClock()))
		Expect(func() { RegisterMetrics(m) }).ToNot(Panic())
		Expect(func() { m.ReportFailure("a", errBoom) }).ToNot(Panic())
	})
	It("records an attempt trace only when enabled", func() {
		Expect(func() { RecordAttemptTrace(false, "chain", "a", errBoom) }).ToNot(Panic())
		Expect(func() { RecordAttemptTrace(true, "chain", "a", errBoom) }).ToNot(Panic())
	})
})
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./core/services/failover/... 2>&1 | tail -5`
Expected: `undefined: RegisterMetrics`.

- [ ] **Step 3: Implement metrics and traces**

`core/services/failover/metrics.go`:

```go
package failover

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	metricsOnce sync.Once
	switches    metric.Int64Counter
)

func initMetrics() {
	metricsOnce.Do(func() {
		meter := otel.Meter("github.com/mudler/LocalAI")
		switches, _ = meter.Int64Counter("localai_failover_switches_total",
			metric.WithDescription("Failover chain switches between targets"))
	})
}

func recordSwitch(ev Event) {
	initMetrics()
	if switches == nil {
		return
	}
	switches.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("chain", ev.Chain),
		attribute.String("from", ev.From),
		attribute.String("to", ev.To),
		attribute.String("reason", string(ev.Reason)),
	))
}

// RegisterMetrics exports target health as a gauge. The application calls it
// once for its manager; tests create many managers and skip it.
func RegisterMetrics(m *Manager) {
	meter := otel.Meter("github.com/mudler/LocalAI")
	_, _ = meter.Int64ObservableGauge("localai_failover_target_up",
		metric.WithDescription("1 when a failover target is healthy, 0 otherwise"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			for name, state := range m.targetStates() {
				v := int64(0)
				if state == StateHealthy {
					v = 1
				}
				o.Observe(v, metric.WithAttributes(attribute.String("target", name)))
			}
			return nil
		}))
}
```

Add to `manager.go`:

```go
func (m *Manager) targetStates() map[string]TargetState {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]TargetState, len(m.targets))
	for name, ts := range m.targets {
		out[name] = ts.state
	}
	return out
}
```

and at the top of `emitLocked`:

```go
	if ev.Type == EventChainSwitched {
		recordSwitch(ev)
	}
```

In `core/trace/backend_trace.go`, add to the `BackendTraceType` constants:

```go
	BackendTraceFailover BackendTraceType = "failover"
```

Check `core/http/react-ui/src` for a label map of backend trace types (`grep -rn "image_generation" core/http/react-ui/src`); if one exists, add `failover: 'Failover'` in the same style.

`core/services/failover/trace.go`:

```go
package failover

import (
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/trace"
)

// RecordAttemptTrace shows in the Traces UI why a target was skipped.
func RecordAttemptTrace(enabled bool, chain, target string, err error) {
	if !enabled || err == nil {
		return
	}
	trace.RecordBackendTrace(trace.BackendTrace{
		Timestamp: time.Now(),
		Type:      trace.BackendTraceFailover,
		ModelName: target,
		Summary:   fmt.Sprintf("failover chain %s: %s failed, trying the next target", chain, target),
		Error:     err.Error(),
		Data:      map[string]any{"chain": chain},
	})
}
```

(If `BackendTrace` field names differ, match `core/trace/backend_trace.go`.)

- [ ] **Step 4: Wire the application**

`core/application/application.go`: add field `failoverManager *failover.Manager` to `Application` and:

```go
// FailoverManager serves failover chains. Never nil after New.
func (a *Application) FailoverManager() *failover.Manager { return a.failoverManager }
```

`core/application/failover.go`:

```go
package application

import (
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/xlog"
)

// applyFailoverWarmTargets pins warm failover targets in the watchdog and
// loads them, so a switch does not wait for a cold load.
func (a *Application) applyFailoverWarmTargets(warm []string) {
	a.SyncPinnedModelsToWatchdog()
	for _, name := range warm {
		if _, err := backend.PreloadModelByName(a.ApplicationConfig().Context, a.ModelConfigLoader(), a.ModelLoader(), a.ApplicationConfig(), name); err != nil {
			xlog.Warn("failover: could not preload warm target", "model", name, "error", err)
		}
	}
}
```

`core/application/watchdog.go` in `SyncPinnedModelsToWatchdog`, before `wd.SetPinnedModels(pinned)`:

```go
	if a.failoverManager != nil {
		pinned = failover.MergePinned(pinned, a.failoverManager.WarmTargets())
	}
```

`core/application/startup.go`: next to `application.routerRegistry = router.NewRegistry()` (~251), construct the manager:

```go
	application.failoverManager = failover.New(application.ModelConfigLoader(),
		failover.WithProber(failover.NewProber(func(ctx context.Context, cfg config.ModelConfig) (grpc.Backend, error) {
			return application.ModelLoader().Load(backend.ModelOptions(cfg, options)...)
		}, application.ModelLoader().ModelPath)),
		failover.WithOnWarmChanged(application.applyFailoverWarmTargets),
	)
```

and directly after the `LoadToMemory` preload loop (~539-548), which runs after `initializeWatchdog`, start it:

```go
	failover.RegisterMetrics(application.failoverManager)
	go application.failoverManager.Run(options.Context)
```

`grpc` here is `github.com/mudler/LocalAI/pkg/grpc`. If `startup.go` already imports a different package as `grpc`, alias this one `lagrpc`.

- [ ] **Step 5: Build and test**

Run: `go build ./... && go test ./core/services/failover/... ./core/application/... 2>&1 | tail -10`
Expected: build OK, tests PASS.

- [ ] **Step 6: Commit**

```bash
git add core/services/failover core/application core/trace core/http/react-ui/src
git commit -m "feat(failover): run the chain manager and keep warm targets loaded

Warm local targets are pinned in the watchdog and preloaded. Switches
and target health are exported as metrics, skipped attempts as traces.

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 8: HTTP chain resolution and in-request retry

**Files:**
- Create: `core/http/middleware/failover.go`, `core/http/middleware/failover_test.go`
- Modify: `core/http/middleware/request.go` (`RequestExtractor` field; `SetModelAndConfig` wraps its body with `failoverRetry`; resolution after the alias block at ~181-195)
- Modify: `core/http/middleware/context_keys.go` (new key)
- Modify: `core/http/app.go:457` (call `SetFailoverManager`)

**Interfaces:**
- Consumes: `failover.Manager.Plan`, `Attempt` methods, `failover.IsRetryable`, `failover.RecordAttemptTrace`.
- Produces: `(*RequestExtractor).SetFailoverManager(*failover.Manager)`, `ContextKeyFailoverAttempt = "failover.attempt"`, `HeaderServedModel = "X-LocalAI-Served-Model"`, `HeaderFailover = "X-LocalAI-Failover"`, `MaxFailoverReplayBody = 32 << 20`.

Design note for the implementer: the retry loop wraps the whole `SetModelAndConfig` body plus `next`, so every attempt binds the request again from the replayed body. No route file changes. `failoverWriter` holds back a response with status >= 500 only while the request is resolved to a chain, so other requests are unaffected.

- [ ] **Step 1: Write the failing tests**

`core/http/middleware/failover_test.go` (use the same package and suite as `request_config_revision_test.go`):

```go
package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("failover chains in the request pipeline", func() {
	var (
		app      *echo.Echo
		fm       *failover.Manager
		mu       sync.Mutex
		calls    []string
		behavior map[string]func(c echo.Context) error
	)

	served := func(c echo.Context) error {
		cfg := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		return c.JSON(http.StatusOK, map[string]string{"served": cfg.Name})
	}

	handler := func(c echo.Context) error {
		cfg := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		mu.Lock()
		calls = append(calls, cfg.Name)
		b := behavior[cfg.Name]
		mu.Unlock()
		if b == nil {
			return served(c)
		}
		return b(c)
	}

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}
	chat := func(model string) *httptest.ResponseRecorder {
		return post("/v1/chat/completions", `{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`)
	}

	BeforeEach(func() {
		calls = nil
		behavior = map[string]func(c echo.Context) error{}
		dir := GinkgoT().TempDir()
		write := func(name, body string) {
			Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600)).To(Succeed())
		}
		write("a", "name: a\nbackend: fake-a\n")
		write("b", "name: b\nbackend: fake-b\n")
		write("plain", "name: plain\nbackend: fake-p\n")
		write("chain", "name: chain\nfailover:\n  targets:\n    - model: a\n    - model: b\n")

		ss := &system.SystemState{Model: system.Model{ModelsPath: dir}}
		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = ss
		mcl := config.NewModelConfigLoader(dir)
		Expect(mcl.LoadModelConfigsFromPath(dir)).To(Succeed())
		re := NewRequestExtractor(mcl, model.NewModelLoader(ss), appConfig)
		fm = failover.New(mcl)
		re.SetFailoverManager(fm)

		app = echo.New()
		// echo's default handler hides internal error messages; the specs
		// below check which target's error reached the client.
		app.HTTPErrorHandler = func(err error, c echo.Context) {
			code := http.StatusInternalServerError
			var he *echo.HTTPError
			if errors.As(err, &he) {
				code = he.Code
			}
			_ = c.JSON(code, map[string]string{"error": err.Error()})
		}
		app.POST("/v1/chat/completions", handler,
			re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
		app.POST("/v1/audio/transcriptions", handler,
			re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
	})

	It("serves from the next target when the first fails before responding", func() {
		behavior["a"] = func(echo.Context) error { return errors.New("dial tcp: connection refused") }
		rec := chat("chain")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring(`"served":"b"`))
		Expect(rec.Header().Get(HeaderServedModel)).To(Equal("b"))
		Expect(rec.Header().Get(HeaderFailover)).To(Equal("fallback"))
		Expect(calls).To(Equal([]string{"a", "b"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Active).To(Equal("b"))
	})

	It("serves the primary without the failover header", func() {
		rec := chat("chain")
		Expect(rec.Header().Get(HeaderServedModel)).To(Equal("a"))
		Expect(rec.Header().Get(HeaderFailover)).To(BeEmpty())
	})

	It("drops a buffered 5xx response and retries", func() {
		behavior["a"] = func(c echo.Context) error {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "no healthy nodes"})
		}
		rec := chat("chain")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).ToNot(ContainSubstring("no healthy nodes"))
	})

	It("does not retry after streaming started, and trips the target", func() {
		behavior["a"] = func(c echo.Context) error {
			c.Response().Header().Set("Content-Type", "text/event-stream")
			_, _ = c.Response().Write([]byte("data: x\n\n"))
			c.Response().Flush()
			return errors.New("connection reset by peer")
		}
		rec := chat("chain")
		Expect(rec.Body.String()).To(HavePrefix("data: x"))
		Expect(calls).To(Equal([]string{"a"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(failover.StateDown))
	})

	It("does not retry or trip on 4xx", func() {
		behavior["a"] = func(echo.Context) error { return echo.NewHTTPError(http.StatusBadRequest, "bad") }
		rec := chat("chain")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(calls).To(Equal([]string{"a"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(failover.StateHealthy))
	})

	It("does not retry when the client cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		behavior["a"] = func(echo.Context) error { cancel(); return context.Canceled }
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"chain","messages":[{"role":"user","content":"hi"}]}`)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		app.ServeHTTP(httptest.NewRecorder(), req)
		Expect(calls).To(Equal([]string{"a"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(failover.StateHealthy))
	})

	It("gives each attempt a fresh request", func() {
		behavior["a"] = func(c echo.Context) error {
			in := c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
			in.Messages = nil
			return errors.New("dial tcp: connection refused")
		}
		behavior["b"] = func(c echo.Context) error {
			in := c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
			Expect(in.Messages).To(HaveLen(1))
			return served(c)
		}
		Expect(chat("chain").Code).To(Equal(http.StatusOK))
	})

	It("degraded: tries every target in priority order and returns the last error", func() {
		behavior["a"] = func(echo.Context) error { return errors.New("dial tcp: a down") }
		behavior["b"] = func(echo.Context) error { return errors.New("dial tcp: b down") }
		chat("chain") // trips both
		calls = nil
		rec := chat("chain")
		Expect(calls).To(Equal([]string{"a", "b"}))
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(rec.Body.String()).To(ContainSubstring("b down"))
		Expect(rec.Header().Get(HeaderFailover)).To(Equal("degraded"))
	})

	It("replays a multipart body for the next target", func() {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("model", "chain")
		fw, _ := mw.CreateFormFile("file", "a.wav")
		_, _ = fw.Write(bytes.Repeat([]byte{7}, 4096))
		Expect(mw.Close()).To(Succeed())
		size := func(c echo.Context) int64 {
			fh, err := c.FormFile("file")
			Expect(err).ToNot(HaveOccurred())
			f, _ := fh.Open()
			n, _ := io.Copy(io.Discard, f)
			return n
		}
		behavior["a"] = func(c echo.Context) error { size(c); return errors.New("dial tcp: refused") }
		behavior["b"] = func(c echo.Context) error {
			Expect(size(c)).To(Equal(int64(4096)))
			return served(c)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(calls).To(Equal([]string{"a", "b"}))
	})

	It("leaves plain models untouched", func() {
		behavior["plain"] = func(c echo.Context) error {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "boom"})
		}
		rec := chat("plain")
		Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		Expect(rec.Body.String()).To(ContainSubstring("boom"))
		Expect(rec.Header().Get(HeaderServedModel)).To(BeEmpty())
	})
})
```

If `SetModelAndConfig` rejects `backend: fake-a` configs in this fixture (for example through the existence check), give the fixture configs the backend the revision test uses (`llama-cpp`); the handler never loads a backend.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./core/http/middleware/... 2>&1 | tail -5`
Expected: compile failure (`SetFailoverManager undefined`).

- [ ] **Step 3: Implement**

In `core/http/middleware/context_keys.go` add:

```go
	// ContextKeyFailoverAttempt holds the *failoverState of a request whose
	// model is a failover chain.
	ContextKeyFailoverAttempt = "failover.attempt"
```

`core/http/middleware/failover.go`:

```go
package middleware

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/failover"
)

const (
	HeaderServedModel = "X-LocalAI-Served-Model"
	HeaderFailover    = "X-LocalAI-Failover"
)

// MaxFailoverReplayBody caps the request body kept for a retry. A larger body
// is still served, by one target only.
const MaxFailoverReplayBody = 32 << 20

type failoverState struct {
	attempt *failover.Attempt
}

// SetFailoverManager enables failover chains. Without it, a request for a
// chain fails with 503.
func (re *RequestExtractor) SetFailoverManager(m *failover.Manager) { re.failover = m }

// resolveFailover returns the config of the target that should serve this
// attempt. The first attempt plans the chain; retries reuse the plan.
func (re *RequestExtractor) resolveFailover(c echo.Context, requested string, chain *config.ModelConfig) (*config.ModelConfig, error) {
	st, _ := c.Get(ContextKeyFailoverAttempt).(*failoverState)
	if st == nil || st.attempt.Chain() != chain.Name {
		if re.failover == nil {
			return nil, fmt.Errorf("model %q is a failover chain, but failover is not running", chain.Name)
		}
		att, err := re.failover.Plan(chain.Name)
		if err != nil {
			return nil, err
		}
		st = &failoverState{attempt: att}
		c.Set(ContextKeyFailoverAttempt, st)
	}
	for {
		cfg, err := re.loadFailoverTarget(st.attempt.Target())
		if err == nil {
			c.Set(ContextKeyRequestedModel, requested)
			c.Set(ContextKeyServedModel, cfg.Name)
			setFailoverHeaders(c.Response().Header(), st.attempt)
			return cfg, nil
		}
		if !st.attempt.Fail(err) {
			// Clear the state so the retry wrapper sends this 503 as is.
			c.Set(ContextKeyFailoverAttempt, nil)
			return nil, err
		}
	}
}

func (re *RequestExtractor) loadFailoverTarget(name string) (*config.ModelConfig, error) {
	cfg, err := re.modelConfigLoader.LoadModelConfigFileByNameDefaultOptions(name, re.applicationConfig)
	if err != nil {
		return nil, err
	}
	resolved, _, err := re.modelConfigLoader.ResolveAlias(cfg)
	return resolved, err
}

func setFailoverHeaders(h http.Header, att *failover.Attempt) {
	h.Set(HeaderServedModel, att.Target())
	switch {
	case att.Degraded():
		h.Set(HeaderFailover, "degraded")
	case att.Target() != att.Primary():
		h.Set(HeaderFailover, "fallback")
	default:
		h.Del(HeaderFailover)
	}
}

// failoverRetry runs h again on the next target while the response is not
// committed. h is SetModelAndConfig's body plus the rest of the chain, so
// every attempt binds the request again from the replayed body.
func failoverRetry(appConfig *config.ApplicationConfig, h echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := c.Request()
		src := req.Body
		if src == nil {
			src = http.NoBody
		}
		rec := &replayBody{src: src, limit: MaxFailoverReplayBody}
		req.Body = rec
		resp := c.Response()
		orig := resp.Writer
		baseHeader := resp.Header().Clone()
		defer func() { resp.Writer = orig }()
		active := func() bool {
			st, _ := c.Get(ContextKeyFailoverAttempt).(*failoverState)
			return st != nil
		}
		tracing := appConfig != nil && appConfig.EnableTracing
		for {
			w := &failoverWriter{ResponseWriter: orig, active: active}
			resp.Writer = w
			err := h(c)
			st, _ := c.Get(ContextKeyFailoverAttempt).(*failoverState)
			if st == nil {
				w.release()
				return err
			}
			att := st.attempt
			status := w.held
			if err == nil && status == 0 {
				att.Succeed()
				return nil
			}
			cause := attemptError(err, status, w.body.Bytes())
			retryable := req.Context().Err() == nil && failover.IsRetryable(err, status)
			if !retryable || w.committed || !rec.replayable() {
				if retryable {
					att.Report(cause)
				}
				w.release()
				return err
			}
			failover.RecordAttemptTrace(tracing, att.Chain(), att.Target(), cause)
			if !att.Fail(cause) {
				w.release()
				return err
			}
			req.Body = rec.replay()
			req.MultipartForm, req.Form, req.PostForm = nil, nil, nil
			resetResponse(resp, baseHeader)
		}
	}
}

func attemptError(err error, status int, body []byte) error {
	if err != nil {
		return err
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return fmt.Errorf("HTTP %d: %s", status, msg)
}

func resetResponse(resp *echo.Response, base http.Header) {
	h := resp.Header()
	for k := range h {
		delete(h, k)
	}
	for k, v := range base {
		h[k] = slices.Clone(v)
	}
	resp.Committed = false
	resp.Status = http.StatusOK
	resp.Size = 0
}

// failoverWriter holds back an error response (status >= 500) of a chain
// request until the handler returns, so the retry can drop it.
type failoverWriter struct {
	http.ResponseWriter
	active    func() bool
	held      int
	body      bytes.Buffer
	committed bool
}

func (w *failoverWriter) WriteHeader(code int) {
	if w.held != 0 {
		return
	}
	if !w.committed && code >= 500 && w.active() {
		w.held = code
		return
	}
	w.committed = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *failoverWriter) Write(b []byte) (int, error) {
	if w.held != 0 {
		return w.body.Write(b)
	}
	w.committed = true
	return w.ResponseWriter.Write(b)
}

func (w *failoverWriter) Flush() {
	w.release()
	w.committed = true
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *failoverWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.committed = true
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("failover: response writer cannot hijack")
	}
	return h.Hijack()
}

func (w *failoverWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// release sends a held error response to the client.
func (w *failoverWriter) release() {
	if w.held == 0 {
		return
	}
	code := w.held
	w.held = 0
	w.committed = true
	w.ResponseWriter.WriteHeader(code)
	_, _ = w.ResponseWriter.Write(w.body.Bytes())
	w.body.Reset()
}

// replayBody records what the handler reads, up to limit, so the body can be
// sent again to the next target. Decoders often stop at the end of the value
// without reading to EOF, so a replay is the recorded bytes followed by
// whatever the previous attempt left unread.
type replayBody struct {
	src      io.ReadCloser
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (r *replayBody) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 && !r.overflow {
		if r.buf.Len()+n > r.limit {
			r.overflow = true
			r.buf.Reset()
		} else {
			r.buf.Write(p[:n])
		}
	}
	return n, err
}

func (r *replayBody) Close() error { return r.src.Close() }

// replayable reports whether everything read so far was kept.
func (r *replayBody) replayable() bool { return !r.overflow }

// replay rewinds to the start of the body and keeps recording, so a third
// attempt can replay too.
func (r *replayBody) replay() io.ReadCloser {
	data := bytes.Clone(r.buf.Bytes())
	rest := r.src
	r.src = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(data), rest), rest}
	r.buf.Reset()
	return r
}
```

In `core/http/middleware/request.go`:

1. Add field `failover *failover.Manager` to `RequestExtractor`.
2. In `SetModelAndConfig`, wrap the returned handler:

```go
func (re *RequestExtractor) SetModelAndConfig(initializer func() schema.LocalAIRequest) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return failoverRetry(re.applicationConfig, func(c echo.Context) error {
			// ... existing body, unchanged ...
		})
	}
}
```

3. After the alias block (after `cfg = resolved` at ~195) and before the disabled check, add:

```go
			// A failover chain resolves to one of its targets, like an alias.
			// failoverRetry re-runs this middleware for the next target.
			if cfg != nil && cfg.IsFailover() {
				resolved, fErr := re.resolveFailover(c, modelName, cfg)
				if fErr != nil {
					return c.JSON(http.StatusServiceUnavailable, schema.ErrorResponse{
						Error: &schema.APIError{
							Message: fErr.Error(),
							Code:    http.StatusServiceUnavailable,
							Type:    "failover_unavailable",
						},
					})
				}
				cfg = resolved
			}
```

In `core/http/app.go` after line 457:

```go
	requestExtractor.SetFailoverManager(application.FailoverManager())
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./core/http/middleware/... 2>&1 | tail -15`
Expected: PASS, including the existing middleware specs.

- [ ] **Step 5: Run the wider HTTP suite**

Run: `make build-mock-backend && go test ./core/http/... 2>&1 | tail -15`
Expected: PASS (the retry wrapper is a no-op for plain models).

- [ ] **Step 6: Commit**

```bash
git add core/http
git commit -m "feat(failover): resolve chains per request and retry on the next target

The retry wraps SetModelAndConfig, so each attempt binds the request
again from a replayed body. A 5xx of a chain request is held back until
the handler returns, and a streamed response is never retried.

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 9: REST and SSE endpoints

**Files:**
- Create: `core/http/endpoints/localai/failover.go`, `core/http/endpoints/localai/failover_test.go`
- Modify: `core/http/routes/localai.go` (register routes), `core/http/endpoints/localai/api_instructions.go` (+ test count 19 → 20 in `api_instructions_test.go:42`)
- Test: auth coverage next to the existing aliases auth tests (find them with `grep -rn '"/api/aliases"' core/http --include=*_test.go`)

**Interfaces:**
- Consumes: `failover.Manager` `Status`, `ChainStatus`, `Pin`, `Unpin`, `Subscribe`, errors.
- Produces: routes `GET /api/failover`, `GET /api/failover/events`, `GET /api/failover/:chain`, `POST /api/failover/:chain/pin`, `DELETE /api/failover/:chain/pin`; type `localai.FailoverChainsResponse`.

- [ ] **Step 1: Write the failing tests**

`core/http/endpoints/localai/failover_test.go` (match the package name and suite used by the other `_test.go` files in that directory):

```go
package localai

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/failover"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type mapSource map[string]config.ModelConfig

func (s mapSource) GetModelConfig(n string) (config.ModelConfig, bool) { c, ok := s[n]; return c, ok }
func (s mapSource) GetAllModelsConfigs() []config.ModelConfig {
	var out []config.ModelConfig
	for _, c := range s {
		out = append(out, c)
	}
	return out
}

var _ = Describe("failover endpoints", func() {
	var (
		e  *echo.Echo
		fm *failover.Manager
	)

	BeforeEach(func() {
		src := mapSource{
			"a": {Name: "a", Backend: "cloud-proxy"},
			"b": {Name: "b", Backend: "llama-cpp"},
			"chain": {Name: "chain", Failover: &config.FailoverConfig{Targets: []config.FailoverTarget{{Model: "a"}, {Model: "b"}}}},
		}
		fm = failover.New(src)
		e = echo.New()
		e.GET("/api/failover", ListFailoverChainsEndpoint(fm))
		e.GET("/api/failover/events", FailoverEventsEndpoint(fm))
		e.GET("/api/failover/:chain", GetFailoverChainEndpoint(fm))
		e.POST("/api/failover/:chain/pin", PinFailoverTargetEndpoint(fm))
		e.DELETE("/api/failover/:chain/pin", UnpinFailoverTargetEndpoint(fm))
	})

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("lists chains", func() {
		rec := do(http.MethodGet, "/api/failover", "")
		Expect(rec.Code).To(Equal(http.StatusOK))
		var out FailoverChainsResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Chains).To(HaveLen(1))
		Expect(out.Chains[0].Active).To(Equal("a"))
	})

	It("gets one chain or 404", func() {
		Expect(do(http.MethodGet, "/api/failover/chain", "").Code).To(Equal(http.StatusOK))
		Expect(do(http.MethodGet, "/api/failover/nope", "").Code).To(Equal(http.StatusNotFound))
	})

	It("pins and unpins", func() {
		rec := do(http.MethodPost, "/api/failover/chain/pin", `{"target":"b"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Active).To(Equal("b"))
		Expect(do(http.MethodPost, "/api/failover/chain/pin", `{"target":"zzz"}`).Code).To(Equal(http.StatusBadRequest))
		Expect(do(http.MethodPost, "/api/failover/chain/pin", `{}`).Code).To(Equal(http.StatusBadRequest))
		Expect(do(http.MethodPost, "/api/failover/nope/pin", `{"target":"a"}`).Code).To(Equal(http.StatusNotFound))
		Expect(do(http.MethodDelete, "/api/failover/chain/pin", "").Code).To(Equal(http.StatusOK))
		st, _ = fm.ChainStatus("chain")
		Expect(st.Pinned).To(BeNil())
	})

	It("streams a snapshot, then switch events", func() {
		srv := httptest.NewServer(e)
		defer srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/failover/events", nil)
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/event-stream"))
		r := bufio.NewReader(resp.Body)
		next := func() string {
			for {
				line, err := r.ReadString('\n')
				Expect(err).ToNot(HaveOccurred())
				if strings.HasPrefix(line, "event: ") {
					return strings.TrimSpace(strings.TrimPrefix(line, "event: "))
				}
			}
		}
		Expect(next()).To(Equal("snapshot"))
		Expect(fm.Pin("chain", "b")).To(Succeed())
		Expect(next()).To(Equal("chain.switched"))
	})
})
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./core/http/endpoints/localai/... 2>&1 | tail -5`
Expected: compile failure.

- [ ] **Step 3: Implement the handlers**

`core/http/endpoints/localai/failover.go`:

```go
package localai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/failover"
)

type FailoverChainsResponse struct {
	Chains []failover.ChainStatus `json:"chains"`
}

type FailoverPinRequest struct {
	Target string `json:"target"`
}

func failoverError(c echo.Context, code int, msg string) error {
	return c.JSON(code, schema.ErrorResponse{Error: &schema.APIError{Message: msg, Code: code, Type: "failover_error"}})
}

// ListFailoverChainsEndpoint lists failover chains and the health of their targets
//
//	@Summary	List failover chains and the health of their targets
//	@Tags		failover
//	@Produce	json
//	@Success	200	{object}	FailoverChainsResponse
//	@Router		/api/failover [get]
func ListFailoverChainsEndpoint(fm *failover.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, FailoverChainsResponse{Chains: fm.Status()})
	}
}

// GetFailoverChainEndpoint returns one failover chain
//
//	@Summary	Get one failover chain
//	@Tags		failover
//	@Produce	json
//	@Param		chain	path		string	true	"Chain name"
//	@Success	200		{object}	failover.ChainStatus
//	@Failure	404		{object}	schema.ErrorResponse
//	@Router		/api/failover/{chain} [get]
func GetFailoverChainEndpoint(fm *failover.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		st, ok := fm.ChainStatus(c.Param("chain"))
		if !ok {
			return failoverError(c, http.StatusNotFound, fmt.Sprintf("failover chain %q not found", c.Param("chain")))
		}
		return c.JSON(http.StatusOK, st)
	}
}

// PinFailoverTargetEndpoint forces a chain to one target
//
//	@Summary	Pin a failover chain to one target
//	@Tags		failover
//	@Accept		json
//	@Produce	json
//	@Param		chain	path		string				true	"Chain name"
//	@Param		request	body		FailoverPinRequest	true	"Target to pin"
//	@Success	200		{object}	failover.ChainStatus
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Router		/api/failover/{chain}/pin [post]
func PinFailoverTargetEndpoint(fm *failover.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req FailoverPinRequest
		if err := c.Bind(&req); err != nil || req.Target == "" {
			return failoverError(c, http.StatusBadRequest, "request body must set \"target\"")
		}
		chain := c.Param("chain")
		if err := fm.Pin(chain, req.Target); err != nil {
			return pinError(c, err)
		}
		st, _ := fm.ChainStatus(chain)
		return c.JSON(http.StatusOK, st)
	}
}

// UnpinFailoverTargetEndpoint removes a pin
//
//	@Summary	Remove the pin from a failover chain
//	@Tags		failover
//	@Produce	json
//	@Param		chain	path		string	true	"Chain name"
//	@Success	200		{object}	failover.ChainStatus
//	@Failure	404		{object}	schema.ErrorResponse
//	@Router		/api/failover/{chain}/pin [delete]
func UnpinFailoverTargetEndpoint(fm *failover.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		chain := c.Param("chain")
		if err := fm.Unpin(chain); err != nil {
			return pinError(c, err)
		}
		st, _ := fm.ChainStatus(chain)
		return c.JSON(http.StatusOK, st)
	}
}

func pinError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, failover.ErrChainNotFound):
		return failoverError(c, http.StatusNotFound, err.Error())
	case errors.Is(err, failover.ErrTargetNotInChain):
		return failoverError(c, http.StatusBadRequest, err.Error())
	}
	return failoverError(c, http.StatusInternalServerError, err.Error())
}

// FailoverEventsEndpoint streams failover events
//
//	@Summary	Stream failover events (server-sent events)
//	@Description	The first event is "snapshot" with the full state, then "chain.switched" and "target.state" events.
//	@Tags		failover
//	@Produce	text/event-stream
//	@Success	200
//	@Router		/api/failover/events [get]
func FailoverEventsEndpoint(fm *failover.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Subscribe before the snapshot so no event falls between the two.
		events, cancel := fm.Subscribe(64)
		defer cancel()
		w := c.Response()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		send := func(name string, v any) error {
			data, err := json.Marshal(v)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil {
				return err
			}
			w.Flush()
			return nil
		}
		if err := send("snapshot", FailoverChainsResponse{Chains: fm.Status()}); err != nil {
			return nil
		}
		keepalive := time.NewTicker(15 * time.Second)
		defer keepalive.Stop()
		for {
			select {
			case <-c.Request().Context().Done():
				return nil
			case <-keepalive.C:
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return nil
				}
				w.Flush()
			case ev, ok := <-events:
				if !ok {
					return nil
				}
				if err := send(string(ev.Type), ev); err != nil {
					return nil
				}
			}
		}
	}
}
```

- [ ] **Step 4: Register routes and instructions**

In `core/http/routes/localai.go`, next to `/api/aliases` (~92), where `app` / `application` is in scope (use the variable holding `*application.Application`):

```go
	fm := application.FailoverManager()
	router.GET("/api/failover", localai.ListFailoverChainsEndpoint(fm))
	router.GET("/api/failover/events", localai.FailoverEventsEndpoint(fm))
	router.GET("/api/failover/:chain", localai.GetFailoverChainEndpoint(fm))
	router.POST("/api/failover/:chain/pin", localai.PinFailoverTargetEndpoint(fm), adminMiddleware)
	router.DELETE("/api/failover/:chain/pin", localai.UnpinFailoverTargetEndpoint(fm), adminMiddleware)
```

In `api_instructions.go` `instructionDefs`, add:

```go
	{
		Name:        "failover",
		Description: "Model failover chains: target health, pinning and switch events",
		Tags:        []string{"failover"},
		Intro:       "A failover chain is a model config with a failover block. Requests for the chain name are served by its highest-priority healthy target; the X-LocalAI-Served-Model response header names it. Subscribe to GET /api/failover/events (SSE) to follow switches.",
	},
```

and change `HaveLen(19)` to `HaveLen(20)` in `api_instructions_test.go`.

- [ ] **Step 5: Auth tests**

Open the test that covers `/api/aliases` auth (found with the grep above) and add the same cases for:
- `GET /api/failover` without credentials → 401 when auth is enabled; with a user key → 200.
- `POST /api/failover/<chain>/pin` with a non-admin user → 403; with an admin → 200 (or 404 for an unknown chain, which still proves the admin gate passed).

Mirror the existing assertions exactly; do not invent a new auth harness.

- [ ] **Step 6: Swagger and tests**

Run: `make swagger && go test ./core/http/... 2>&1 | tail -15`
Expected: swagger regenerates without errors; tests PASS.

- [ ] **Step 7: Commit**

```bash
git add core/http swagger
git commit -m "feat(failover): expose chain status, pins and events over REST and SSE

Assisted-by: Claude:claude-opus-5-5"
```

(Use the actual swagger output directory if it is not `swagger/`; `git status` shows it.)

---

### Task 10: End-to-end HTTP failover and endpoint audit

**Files:**
- Modify: `tests/e2e/mock-backend/main.go` (`LoadModel` failure trigger, line ~89)
- Modify: `tests/e2e/cloud_proxy_helpers_test.go` (fake upstream serves `GET /v1/models`)
- Modify: `tests/e2e/e2e_suite_test.go` (model configs)
- Create: `tests/e2e/e2e_failover_test.go`

**Interfaces:**
- Consumes: the running app from the e2e suite, Task 8 headers, Task 9 endpoints.

- [ ] **Step 1: Add the mock load-failure trigger**

In `tests/e2e/mock-backend/main.go` `LoadModel`, before the success return:

```go
	// Lets e2e specs build a failover target whose backend cannot load.
	if strings.HasPrefix(in.Model, "fail-load") {
		return &pb.Result{Message: "mock: load failure", Success: false}, nil
	}
```

(add `strings` to the imports if missing)

- [ ] **Step 2: Make the fake upstream answer `/v1/models`**

In `newFakeOpenAIUpstream()` (`cloud_proxy_helpers_test.go:40-97`), add a models list and, at the top of the handler:

```go
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			u.mu.Lock()
			ids := slices.Clone(u.models)
			u.mu.Unlock()
			data := make([]map[string]string, 0, len(ids))
			for _, id := range ids {
				data = append(data, map[string]string{"id": id})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
```

with a `models []string` field and:

```go
func (u *fakeOpenAIUpstream) SetModels(ids ...string) {
	u.mu.Lock()
	u.models = ids
	u.mu.Unlock()
}
```

Use the helper's actual type, mutex and field names. The existing cloud-proxy specs must still pass.

- [ ] **Step 3: Add configs to the suite**

In `tests/e2e/e2e_suite_test.go`, where the existing mock-backend configs are written (lines ~105-125), write these additional configs with the same helper and style. For each family `f` in `chat, completion, embeddings, transcription, tts, image, rerank, vad`:

```yaml
# fail-<f>.yaml
name: fail-<f>
backend: mock-backend
parameters:
  model: fail-load-<f>
```

```yaml
# chain-<f>.yaml
name: chain-<f>
failover:
  targets:
    - model: fail-<f>
    - model: <the existing mock model name used by the suite>
```

The remote chain needs the fake upstreams' URLs, which exist only at runtime. Write those configs in the `BeforeAll` of the remote spec (Step 4) and load them the way `e2e_cloud_proxy_test.go` registers its cloud-proxy models (read that file first and reuse its mechanism, for example writing YAML to the models dir and calling the loader, or `POST /models/import`).

- [ ] **Step 4: Write the e2e specs**

`tests/e2e/e2e_failover_test.go`. Use the suite's base URL variable and HTTP helpers (check `e2e_suite_test.go` for their names; below they are `apiURL` and `http.Post`):

```go
package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Failover chains", Label("failover"), func() {
	postJSON := func(path string, body map[string]any) *http.Response {
		b, _ := json.Marshal(body)
		resp, err := http.Post(apiURL+path, "application/json", bytes.NewReader(b))
		Expect(err).ToNot(HaveOccurred())
		return resp
	}
	expectServedByMock := func(resp *http.Response) {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		Expect(resp.StatusCode).To(BeNumerically("<", 300), string(body))
		Expect(resp.Header.Get("X-LocalAI-Served-Model")).ToNot(HavePrefix("fail-"))
		Expect(resp.Header.Get("X-LocalAI-Failover")).To(Equal("fallback"))
	}

	DescribeTable("retries every endpoint family on the next target",
		func(path string, body func(model string) map[string]any) {
			expectServedByMock(postJSON(path, body("chain-"+CurrentSpecReport().LeafNodeText)))
		},
		Entry("chat", "/v1/chat/completions", func(m string) map[string]any {
			return map[string]any{"model": m, "messages": []map[string]string{{"role": "user", "content": "hi"}}}
		}),
		Entry("completion", "/v1/completions", func(m string) map[string]any {
			return map[string]any{"model": m, "prompt": "hi"}
		}),
		Entry("embeddings", "/v1/embeddings", func(m string) map[string]any {
			return map[string]any{"model": m, "input": "hi"}
		}),
		Entry("tts", "/v1/audio/speech", func(m string) map[string]any {
			return map[string]any{"model": m, "input": "hi"}
		}),
		Entry("image", "/v1/images/generations", func(m string) map[string]any {
			return map[string]any{"model": m, "prompt": "a cat", "size": "256x256"}
		}),
		Entry("rerank", "/v1/rerank", func(m string) map[string]any {
			return map[string]any{"model": m, "query": "q", "documents": []string{"a", "b"}}
		}),
		Entry("vad", "/v1/vad", func(m string) map[string]any {
			return map[string]any{"model": m, "audio": []float32{0, 0, 0, 0}}
		}),
	)

	It("retries transcription with the multipart body", func() {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("model", "chain-transcription")
		fw, _ := mw.CreateFormFile("file", "a.wav")
		_, _ = fw.Write(testWAV())
		Expect(mw.Close()).To(Succeed())
		resp, err := http.Post(apiURL+"/v1/audio/transcriptions", mw.FormDataContentType(), &body)
		Expect(err).ToNot(HaveOccurred())
		expectServedByMock(resp)
	})

	Describe("remote targets", Ordered, func() {
		var up1, up2 *fakeOpenAIUpstream

		BeforeAll(func() {
			up1, up2 = newFakeOpenAIUpstream(), newFakeOpenAIUpstream()
			up1.SetModels("up-1")
			up2.SetModels("up-2")
			// Register up-1 and up-2 as cloud-proxy passthrough models pointing at
			// up1.URL()+"/v1/chat/completions" and up2.URL()+"/v1/chat/completions",
			// and chain-remote with probe interval 1s, recovery probes 2 and
			// min_dwell 2s, using the same mechanism as e2e_cloud_proxy_test.go.
			registerFailoverRemoteModels(up1.URL(), up2.URL())
		})

		It("fails over when the primary upstream errors and fails back when it recovers", func() {
			up1.SetScript(func([]byte) (int, string, string) { return 503, `{"error":"no healthy nodes"}`, "application/json" })
			up2.SetScript(func([]byte) (int, string, string) {
				return 200, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`, "application/json"
			})
			resp := postJSON("/v1/chat/completions", map[string]any{"model": "chain-remote", "messages": []map[string]string{{"role": "user", "content": "hi"}}})
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(200))
			Expect(resp.Header.Get("X-LocalAI-Served-Model")).To(Equal("up-2"))

			up1.SetScript(func([]byte) (int, string, string) {
				return 200, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`, "application/json"
			})
			Eventually(func() string {
				r, err := http.Get(apiURL + "/api/failover/chain-remote")
				if err != nil {
					return ""
				}
				defer r.Body.Close()
				var st struct {
					Active string `json:"active"`
				}
				_ = json.NewDecoder(r.Body).Decode(&st)
				return st.Active
			}, 30*time.Second, 500*time.Millisecond).Should(Equal("up-1"))
		})
	})
})
```

Add this helper to the same file (200 ms of 16 kHz mono 16-bit silence):

```go
func testWAV() []byte {
	const rate, samples = 16000, 3200
	data := samples * 2
	b := make([]byte, 44+data)
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+data))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], rate)
	binary.LittleEndian.PutUint32(b[28:], rate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(data))
	return b
}
```

(add `encoding/binary` to the imports; if the package already defines `testWAV`, use the existing one.)

Implement `registerFailoverRemoteModels(url1, url2 string)` in the same file using the cloud-proxy registration mechanism found in Step 3. `CurrentSpecReport().LeafNodeText` gives the entry name (`chat`, `completion`, ...), which matches the chain names from Step 3. For VAD and rerank, copy the request shape from existing e2e specs if they differ from the ones above. If the mock backend cannot serve a family at all (the request fails on the mock target too), remove that entry and list the family in the PR description as "covered by unit tests only".

- [ ] **Step 5: Run the e2e specs**

Run: `make build-mock-backend && go run github.com/onsi/ginkgo/v2/ginkgo --label-filter=failover -v ./tests/e2e 2>&1 | tail -30`
Expected: PASS. Then run the cloud-proxy specs to check the helper change: `go run github.com/onsi/ginkgo/v2/ginkgo --focus="cloud" -v ./tests/e2e 2>&1 | tail -10`.

- [ ] **Step 6: Commit**

```bash
git add tests/e2e
git commit -m "test(failover): cover retry per endpoint family and remote fail-back

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 11: Realtime pipelines

**Files:**
- Modify: `core/http/endpoints/openai/realtime_model.go` (`RealtimeRoutingContext`, `buildRealtimeRoutingContext` ~860-880, `wrappedModel` ~39-90, `newModel` ~882, stage methods)
- Create: `core/http/endpoints/openai/realtime_failover.go`, `core/http/endpoints/openai/realtime_failover_test.go`, `core/http/endpoints/openai/types/failover.go`
- Modify: `core/http/endpoints/openai/types/server_events.go` (event type constant)
- Modify: `core/http/endpoints/openai/realtime.go` (start events after `newModel` succeeds, ~643-660)
- Test: `tests/e2e/realtime_ws_test.go` (new spec)

**Interfaces:**
- Consumes: `failover.Manager.Do`, `ChainStatus`, `Subscribe`, `RecordAttemptTrace`.
- Produces: `types.ModelFailoverEvent`, `types.ServerEventTypeModelFailover = "localai.model.failover"`, `(*wrappedModel).stageCall`.

- [ ] **Step 1: Add the event type**

In `types/server_events.go`, next to `ServerEventTypeClassifierResult`:

```go
	ServerEventTypeModelFailover ServerEventType = "localai.model.failover"
```

`types/failover.go`, following the `ClassifierResultEvent` pattern in `types/classifier.go:426-469` (copy its `ServerEventBase` embedding and `MarshalJSON` shape exactly):

```go
package types

import "encoding/json"

// ModelFailoverEvent tells a client which target serves a pipeline stage
// that names a failover chain.
type ModelFailoverEvent struct {
	ServerEventBase
	Chain  string `json:"chain"`
	Stage  string `json:"stage"`
	From   string `json:"from"`
	To     string `json:"to"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

func (ModelFailoverEvent) ServerEventType() ServerEventType { return ServerEventTypeModelFailover }

func (e ModelFailoverEvent) MarshalJSON() ([]byte, error) {
	type alias ModelFailoverEvent
	return json.Marshal(struct {
		Type ServerEventType `json:"type"`
		alias
	}{Type: ServerEventTypeModelFailover, alias: alias(e)})
}
```

- [ ] **Step 2: Write the failing unit tests**

`core/http/endpoints/openai/realtime_failover_test.go` (package and suite of the other tests in that directory; `fakeTransport` is in `realtime_doubles_test.go:29`):

```go
package openai

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/services/failover"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type rtSource map[string]config.ModelConfig

func (s rtSource) GetModelConfig(n string) (config.ModelConfig, bool) { c, ok := s[n]; return c, ok }
func (s rtSource) GetAllModelsConfigs() []config.ModelConfig {
	var out []config.ModelConfig
	for _, c := range s {
		out = append(out, c)
	}
	return out
}

var _ = Describe("realtime failover", func() {
	var fm *failover.Manager

	BeforeEach(func() {
		fm = failover.New(rtSource{
			"a": {Name: "a", Backend: "cloud-proxy"},
			"b": {Name: "b", Backend: "llama-cpp"},
			"chain": {Name: "chain", Failover: &config.FailoverConfig{Targets: []config.FailoverTarget{{Model: "a"}, {Model: "b"}}}},
		})
	})

	It("routes a chain stage through the plan and retries before commit", func() {
		m := &wrappedModel{failover: fm, stageChains: map[string]string{"tts": "chain"},
			stageTargetConfig: func(name string) (*config.ModelConfig, error) { return &config.ModelConfig{Name: name}, nil }}
		var tried []string
		err := m.stageCall(context.Background(), "tts", nil, func(cfg *config.ModelConfig, _ func()) error {
			tried = append(tried, cfg.Name)
			if cfg.Name == "a" {
				return errors.New("dial tcp: refused")
			}
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(tried).To(Equal([]string{"a", "b"}))
	})

	It("calls a plain stage once with its own config", func() {
		m := &wrappedModel{}
		base := &config.ModelConfig{Name: "plain"}
		err := m.stageCall(context.Background(), "tts", base, func(cfg *config.ModelConfig, _ func()) error {
			Expect(cfg).To(BeIdenticalTo(base))
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
	})

	It("sends initial events, then switch events, and stops on cancel", func() {
		t := &fakeTransport{}
		failoverEvents := func() []types.ModelFailoverEvent {
			var out []types.ModelFailoverEvent
			for _, e := range t.events() {
				if fe, ok := e.(types.ModelFailoverEvent); ok {
					out = append(out, fe)
				}
			}
			return out
		}
		stop := startFailoverEvents(t, fm, map[string]string{"llm": "chain"})
		Eventually(failoverEvents).Should(ContainElement(And(
			HaveField("Reason", "initial"), HaveField("To", "a"), HaveField("Stage", "llm"))))
		fm.ReportFailure("a", errors.New("dial tcp: refused"))
		Eventually(failoverEvents).Should(ContainElement(And(
			HaveField("Reason", "trip"), HaveField("To", "b"))))
		stop()
	})
})
```

`t.events()` must return the events sent so far as `[]types.ServerEvent`. Read `realtime_doubles_test.go`: if `fakeTransport` has no such accessor, add a mutex-guarded `events()` method there.

- [ ] **Step 3: Run to verify failure**

Run: `go test ./core/http/endpoints/openai/... 2>&1 | tail -5`
Expected: compile failure (`unknown field failover`).

- [ ] **Step 4: Implement stage resolution**

In `realtime_model.go`:

1. `RealtimeRoutingContext`: add `Failover *failover.Manager`. In `buildRealtimeRoutingContext`, set `Failover: a.FailoverManager()`.
2. `wrappedModel`: add

```go
	// failover and stageChains route pipeline stages that name a failover
	// chain; stageChains maps a stage ("llm", "tts", ...) to its chain.
	failover          *failover.Manager
	stageChains       map[string]string
	stageTargetConfig func(name string) (*config.ModelConfig, error)
	appTracing        bool
```

3. In `newModel`, after each stage config is loaded with `LoadResolvedModelConfig` and before `Validate()`: when the loaded config `IsFailover()`, record the chain and swap in its active target's config, so everything that inspects stage configs at session start (voice, reasoning, templates) sees a real model:

```go
	resolveStage := func(stage string, cfg *config.ModelConfig) (*config.ModelConfig, error) {
		if cfg == nil || !cfg.IsFailover() {
			return cfg, nil
		}
		if routing == nil || routing.Failover == nil {
			return nil, fmt.Errorf("pipeline %s stage %q is a failover chain, but failover is not running", stage, cfg.Name)
		}
		st, ok := routing.Failover.ChainStatus(cfg.Name)
		if !ok {
			return nil, fmt.Errorf("failover chain %q not found", cfg.Name)
		}
		stageChains[stage] = cfg.Name
		return cl.LoadResolvedModelConfig(st.Active, ml.ModelPath, appConfig.ToConfigLoaderOptions()...)
	}
```

Call it for `vad`, `transcription`, `llm`, `tts` and `sound_detection` right after each load. Declare `stageChains := map[string]string{}` before the loads. When building `&wrappedModel{...}`, set:

```go
		stageChains: stageChains,
		stageTargetConfig: func(name string) (*config.ModelConfig, error) {
			return cl.LoadResolvedModelConfig(name, ml.ModelPath, appConfig.ToConfigLoaderOptions()...)
		},
		appTracing: appConfig.EnableTracing,
```

and, when `routing != nil`, `failover: routing.Failover`.

4. `realtime_failover.go`:

```go
package openai

import (
	"context"
	"sort"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/services/failover"
)

// stageCall runs fn with the config that should serve stage now. A plain
// stage uses base. A chain stage goes through the failover plan and retries
// on the next target until fn calls commit.
func (m *wrappedModel) stageCall(ctx context.Context, stage string, base *config.ModelConfig, fn func(cfg *config.ModelConfig, commit func()) error) error {
	chain, ok := m.stageChains[stage]
	if !ok || m.failover == nil {
		return fn(base, func() {})
	}
	return m.failover.Do(ctx, chain, func(ctx context.Context, target string, commit func()) error {
		cfg, err := m.stageTargetConfig(target)
		if err != nil {
			return err
		}
		err = fn(cfg, commit)
		if err != nil {
			failover.RecordAttemptTrace(m.appTracing, chain, target, err)
		}
		return err
	})
}

// startFailoverEvents tells the client which target serves each chain stage
// now, and again whenever a chain switches. The returned func stops it.
func startFailoverEvents(t Transport, fm *failover.Manager, stageChains map[string]string) func() {
	events, cancel := fm.Subscribe(16)
	stages := make([]string, 0, len(stageChains))
	for s := range stageChains {
		stages = append(stages, s)
	}
	sort.Strings(stages)
	for _, stage := range stages {
		chain := stageChains[stage]
		if st, ok := fm.ChainStatus(chain); ok {
			sendEvent(t, types.ModelFailoverEvent{Chain: chain, Stage: stage, To: st.Active, State: string(st.State), Reason: string(failover.ReasonInitial)})
		}
	}
	go func() {
		for ev := range events {
			if ev.Type != failover.EventChainSwitched {
				continue
			}
			for _, stage := range stages {
				if stageChains[stage] == ev.Chain {
					sendEvent(t, types.ModelFailoverEvent{Chain: ev.Chain, Stage: stage, From: ev.From, To: ev.To, State: ev.State, Reason: string(ev.Reason)})
				}
			}
		}
	}()
	return cancel
}
```

5. Route each stage method through `stageCall`. Non-streaming example (`TTS`):

```go
func (m *wrappedModel) TTS(ctx context.Context, text, voice, language string) (string, *proto.Result, error) {
	var (
		out string
		res *proto.Result
	)
	err := m.stageCall(ctx, "tts", m.TTSConfig, func(cfg *config.ModelConfig, _ func()) error {
		var err error
		out, res, err = backend.ModelTTS(ctx, text, voice, language, "", maps.Clone(m.ttsParams), m.modelLoader, m.appConfig, *cfg)
		return err
	})
	return out, res, err
}
```

Apply the same shape to `VAD` (stage `vad`, `m.VADConfig`), `Transcribe` (`transcription`, `m.TranscriptionConfig`) and `SoundDetection` (`sound_detection`, `m.SoundDetectionConfig`).

Streaming stages wrap the callback so the first delivered chunk commits:
- `TTSStream`: `onAudio` becomes `func(pcm []byte, sr int) error { commit(); return onAudio(pcm, sr) }`.
- `TranscribeStream`: `onDelta` becomes `func(s string) { commit(); onDelta(s) }`.
- `TranscribeLive`: call `commit()` right after the session opens successfully; the stage call only covers opening.

`Predict` returns a closure that runs inference later. Keep the early part (message and tool preparation that does not depend on the config) where it is, and move everything that reads `turnCfg` (templating, `routeTurn` result use, `backend.ModelInference`) into the returned closure:

```go
	return func() (backend.LLMResponse, error) {
		var resp backend.LLMResponse
		err := m.stageCall(ctx, "llm", turnCfg, func(cfg *config.ModelConfig, commit func()) error {
			cb := func(s string, u backend.TokenUsage) bool {
				commit()
				return tokenCallback(s, u)
			}
			// build predInput from cfg (not turnCfg) here, then:
			predict, err := backend.ModelInference(ctx, predInput, messages, images, videos, audios, m.modelLoader, *cfg, m.confLoader, m.appConfig, cb, toolsJSON, toolChoiceJSON, logprobs, topLogprobs, logitBias, nil)
			if err != nil {
				return err
			}
			resp, err = predict()
			return err
		})
		return resp, err
	}, nil
```

Handle a nil `tokenCallback` (call `commit()` only). Apply `applyPipelineReasoning`/`applyPipelineThinking` to `cfg` inside the closure the same way `newModel` applies them to `cfgLLM`. A chain stage used together with a router (`routeTurn`) is out of scope: when `routeTurn` swapped the config, call the backend directly as today.

6. In `realtime.go`, directly after the successful `newModel` for the main session (~643-660), start the events for the life of the session handler:

```go
	if wrapped, ok := m.(*wrappedModel); ok && wrapped.failover != nil && len(wrapped.stageChains) > 0 {
		stopFailoverEvents := startFailoverEvents(t, wrapped.failover, wrapped.stageChains)
		defer stopFailoverEvents()
	}
```

Check that the enclosing function runs for the whole session (the defer must fire when the session ends, not when setup returns). If it returns earlier, store the stop func on the session and call it where the session is torn down.

- [ ] **Step 5: Run unit tests**

Run: `go test -race ./core/http/endpoints/openai/... 2>&1 | tail -15`
Expected: PASS, including the existing realtime specs.

- [ ] **Step 6: Add the e2e realtime spec**

In `tests/e2e/realtime_ws_test.go`, add a spec (Label `failover`) next to the existing WebSocket session spec, reusing its connection and turn helpers:
- Config `rt-failover` whose pipeline uses the same VAD/transcription/TTS models as the existing spec and `llm: chain-rt`, where `chain-rt` targets `[fail-rt, <mock llm model>]` and `fail-rt` has `parameters.model: fail-load-rt` (write these in the suite as in Task 10 Step 3).
- Assert: after connect, a `localai.model.failover` event with `stage: llm`, `reason: initial`, `to: fail-rt`.
- Send one user turn: a `localai.model.failover` event with `to` = the mock LLM and `reason: trip` arrives, and the turn completes with `response.done`.
- Send a second turn: it completes, and the conversation still holds the first turn's items (the item ids from the first turn's `conversation.item.created` events are still retrievable, or the second request's messages include them — use whatever the existing spec can observe).

Run: `go run github.com/onsi/ginkgo/v2/ginkgo --label-filter=failover -v ./tests/e2e 2>&1 | tail -30`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add core/http/endpoints/openai tests/e2e
git commit -m "feat(failover): switch realtime pipeline stages per call

A stage that names a chain is resolved on every call, so a switch keeps
the session and its conversation. Clients get localai.model.failover
events at session start and on every switch.

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 12: MCP admin tools

**Files:**
- Modify: `pkg/mcp/localaitools/tools.go` (constants, `mutatingToolNames`), `client.go` (interface), `dto.go` (DTOs), `inproc/client.go`, `httpapi/client.go`, `httpapi/routes.go`, `server.go` (register), `prompts/20_tools.md`, `prompts/10_safety.md`
- Create: `pkg/mcp/localaitools/tools_failover.go`
- Modify tests: `coverage_test.go`, `server_test.go`, `fakes_test.go`, `inproc/client_test.go`, `httpapi/client_test.go`, `parity_test.go`
- Modify: where the in-process client is constructed (find with `grep -rn "inproc.Client{\|inproc.New" core pkg`) to pass the failover manager

**Interfaces:**
- Consumes: `failover.Manager.Status/Pin/Unpin`.
- Produces: tools `list_failover_chains` (read-only), `pin_failover_target`, `unpin_failover_target` (mutating).

- [ ] **Step 1: Write the failing tests**

- `coverage_test.go` `toolToHTTPRoute`:

```go
	ToolListFailoverChains:  "GET /api/failover",
	ToolPinFailoverTarget:   "POST /api/failover/:chain/pin",
	ToolUnpinFailoverTarget: "DELETE /api/failover/:chain/pin",
```

- `server_test.go`: add `ToolListFailoverChains` to `expectedReadOnlyCatalog`; add dispatch rows in the table at ~154:

```go
	{ToolListFailoverChains, map[string]any{}, "ListFailoverChains"},
	{ToolPinFailoverTarget, map[string]any{"chain": "c", "target": "b"}, "PinFailoverTarget"},
	{ToolUnpinFailoverTarget, map[string]any{"chain": "c"}, "UnpinFailoverTarget"},
```

- `fakes_test.go`: fake methods that record the call name like the existing alias fakes (lines ~157-170).
- `inproc/client_test.go` and `httpapi/client_test.go`: one spec each for list and pin, in the style of the alias specs. The httpapi spec asserts the request method and path; the inproc spec builds a `failover.Manager` over a small in-memory source with one chain.

Run: `go test ./pkg/mcp/localaitools/... 2>&1 | tail -5`
Expected: compile failure.

- [ ] **Step 2: Implement**

`tools.go`: add `ToolListFailoverChains = "list_failover_chains"` to the read-only block, `ToolPinFailoverTarget = "pin_failover_target"` and `ToolUnpinFailoverTarget = "unpin_failover_target"` to the mutating block, and both mutating names to `mutatingToolNames`.

`dto.go`:

```go
type FailoverTargetInfo struct {
	Model     string `json:"model"`
	Kind      string `json:"kind"`
	Warm      bool   `json:"warm"`
	State     string `json:"state"`
	LastError string `json:"last_error,omitempty"`
}

type FailoverChainInfo struct {
	Name    string               `json:"name"`
	State   string               `json:"state"`
	Active  string               `json:"active"`
	Pinned  string               `json:"pinned,omitempty"`
	Targets []FailoverTargetInfo `json:"targets"`
}
```

`client.go` interface, next to the alias methods:

```go
	ListFailoverChains(ctx context.Context) ([]FailoverChainInfo, error)
	PinFailoverTarget(ctx context.Context, chain, target string) error
	UnpinFailoverTarget(ctx context.Context, chain string) error
```

`inproc/client.go`: add field `Failover *failover.Manager` to the client struct, set it where the client is constructed (from `application.FailoverManager()`), and:

```go
func (c *Client) ListFailoverChains(_ context.Context) ([]localaitools.FailoverChainInfo, error) {
	out := []localaitools.FailoverChainInfo{}
	if c.Failover == nil {
		return out, nil
	}
	for _, ch := range c.Failover.Status() {
		info := localaitools.FailoverChainInfo{Name: ch.Name, State: string(ch.State), Active: ch.Active}
		if ch.Pinned != nil {
			info.Pinned = *ch.Pinned
		}
		for _, t := range ch.Targets {
			info.Targets = append(info.Targets, localaitools.FailoverTargetInfo{
				Model: t.Model, Kind: string(t.Kind), Warm: t.Warm, State: string(t.State), LastError: t.LastError,
			})
		}
		out = append(out, info)
	}
	return out, nil
}

func (c *Client) PinFailoverTarget(_ context.Context, chain, target string) error {
	if c.Failover == nil {
		return errors.New("failover is not running")
	}
	return c.Failover.Pin(chain, target)
}

func (c *Client) UnpinFailoverTarget(_ context.Context, chain string) error {
	if c.Failover == nil {
		return errors.New("failover is not running")
	}
	return c.Failover.Unpin(chain)
}
```

`httpapi/routes.go`: `routeFailover = "/api/failover"`. `httpapi/client.go`:

```go
func (c *Client) ListFailoverChains(ctx context.Context) ([]localaitools.FailoverChainInfo, error) {
	var out struct {
		Chains []localaitools.FailoverChainInfo `json:"chains"`
	}
	if err := c.do(ctx, http.MethodGet, routeFailover, nil, &out); err != nil {
		return nil, err
	}
	return out.Chains, nil
}

func (c *Client) PinFailoverTarget(ctx context.Context, chain, target string) error {
	return c.do(ctx, http.MethodPost, routeFailover+"/"+url.PathEscape(chain)+"/pin", map[string]string{"target": target}, nil)
}

func (c *Client) UnpinFailoverTarget(ctx context.Context, chain string) error {
	return c.do(ctx, http.MethodDelete, routeFailover+"/"+url.PathEscape(chain)+"/pin", nil, nil)
}
```

The REST list returns `pinned` as a JSON string or null and `failover.TargetStatus` fields; `FailoverChainInfo` decodes the fields it shares. Check that `c.do` accepts a body map and a nil out; match its real signature.

`tools_failover.go`, following `tools_aliases.go`:

```go
package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerFailoverTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListFailoverChains,
		Description: "List model failover chains, the target serving each one now, and the health of every target.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		chains, err := client.ListFailoverChains(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(chains)
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolPinFailoverTarget,
		Description: "Force a failover chain to serve every request from one target, regardless of health, until it is unpinned.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Chain  string `json:"chain" jsonschema:"failover chain name"`
		Target string `json:"target" jsonschema:"target model to pin"`
	}) (*mcp.CallToolResult, any, error) {
		if err := client.PinFailoverTarget(ctx, args.Chain, args.Target); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]string{"chain": args.Chain, "pinned": args.Target})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolUnpinFailoverTarget,
		Description: "Remove the pin from a failover chain so health decides the target again.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Chain string `json:"chain" jsonschema:"failover chain name"`
	}) (*mcp.CallToolResult, any, error) {
		if err := client.UnpinFailoverTarget(ctx, args.Chain); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]string{"chain": args.Chain, "pinned": ""})
	})
}
```

Match the import path of the MCP SDK and the exact signatures of `errorResult`/`jsonResult` used in `tools_aliases.go`.

`server.go`: add `registerFailoverTools(s, client, opts)` to the register list (~45-56).

`prompts/20_tools.md`: under `## Read-only` add ``- `list_failover_chains` — List failover chains, their active target and target health.``; under `## Mutating` add ``- `pin_failover_target` — Force a failover chain to one target.`` and ``- `unpin_failover_target` — Remove a failover pin.``. `prompts/10_safety.md` line 5: add both mutating names to the backticked list.

- [ ] **Step 3: Run tests**

Run: `go test ./pkg/mcp/localaitools/... 2>&1 | tail -10`
Expected: PASS (including `TestToolHTTPRouteMappingComplete`, the prompts test and parity).

- [ ] **Step 4: Commit**

```bash
git add pkg/mcp core
git commit -m "feat(failover): add MCP tools to list chains and pin targets

Assisted-by: Claude:claude-opus-5-5"
```

---

### Task 13: Documentation and final verification

**Files:**
- Create: `docs/content/features/model-failover.md`
- Modify: `docs/content/features/model-aliases.md`, `docs/content/features/openai-realtime.md`, and the cloud-proxy section (find it with `grep -rln "cloud-proxy" docs/content`; add the link on the page that documents the `proxy:` block)

- [ ] **Step 1: Write the docs page**

`docs/content/features/model-failover.md`:

````markdown
+++
disableToc = false
title = "Model Failover"
weight = 15
url = "/features/model-failover/"
+++

A **failover chain** is a model name that is served by an ordered list of
other models. LocalAI sends each request to the first healthy target. When a
target fails, the request moves to the next target, and later requests stay
there until the first target has recovered.

Use it to serve a model from a remote LocalAI or another OpenAI-compatible
provider, and to fall back to a local model when the remote one is down.

## Declaring a chain

```yaml
name: assistant-llm
failover:
  targets:
    - model: argus-llm          # for example a cloud-proxy model
    - model: gemma-local
      warm: true                # keep it loaded
```

Clients call `assistant-llm`. Each target is a normal model config. A chain
has no `backend` and no `parameters.model`.

Optional settings, with their defaults:

```yaml
failover:
  probe:
    interval: 15s     # how often an idle target is checked
    timeout: 5s
  trip:
    errors: 1         # failures within the window that mark a target down
    window: 30s
  recovery:
    probes: 3         # test requests a target must pass before it is used again
    min_dwell: 60s    # minimum time on a lower target before moving back
```

Rules:

- A chain needs at least 2 targets. A target can be an alias, but not another
  chain.
- A chain cannot also set `alias` or `backend`.
- Responses name the chain as the model. The `X-LocalAI-Served-Model` header
  names the target that served the request.

## How the target is chosen

- The active target is the first healthy target in the list.
- When a target fails, LocalAI marks it down and moves to the next target at
  once.
- LocalAI moves back to a higher target only when that target has passed
  `recovery.probes` test requests **and** the current target has been active
  for at least `recovery.min_dwell`. This stops an unstable upstream from
  moving traffic back and forth.
- When all targets are down, the chain is `degraded`. Each request still tries
  every target in order.

## Retry inside a request

When a target fails before the response starts, LocalAI sends the same
request to the next target. The client does not see the failure.

- LocalAI does not retry after the first byte of a response is sent (for
  example after the first streamed token). The request fails, the target is
  marked down, and the next request uses the next target.
- LocalAI does not retry client errors (4xx), such as a prompt that is too
  long, because the next target would reject it too.
- Request bodies larger than 32 MiB are not retried.

When the primary did not serve the request, the response has the header
`X-LocalAI-Failover: fallback`, or `X-LocalAI-Failover: degraded` when all
targets were down.

## Health checks

| Target | Regular check | Check before moving back |
|---|---|---|
| Remote (`cloud-proxy`) | `GET /v1/models` on the upstream lists the model | one small real request, for example a 1-token completion |
| Local, `warm: true` | the backend answers a health check | one small real request |
| Local, not warm | the model file exists | none: the target is used again after `min_dwell` |

A request that succeeds counts as a check, so a busy target is almost never
probed. A model that is not warm is never loaded only to check it.

When a target is in more than one chain, its check settings come from the
first of those chains in name order.

## Warm targets

`warm: true` loads a local target at startup and protects it from idle and
LRU eviction, so a switch does not wait for the model to load. Warm targets
count toward the active backend limit. If warm targets fill that limit, other
models cannot load, and the error names the warm targets.

## Realtime pipelines

A pipeline stage can name a chain:

```yaml
name: assistant
pipeline:
  vad: silero-vad
  transcription: whisper-chain
  llm: assistant-llm
  tts: voice-chain
```

LocalAI resolves the chain for every call of the stage. When a chain switches,
the session stays open and keeps its conversation. The next turn uses the new
target.

The session receives a `localai.model.failover` event for each chain stage when
it starts (`reason: initial`) and each time a chain switches:

```json
{"type":"localai.model.failover","chain":"assistant-llm","stage":"llm",
 "from":"argus-llm","to":"gemma-local","state":"fallback","reason":"trip"}
```

A chain used as a candidate of a router in a realtime pipeline is not
resolved per call.

## Watching failover

- `GET /api/failover` lists every chain, its active target and the state of
  each target.
- `GET /api/failover/{chain}` returns one chain.
- `GET /api/failover/events` is a server-sent event stream. The first event is
  `snapshot` with the full state. Then `chain.switched` and `target.state`
  events follow.
- Metrics: `localai_failover_switches_total{chain,from,to,reason}` and
  `localai_failover_target_up{target}`.
- With tracing on, each skipped target appears in the Traces view with the
  error that made LocalAI skip it.

## Pinning a target

An admin can force a chain to one target, for example during maintenance:

```bash
curl -X POST http://localhost:8080/api/failover/assistant-llm/pin \
  -H 'Content-Type: application/json' -d '{"target":"gemma-local"}'
curl -X DELETE http://localhost:8080/api/failover/assistant-llm/pin
```

While a chain is pinned, only the pinned target serves it. Health checks
continue. A restart removes the pin.

## Assistant and MCP

The LocalAI Assistant and `local-ai mcp-server` offer `list_failover_chains`,
`pin_failover_target` and `unpin_failover_target`. Create and edit chains with
the model config tools, like any other model.

## Limits

- Failover state is kept in memory by each LocalAI instance. Several frontends
  in distributed mode each keep their own view.
- Chains do not nest.
- See also [model aliases]({{%relref "features/model-aliases" %}}) and the
  [realtime API]({{%relref "features/openai-realtime" %}}).
````

- [ ] **Step 2: Cross-links**

- `model-aliases.md`: add at the end of `## Rules and behavior`: `To serve a name from several models with automatic fallback, use a [failover chain]({{%relref "features/model-failover" %}}).`
- `openai-realtime.md`: in the pipeline section add: `A pipeline stage can name a [failover chain]({{%relref "features/model-failover" %}}); the stage then switches targets without closing the session.`
- The page that documents `proxy:` / `cloud-proxy`: add `To fall back to a local model when the upstream is down, list the proxy model in a [failover chain]({{%relref "features/model-failover" %}}).`

- [ ] **Step 3: Final verification**

Run, in order, and read each output:

```bash
make protogen-go build-mock-backend
go vet ./core/... ./pkg/mcp/...
go test -race ./core/services/failover/... ./core/config/... ./core/http/... ./pkg/mcp/localaitools/...
go run github.com/onsi/ginkgo/v2/ginkgo --label-filter=failover -v ./tests/e2e
make swagger && git diff --stat -- swagger
make test-coverage-check
```

Expected: vet clean, all tests PASS, swagger has no uncommitted changes, coverage at or above the baseline. If coverage dropped, add tests; never edit `coverage-baseline.txt`.

- [ ] **Step 4: Commit**

```bash
git add docs/content
git commit -m "docs: document model failover chains

Assisted-by: Claude:claude-opus-5-5"
```

---

## PR notes (for the finishing step)

The PR description must include:
- The MCP decision: tools added for list, pin and unpin; chain create/edit reuses the model config tools.
- Endpoint families without in-request retry, if Task 10 found any.
- The limits from the docs page (per-instance state, router candidates in realtime).
- A note that the human submitter adds `Signed-off-by` (DCO).
