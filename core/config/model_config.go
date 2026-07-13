package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/piipattern"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/reasoning"
	"github.com/mudler/cogito"
	"gopkg.in/yaml.v3"
)

const (
	RAND_SEED = -1
)

// @Description TTS configuration
type TTSConfig struct {
	// Voice wav path or id
	Voice string `yaml:"voice,omitempty" json:"voice,omitempty"`

	AudioPath string `yaml:"audio_path,omitempty" json:"audio_path,omitempty"`

	// VoiceCloning overrides saved-profile capability detection for this model.
	// A pointer preserves the distinction between an explicit false and the
	// default automatic behavior.
	VoiceCloning *bool `yaml:"voice_cloning,omitempty" json:"voice_cloning,omitempty"`
}

// @Description ModelConfig represents a model configuration
type ModelConfig struct {
	modelConfigFile          string `yaml:"-" json:"-"`
	modelTemplate            string `yaml:"-" json:"-"`
	schema.PredictionOptions `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	Name                     string                `yaml:"name,omitempty" json:"name,omitempty"`
	Artifacts                []modelartifacts.Spec `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`

	// Alias, when set, makes this config a pure redirect: every request for
	// Name is served by the model named here. All other fields are ignored.
	// The target must be an existing, non-alias model (enforced at load and
	// at create/swap time). See docs/content for Model Aliases.
	Alias string `yaml:"alias,omitempty" json:"alias,omitempty"`

	F16                 *bool               `yaml:"f16,omitempty" json:"f16,omitempty"`
	Threads             *int                `yaml:"threads,omitempty" json:"threads,omitempty"`
	Debug               *bool               `yaml:"debug,omitempty" json:"debug,omitempty"`
	Roles               map[string]string   `yaml:"roles,omitempty" json:"roles,omitempty"`
	Embeddings          *bool               `yaml:"embeddings,omitempty" json:"embeddings,omitempty"`
	Backend             string              `yaml:"backend,omitempty" json:"backend,omitempty"`
	TemplateConfig      TemplateConfig      `yaml:"template,omitempty" json:"template,omitempty"`
	KnownUsecaseStrings []string            `yaml:"known_usecases,omitempty" json:"known_usecases,omitempty"`
	KnownUsecases       *ModelConfigUsecase `yaml:"-" json:"-"`
	// KnownInputModalities and KnownOutputModalities describe model-specific I/O
	// that usecases alone cannot express, such as image- or audio-conditioned video.
	KnownInputModalities  []string `yaml:"known_input_modalities,omitempty" json:"known_input_modalities,omitempty"`
	KnownOutputModalities []string `yaml:"known_output_modalities,omitempty" json:"known_output_modalities,omitempty"`
	Pipeline              Pipeline `yaml:"pipeline,omitempty" json:"pipeline,omitempty"`

	PromptStrings, InputStrings                []string       `yaml:"-" json:"-"`
	InputToken                                 [][]int        `yaml:"-" json:"-"`
	functionCallString, functionCallNameString string         `yaml:"-" json:"-"`
	ResponseFormat                             string         `yaml:"-" json:"-"`
	ResponseFormatMap                          map[string]any `yaml:"-" json:"-"`

	// MediaMarker is the runtime-discovered multimodal marker the backend expects
	// in the prompt (e.g. "<__media__>" or a random "<__media_<rand>__>" picked by
	// llama.cpp). Populated on first successful ModelMetadata call. Empty until
	// then — callers must fall back to templates.DefaultMultiMediaMarker.
	MediaMarker string `yaml:"-" json:"-"`

	FunctionsConfig functions.FunctionsConfig `yaml:"function,omitempty" json:"function,omitempty"`
	ReasoningConfig reasoning.Config          `yaml:"reasoning,omitempty" json:"reasoning,omitempty"`

	// ReasoningEffort is the default reasoning effort (none|minimal|low|medium|high)
	// for this model. A per-request reasoning_effort overrides it. It is forwarded
	// to the backend as the reasoning_effort chat_template_kwarg (see
	// gRPCPredictOpts), so jinja-templated models that key on it — e.g. gpt-oss
	// (Harmony) or LFM2.5 — honor it; "none" also toggles enable_thinking off.
	ReasoningEffort string `yaml:"reasoning_effort,omitempty" json:"reasoning_effort,omitempty"`

	// ChatTemplateKwargs are arbitrary key/values forwarded to the backend's jinja
	// chat template via chat_template_kwargs (e.g. preserve_thinking: true). The
	// server-derived reasoning levers (enable_thinking / reasoning_effort) and any
	// per-request metadata overrides layer on top. See gRPCPredictOpts.
	ChatTemplateKwargs map[string]any `yaml:"chat_template_kwargs,omitempty" json:"chat_template_kwargs,omitempty"`

	// RequestMetadata holds the raw client request `metadata` map for the current
	// request. The request middleware stamps it; gRPCPredictOpts merges it into the
	// backend gRPC metadata (overriding the server-derived enable_thinking /
	// reasoning_effort) and folds it, coerced, into the chat_template_kwargs blob.
	// Never persisted to YAML.
	RequestMetadata map[string]string `yaml:"-" json:"-"`

	FeatureFlag FeatureFlag `yaml:"feature_flags,omitempty" json:"feature_flags,omitempty"` // Feature Flag registry. We move fast, and features may break on a per model/backend basis. Registry for (usually temporary) flags that indicate aborting something early.
	// LLM configs (GPT4ALL, Llama.cpp, ...)
	LLMConfig `yaml:",inline" json:",inline"`

	// Diffusers
	Diffusers Diffusers `yaml:"diffusers,omitempty" json:"diffusers,omitempty"`
	Step      int       `yaml:"step,omitempty" json:"step,omitempty"`

	// GRPC Options
	GRPC GRPC `yaml:"grpc,omitempty" json:"grpc,omitempty"`

	// TTS specifics
	TTSConfig `yaml:"tts,omitempty" json:"tts,omitempty"`

	// CUDA
	// Explicitly enable CUDA or not (some backends might need it)
	CUDA bool `yaml:"cuda,omitempty" json:"cuda,omitempty"`

	DownloadFiles []File `yaml:"download_files,omitempty" json:"download_files,omitempty"`

	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Usage       string `yaml:"usage,omitempty" json:"usage,omitempty"`
	Disabled    *bool  `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	Pinned      *bool  `yaml:"pinned,omitempty" json:"pinned,omitempty"`

	// ConcurrencyGroups declares per-node mutual-exclusion groups: the model
	// cannot be loaded alongside another model that shares any group name.
	// See docs/content/advanced/vram-management.md for usage.
	ConcurrencyGroups []string `yaml:"concurrency_groups,omitempty" json:"concurrency_groups,omitempty"`

	Options   []string `yaml:"options,omitempty" json:"options,omitempty"`
	Overrides []string `yaml:"overrides,omitempty" json:"overrides,omitempty"`

	MCP   MCPConfig   `yaml:"mcp,omitempty" json:"mcp,omitempty"`
	Agent AgentConfig `yaml:"agent,omitempty" json:"agent,omitempty"`
	PII   PIIConfig   `yaml:"pii,omitempty" json:"pii,omitempty"`
	// PIIDetection is the detection policy when THIS model is used as a
	// PII detector (a token_classify model named in another model's
	// pii.detectors). Ignored on models that aren't referenced as
	// detectors.
	PIIDetection PIIDetectionConfig `yaml:"pii_detection,omitempty" json:"pii_detection,omitempty"`
	Router       RouterConfig       `yaml:"router,omitempty" json:"router,omitempty"`
	Proxy        ProxyConfig        `yaml:"proxy,omitempty" json:"proxy,omitempty"`
	MITM         MITMModelConfig    `yaml:"mitm,omitempty" json:"mitm,omitempty"`
	Limits       LimitsConfig       `yaml:"limits,omitempty" json:"limits,omitempty"`
}

// @Description Admission-control limits applied per request. The
// admission middleware enforces these before invoking the handler;
// requests that exceed a limit get 503 with a Retry-After hint so
// clients back off rather than pile on. Per-model so cloud passthroughs
// can have a stricter ceiling than local models.
type LimitsConfig struct {
	// MaxConcurrent caps simultaneous in-flight requests for this
	// model. 0 = unlimited (default). Useful for cloud-passthrough
	// configs where the upstream rate-limits aggressively, or for
	// local backends whose memory budget tops out before LocalAI's
	// queue depth would.
	MaxConcurrent int `yaml:"max_concurrent,omitempty" json:"max_concurrent,omitempty"`

	// RetryAfterSeconds advises clients how long to wait before
	// retrying when admission rejects. 0 defaults to 1s — enough to
	// let an in-flight request finish on a busy local model. The
	// value is sent verbatim in the Retry-After response header.
	RetryAfterSeconds int `yaml:"retry_after_seconds,omitempty" json:"retry_after_seconds,omitempty"`
}

// @Description MITM intercept binding for the model. When the cloudproxy
// MITM listener is enabled and any host listed here appears in a CONNECT,
// the proxy uses THIS model config's pii: settings to filter the
// intercepted body. Strict 1-to-1: a host claimed by two configs is a
// configuration error and disables the MITM listener until resolved.
//
// Lets an admin pair a host (api.anthropic.com) with the model's
// PII overrides without maintaining a parallel per-host map.
type MITMModelConfig struct {
	// Hosts is the list of hostnames this model claims for MITM
	// interception. Each entry must be unique across all model configs.
	Hosts []string `yaml:"hosts,omitempty" json:"hosts,omitempty"`
}

// @Description Cloud proxy configuration. The cloud-proxy backend
// forwards a model's traffic to an external provider. Two modes:
//
//   - mode: passthrough — client and upstream must speak the same wire
//     format; the backend ships the raw request body to the upstream
//     URL and streams the response back untouched. The streaming PII
//     filter still runs because it operates on extracted token text.
//
//   - mode: translate — the backend converts LocalAI's internal proto
//     to the provider's wire format and back. Unlocks cross-provider
//     routing (OpenAI client → Anthropic upstream, etc.) at the cost
//     of dropping provider-specific extensions that the internal proto
//     doesn't model.
type ProxyConfig struct {
	// UpstreamURL is the full POST endpoint, e.g.
	// https://api.openai.com/v1/chat/completions or
	// https://api.anthropic.com/v1/messages. Required.
	UpstreamURL string `yaml:"upstream_url,omitempty" json:"upstream_url,omitempty"`

	// Mode selects passthrough (wire-perfect) or translate (full
	// control via internal proto). Empty defaults to passthrough.
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`

	// Provider identifies the upstream's wire format for translate
	// mode (openai, anthropic). Ignored in passthrough mode — the
	// wire format there is whatever the client sent.
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"`

	// APIKeyEnv names the environment variable holding the upstream
	// API key. Mutually exclusive with APIKeyFile. Both empty is
	// allowed (no-auth upstreams).
	APIKeyEnv string `yaml:"api_key_env,omitempty" json:"api_key_env,omitempty"`

	// APIKeyFile is a path to a file whose contents are the upstream
	// API key. Trailing whitespace is trimmed. Mutually exclusive
	// with APIKeyEnv. The integration point for K8s secret mounts,
	// Vault agent files, and similar external-secret workflows.
	APIKeyFile string `yaml:"api_key_file,omitempty" json:"api_key_file,omitempty"`

	// UpstreamModel overrides the model name sent to the upstream.
	// Useful when the LocalAI-facing model alias differs from the
	// upstream's canonical name (e.g. local "claude-strict" maps to
	// upstream "claude-3-5-sonnet-20241022"). Empty means forward
	// the client's model field unchanged.
	UpstreamModel string `yaml:"upstream_model,omitempty" json:"upstream_model,omitempty"`

	// RequestTimeoutSeconds caps the upstream request duration. 0
	// means no per-request timeout (only the request context, which
	// is bound to the client connection, applies).
	RequestTimeoutSeconds int `yaml:"request_timeout_seconds,omitempty" json:"request_timeout_seconds,omitempty"`
}

// Proxy mode names. Validate() normalises an empty Mode to
// ProxyModePassthrough so downstream code only sees concrete values.
const (
	ProxyModePassthrough = "passthrough"
	ProxyModeTranslate   = "translate"
)

// Proxy provider names. Only meaningful in translate mode, where the
// cloud-proxy backend picks the wire format to use against the
// upstream URL.
const (
	ProxyProviderOpenAI    = "openai"
	ProxyProviderAnthropic = "anthropic"
)

// IsCloudProxyBackendPassthrough reports whether this model uses the
// cloud-proxy gRPC backend in passthrough mode. Empty Mode counts as
// passthrough (SetDefaults normalises it, but Validate accepts empty
// too — handlers should not rely on a particular call order).
func (c *ModelConfig) IsCloudProxyBackendPassthrough() bool {
	if c.Backend != "cloud-proxy" {
		return false
	}
	return c.Proxy.Mode == "" || c.Proxy.Mode == ProxyModePassthrough
}

// @Description Intelligent routing configuration. When a model declares
// a Router block, requests addressed to it are reclassified at runtime
// and dispatched to one of the named candidates. The router rewrites
// input.Model in-place, then the standard model-resolution path picks
// up the resolved config — meaning ACL checks, disabled-state, and
// per-model PII still run against the chosen target.
//
// Depth-1 invariant: candidates must NOT themselves carry a Router
// block. The router's "smart-router → claude-strict → cloud-proxy"
// chain is fine, but "router-A → router-B → claude" is rejected at
// config load to keep the dispatch graph acyclic and predictable. The
// middleware also asserts depth ≤ 1 at runtime as a defensive check.
type RouterConfig struct {
	// Classifier picks the implementation. Only "score" ships today:
	// it asks the classifier model to score every Policy label as a
	// continuation of the routing prompt and reads off the
	// distribution. Empty defaults to "score".
	Classifier string `yaml:"classifier,omitempty" json:"classifier,omitempty"`

	// Policies is the label vocabulary the classifier scores over.
	// Each policy carries a natural-language description that ends up
	// in the system prompt the classifier model sees — short, action-
	// oriented sentences work best ("writing or debugging code",
	// "small talk", ...). The Score classifier picks the subset of
	// labels whose softmax probability passes ActivationThreshold.
	Policies []RouterPolicy `yaml:"policies,omitempty" json:"policies,omitempty"`

	// Candidates is the routing table — each entry binds a downstream
	// model to a set of labels it can serve. The middleware picks the
	// FIRST candidate whose Labels are a superset of the active label
	// set from the classifier. Admins order this list smallest →
	// largest so a query that needs one label routes to the smallest
	// capable model, while a query that needs multiple falls to a
	// bigger candidate that covers them all.
	Candidates []RouterCandidate `yaml:"candidates,omitempty" json:"candidates,omitempty"`

	// Fallback is the model used when no candidate matches the active
	// label set, or when the classifier returns nothing above
	// threshold. Empty fallback means router failures bubble up as
	// 500 — fail-fast, not silent-bypass.
	Fallback string `yaml:"fallback,omitempty" json:"fallback,omitempty"`

	// ClassifierModel names the model the Score classifier scores
	// against (Arch-Router-1.5B is the canonical choice).
	ClassifierModel string `yaml:"classifier_model,omitempty" json:"classifier_model,omitempty"`

	// ClassifierCacheSize bounds the per-prompt memo cache that
	// amortises the classifier round-trip across repeat probes.
	// 0 disables the cache. Default 1024.
	ClassifierCacheSize int `yaml:"classifier_cache_size,omitempty" json:"classifier_cache_size,omitempty"`

	// ActivationThreshold is the softmax-probability floor a policy
	// must clear to be considered "active" for the request. 0
	// defaults to a sensible value (~0.15) inside the classifier.
	// Higher → narrower routes (single-label dominant); lower →
	// more multi-label activations.
	ActivationThreshold float64 `yaml:"activation_threshold,omitempty" json:"activation_threshold,omitempty"`

	// ClassifierSystemTemplate overrides the routing system prompt
	// the score classifier feeds to its classifier_model. Go
	// text/template + Sprig, executed with `.Policies []ScorePolicy`
	// (Label + Description fields). Empty falls back to the built-in
	// Arch-Router-shaped template (route-listing block + JSON output
	// schema). Override when the classifier model was trained on a
	// different schema (e.g. bare label output, XML route block) or
	// when the routing instructions need to be in a different
	// language. The candidate format scored against the model is
	// fixed at `{"route": "<label>"}` and IS NOT templated — keep
	// your override's output schema instruction matching that, or
	// the per-candidate scores degenerate.
	ClassifierSystemTemplate string `yaml:"classifier_system_template,omitempty" json:"classifier_system_template,omitempty"`

	// ScoreNormalization picks how the score classifier collapses
	// per-candidate joint log-probs into the softmax input.
	//   - ""/"raw": use joint log-prob as-is (default). Matches the
	//     distribution the classifier model was trained against — the
	//     route the model would actually emit if decoded freely.
	//   - "mean": divide by candidate token count. Fairer to long
	//     labels (their joint log-prob is mechanically smaller because
	//     it sums more negatives), but off-distribution for models
	//     trained to emit fixed-format outputs like Arch-Router's
	//     {"route": "name"}.
	// Future modes (e.g. "weighted_mean") will land here too.
	ScoreNormalization string `yaml:"score_normalization,omitempty" json:"score_normalization,omitempty"`

	// EmbeddingCache configures the L2 cache that maps prompt
	// embeddings to past decisions, so semantically-similar prompts
	// reuse a classification instead of re-running the classifier
	// model. Omit the block to disable. See router/embedding_cache.go.
	EmbeddingCache *EmbeddingCacheConfig `yaml:"embedding_cache,omitempty" json:"embedding_cache,omitempty"`
}

// EmbeddingCacheConfig configures the L2 embedding-similarity decision
// cache. Pairs naturally with a larger / slower classifier model: the
// classifier round-trip is amortised across paraphrases of the same
// intent. The cache uses the standard /v1/embeddings backend for
// vector generation and the local-store gRPC surface for KNN search.
type EmbeddingCacheConfig struct {
	// EmbeddingModel names the loaded LocalAI model used to embed
	// router prompts. Required when the cache is enabled. Any model
	// that supports the Embeddings gRPC primitive works;
	// nomic-embed-text-v1.5 is the recommended default.
	EmbeddingModel string `yaml:"embedding_model" json:"embedding_model"`

	// SimilarityThreshold is the cosine-similarity floor a cache
	// candidate must clear to be treated as a hit. 0 picks the
	// package default (0.80). Higher → fewer false hits, higher miss
	// rate; lower → more aggressive sharing across paraphrases.
	SimilarityThreshold float64 `yaml:"similarity_threshold,omitempty" json:"similarity_threshold,omitempty"`

	// ConfidenceThreshold is the minimum classifier top-label
	// probability for a decision to be inserted into the cache. 0
	// picks the package default (0.60). Uncertain decisions are not
	// cached so they can't poison future paraphrases.
	ConfidenceThreshold float64 `yaml:"confidence_threshold,omitempty" json:"confidence_threshold,omitempty"`

	// StoreName overrides the local-store collection name used for
	// this router's cache. Empty defaults to "router-cache-<router>"
	// where <router> is the parent model name. Useful when two
	// router models should share a cache (rare).
	StoreName string `yaml:"store_name,omitempty" json:"store_name,omitempty"`
}

// RouterPolicy is one entry in the label vocabulary. The label string
// is what the classifier model emits and what candidates reference in
// their Labels field; the description is the natural-language hint
// fed to the classifier so it can match user intent against the label
// space.
type RouterPolicy struct {
	Label       string `yaml:"label" json:"label"`
	Description string `yaml:"description" json:"description"`
}

// RouterCandidate names a downstream model and the policy labels it
// is willing to serve. Labels are matched as a set: the middleware
// picks the first candidate whose Labels is a superset of the
// classifier's active set.
type RouterCandidate struct {
	Model  string   `yaml:"model" json:"model"`
	Labels []string `yaml:"labels" json:"labels"`
}

// HasRouter returns true when the model declares a router config with
// at least one candidate. Used by the RouteModel middleware to decide
// whether to engage the classifier.
func (c *ModelConfig) HasRouter() bool {
	return len(c.Router.Candidates) > 0
}

// IsAlias reports whether this config is a pure redirect to another model.
// Value receiver so it is callable on non-addressable config values too.
func (c ModelConfig) IsAlias() bool { return c.Alias != "" }

// @Description PII filtering configuration. PII redaction is per-model so
// that local models don't pay the latency or behaviour change of regex
// scanning, while cloud-bound traffic (cloud-proxy backend) can default to
// on. Setting Enabled explicitly always wins over the backend default.
type PIIConfig struct {
	// Enabled toggles redaction for this model. When unset (zero value),
	// the resolved default depends on Backend: cloud-proxy defaults to
	// true, everything else to false. A pointer is used so the absence of
	// the YAML key is distinguishable from explicit false.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// Detectors lists the token-classification (NER) models whose
	// detections drive PII redaction for this model. The detection policy
	// (min score, per-entity actions, default action) lives on each named
	// detector model's own pii_detection block, not here — a consuming
	// model just opts in by listing detectors. Multiple detectors union
	// their hits; overlapping spans resolve to the strongest action.
	Detectors []string `yaml:"detectors,omitempty" json:"detectors,omitempty"`
}

// @Description Detection policy for a token-classification (NER) model
// used as a PII detector. Lives on the detector model's own config so the
// model is a self-describing policy unit: consuming models reference it by
// name (via pii.detectors) and inherit this policy with no per-consumer
// overrides.
type PIIDetectionConfig struct {
	// MinScore drops detections the model scores below this confidence
	// before they are acted on. 0 keeps every detection.
	MinScore float32 `yaml:"min_score,omitempty" json:"min_score,omitempty"`
	// DefaultAction (mask | block | allow) applies to detected entity
	// groups with no explicit EntityActions entry. Empty defaults to
	// "mask" — the safe-by-default policy for a PII filter.
	DefaultAction string `yaml:"default_action,omitempty" json:"default_action,omitempty"`
	// EntityActions maps an entity group the model emits (e.g. "EMAIL",
	// "PASSWORD") to an action, overriding DefaultAction for that group.
	// This is where an operator says which PII to block vs mask vs
	// allow-log.
	EntityActions map[string]string `yaml:"entity_actions,omitempty" json:"entity_actions,omitempty"`

	// Builtins names the built-in pattern groups this (pattern) detector
	// enables, e.g. "anthropic_api_key", "github_token". Pattern detectors
	// match high-entropy structured secrets the NER tier can't; see
	// core/services/routing/piipattern.
	Builtins []string `yaml:"builtins,omitempty" json:"builtins,omitempty"`
	// Patterns lists operator-defined secret patterns in the restricted-regex
	// subset (validated at load). Each match is reported under its Name as the
	// entity group, so EntityActions/DefaultAction apply by Name.
	Patterns []PIIPattern `yaml:"patterns,omitempty" json:"patterns,omitempty"`
}

// PIIPattern is one operator-defined pattern on a pattern detector model. Name
// is the entity group reported for matches (and the EntityActions key). Match
// is the restricted-regex source. Action optionally overrides DefaultAction for
// this pattern. MinLen drops matches shorter than N bytes (0 = no floor).
type PIIPattern struct {
	Name   string `yaml:"name" json:"name"`
	Match  string `yaml:"match" json:"match"`
	Action string `yaml:"action,omitempty" json:"action,omitempty"`
	MinLen int    `yaml:"min_len,omitempty" json:"min_len,omitempty"`
}

// PIIIsEnabled returns the resolved PII state for this model. Single
// source of truth for the gating decision so the middleware and the
// /api/middleware/status admin view agree.
func (c *ModelConfig) PIIIsEnabled() bool {
	if c.PII.Enabled != nil {
		return *c.PII.Enabled
	}
	return c.Backend == "cloud-proxy"
}

// PIIDetectors returns the names of the token-classification models that
// drive PII redaction for this (consuming) model. Read via the
// ModelPIIConfig interface in core/services/routing/pii/middleware.go.
func (c *ModelConfig) PIIDetectors() []string {
	if len(c.PII.Detectors) == 0 {
		return nil
	}
	out := make([]string, len(c.PII.Detectors))
	copy(out, c.PII.Detectors)
	return out
}

// piiCoverableUsecases lists the model usecases whose serving API has a
// request-side PII filter wired (a piiadapter + the pii middleware). It scopes
// the Middleware admin list (PIIFilterApplies). Grow it as adapters are added
// for new endpoints. cloud-proxy carries no usecase flag but is always covered
// (via the MITM / proxy chat path), so PIIFilterApplies handles it separately.
var piiCoverableUsecases = []ModelConfigUsecase{FLAG_CHAT, FLAG_COMPLETION, FLAG_EDIT, FLAG_EMBEDDINGS}

// PIIFilterApplies reports whether request-side PII filtering can apply to
// this model at all — i.e. it is reachable through a text-accepting endpoint
// that has a PII adapter wired. Used to scope the Middleware admin view so it
// lists only models PII could protect, not every config (VAD, STT,
// embedding-only, image, or the token_classify detector models themselves,
// which are the filters rather than consumers). Detector/score models return
// false naturally: HasUsecases short-circuits to false for any usecase a
// declared score/token_classify model did not itself declare.
func (c *ModelConfig) PIIFilterApplies() bool {
	if c.Backend == "cloud-proxy" {
		return true
	}
	return slices.ContainsFunc(piiCoverableUsecases, c.HasUsecases)
}

// PIIDetectionMinScore returns the confidence floor this model applies
// when used as a PII detector.
func (c *ModelConfig) PIIDetectionMinScore() float32 { return c.PIIDetection.MinScore }

// PIIDetectionDefaultAction returns the raw default-action string applied
// to detected entity groups without an explicit override. The pii package
// validates it and applies the "mask" fallback.
func (c *ModelConfig) PIIDetectionDefaultAction() string { return c.PIIDetection.DefaultAction }

// PIIDetectionEntityActions returns the per-entity-group action policy as
// a fresh map of raw action strings (validated by the pii package).
func (c *ModelConfig) PIIDetectionEntityActions() map[string]string {
	if len(c.PIIDetection.EntityActions) == 0 {
		return nil
	}
	out := make(map[string]string, len(c.PIIDetection.EntityActions))
	for k, v := range c.PIIDetection.EntityActions {
		out[k] = v
	}
	return out
}

// IsPatternDetector reports whether this detector model matches secrets with
// regex patterns (built-in and/or operator-defined) rather than a neural NER
// model. Such a model runs entirely in-process (no backend / GGUF / VRAM); the
// PII resolver builds an in-process pattern matcher for it instead of loading a
// gRPC token-classifier.
func (c *ModelConfig) IsPatternDetector() bool {
	return len(c.PIIDetection.Builtins) > 0 || len(c.PIIDetection.Patterns) > 0
}

// @Description MCP configuration
type MCPConfig struct {
	Servers string `yaml:"remote,omitempty" json:"remote,omitempty"`
	Stdio   string `yaml:"stdio,omitempty" json:"stdio,omitempty"`
}

// @Description Agent configuration
type AgentConfig struct {
	MaxAttempts           int  `yaml:"max_attempts,omitempty" json:"max_attempts,omitempty"`
	MaxIterations         int  `yaml:"max_iterations,omitempty" json:"max_iterations,omitempty"`
	EnableReasoning       bool `yaml:"enable_reasoning,omitempty" json:"enable_reasoning,omitempty"`
	EnablePlanning        bool `yaml:"enable_planning,omitempty" json:"enable_planning,omitempty"`
	EnableMCPPrompts      bool `yaml:"enable_mcp_prompts,omitempty" json:"enable_mcp_prompts,omitempty"`
	EnablePlanReEvaluator bool `yaml:"enable_plan_re_evaluator,omitempty" json:"enable_plan_re_evaluator,omitempty"`
	DisableSinkState      bool `yaml:"disable_sink_state,omitempty" json:"disable_sink_state,omitempty"`
	LoopDetection         int  `yaml:"loop_detection,omitempty" json:"loop_detection,omitempty"`
	MaxAdjustmentAttempts int  `yaml:"max_adjustment_attempts,omitempty" json:"max_adjustment_attempts,omitempty"`
	ForceReasoningTool    bool `yaml:"force_reasoning_tool,omitempty" json:"force_reasoning_tool,omitempty"`
}

// HasMCPServers returns true if any MCP servers (remote or stdio) are configured.
func (c MCPConfig) HasMCPServers() bool {
	return c.Servers != "" || c.Stdio != ""
}

func (c *MCPConfig) MCPConfigFromYAML() (MCPGenericConfig[MCPRemoteServers], MCPGenericConfig[MCPSTDIOServers], error) {
	var remote MCPGenericConfig[MCPRemoteServers]
	var stdio MCPGenericConfig[MCPSTDIOServers]

	if err := yaml.Unmarshal([]byte(c.Servers), &remote); err != nil {
		return remote, stdio, err
	}

	if err := yaml.Unmarshal([]byte(c.Stdio), &stdio); err != nil {
		return remote, stdio, err
	}
	return remote, stdio, nil
}

// @Description MCP generic configuration
type MCPGenericConfig[T any] struct {
	Servers T `yaml:"mcpServers,omitempty" json:"mcpServers,omitempty"`
}
type (
	MCPRemoteServers map[string]MCPRemoteServer
	MCPSTDIOServers  map[string]MCPSTDIOServer
)

// @Description MCP remote server configuration
type MCPRemoteServer struct {
	URL   string `json:"url,omitempty"`
	Token string `json:"token,omitempty"`
}

// @Description MCP STDIO server configuration
type MCPSTDIOServer struct {
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Command string            `json:"command,omitempty"`
}

// @Description Pipeline defines other models to use for audio-to-audio
type Pipeline struct {
	TTS           string `yaml:"tts,omitempty" json:"tts,omitempty"`
	LLM           string `yaml:"llm,omitempty" json:"llm,omitempty"`
	Transcription string `yaml:"transcription,omitempty" json:"transcription,omitempty"`
	VAD           string `yaml:"vad,omitempty" json:"vad,omitempty"`
	// SoundDetection names a sound-event-classification model (e.g. ced). When
	// set, each VAD-committed realtime utterance is also run through it and the
	// scored AudioSet tags are emitted as a conversation.item.sound_detection
	// server event, alongside (and independent of) transcription.
	SoundDetection string `yaml:"sound_detection,omitempty" json:"sound_detection,omitempty"`

	// SoundDetectionWindowMs / SoundDetectionHopMs enable server-side windowing
	// for a sound-detection-only realtime session: instead of the client
	// committing audio buffers, the server classifies the last WindowMs of
	// streamed audio every HopMs and emits a sound_detection event per hop. Both
	// must be > 0 to activate; otherwise the session stays client-driven (the
	// client commits windows via input_audio_buffer.commit).
	SoundDetectionWindowMs int `yaml:"sound_detection_window_ms,omitempty" json:"sound_detection_window_ms,omitempty"`
	SoundDetectionHopMs    int `yaml:"sound_detection_hop_ms,omitempty" json:"sound_detection_hop_ms,omitempty"`

	// ReasoningEffort sets the reasoning effort (none|minimal|low|medium|high) for
	// the pipeline's LLM without editing the LLM model config. Overrides the LLM's
	// own reasoning_effort. Unset leaves the LLM model config in charge.
	ReasoningEffort string `yaml:"reasoning_effort,omitempty" json:"reasoning_effort,omitempty"`

	// Streaming opts each pipeline stage into incremental delivery (LLM tokens,
	// TTS audio chunks, transcription text). Unset stages keep the blocking
	// unary path, so existing configs are unaffected.
	Streaming PipelineStreaming `yaml:"streaming,omitempty" json:"streaming,omitempty"`

	// DisableThinking suppresses reasoning/thinking for the pipeline LLM (maps
	// to enable_thinking=false backend metadata) without editing the underlying
	// LLM model config. Unset leaves the LLM model config in charge.
	DisableThinking *bool `yaml:"disable_thinking,omitempty" json:"disable_thinking,omitempty"`

	// MaxHistoryItems caps how many trailing conversation items are fed to the
	// LLM each realtime turn (0 = unlimited, rely on the LLM's context window).
	// Unset (nil) uses the per-model-type default. Set it on a composed pipeline
	// (VAD+STT+LLM+TTS) so a long-running session doesn't grow until the LLM's
	// context fills.
	MaxHistoryItems *int `yaml:"max_history_items,omitempty" json:"max_history_items,omitempty"`

	// Compaction folds conversation items that age out of the live window
	// (max_history_items) into a rolling summary instead of dropping them, so
	// long realtime sessions stay cheap without losing earlier context. Nil
	// (block absent) means disabled, preserving existing behavior.
	Compaction *PipelineCompaction `yaml:"compaction,omitempty" json:"compaction,omitempty"`

	// VoiceRecognition gates the pipeline behind speaker verification. Nil
	// (block absent) means no gate, preserving existing behavior.
	VoiceRecognition *PipelineVoiceRecognition `yaml:"voice_recognition,omitempty" json:"voice_recognition,omitempty"`

	// TurnDetection sets the server-side default turn-detection mode for
	// realtime sessions on this pipeline, so clients need no session.update
	// to benefit. A client session.update still overrides type and eagerness
	// per session; retranscribe is server-side only. Unset keeps server_vad.
	TurnDetection PipelineTurnDetection `yaml:"turn_detection,omitempty" json:"turn_detection,omitempty"`

	// Classifier switches realtime responses to prefill-only option
	// selection (LocalAI classifier mode): each user turn is scored
	// against a fixed option list via the Score primitive and the winning
	// option's canned reply / tool call is emitted, so weak hardware
	// never pays for autoregressive decode. Nil means disabled; clients
	// can still enable per session via session.update localai_classifier.
	// Validated (and rejected loudly) at realtime session setup, like the
	// pipeline model slots.
	Classifier *PipelineClassifier `yaml:"classifier,omitempty" json:"classifier,omitempty"`

	// DisableWarmup turns off eager pre-loading of the pipeline's sub-models at
	// realtime session start. By default (false) LocalAI loads every configured
	// sub-model backend (VAD, transcription, LLM, TTS, sound detection, voice
	// recognition) into memory (concurrently) before the
	// session is announced and blocks until they are ready, so the first turn
	// pays no cold-start cost and a model that fails to load surfaces as an error
	// at session start rather than mid-call. Set true to restore the lazy "load
	// on first use" behavior — session start no longer blocks on loading and
	// load errors surface on first use instead (e.g. to keep idle sessions from
	// holding model memory they may never use).
	DisableWarmup bool `yaml:"disable_warmup,omitempty" json:"disable_warmup,omitempty"`
}

// PipelineClassifier is the YAML mirror of the realtime API's
// localai_classifier extension (see
// core/http/endpoints/openai/types/classifier.go, which documents the
// field semantics and owns validation — the realtime session converts and
// validates this block at setup).
type PipelineClassifier struct {
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Model optionally names a different config to score on. Empty uses
	// the pipeline's llm — with slot-based Score the same process serves
	// both scoring and generation and shares its prompt cache.
	Model         string                      `yaml:"model,omitempty" json:"model,omitempty"`
	Threshold     float64                     `yaml:"threshold,omitempty" json:"threshold,omitempty"`
	Normalization string                      `yaml:"normalization,omitempty" json:"normalization,omitempty"`
	HistoryItems  int                         `yaml:"history_items,omitempty" json:"history_items,omitempty"`
	Fallback      *PipelineClassifierFallback `yaml:"fallback,omitempty" json:"fallback,omitempty"`
	Options       []PipelineClassifierOption  `yaml:"options,omitempty" json:"options,omitempty"`
	// Address gates every turn on the assistant being addressed by one of
	// these names (wake-word behavior); see types.ClassifierAddress.
	Address *PipelineClassifierAddress `yaml:"address,omitempty" json:"address,omitempty"`
}

// PipelineClassifierAddress mirrors types.ClassifierAddress for YAML.
type PipelineClassifierAddress struct {
	Names []string `yaml:"names,omitempty" json:"names,omitempty"`
	Mode  string   `yaml:"mode,omitempty" json:"mode,omitempty"`
	Reply string   `yaml:"reply,omitempty" json:"reply,omitempty"`
}

type PipelineClassifierOption struct {
	ID          string                  `yaml:"id" json:"id"`
	Description string                  `yaml:"description" json:"description"`
	Reply       string                  `yaml:"reply,omitempty" json:"reply,omitempty"`
	Tool        *PipelineClassifierTool `yaml:"tool,omitempty" json:"tool,omitempty"`
}

type PipelineClassifierTool struct {
	Name string `yaml:"name" json:"name"`
	// Arguments is a plain YAML map; the realtime session marshals it to
	// the JSON arguments string of the emitted function call.
	Arguments map[string]any `yaml:"arguments,omitempty" json:"arguments,omitempty"`
}

type PipelineClassifierFallback struct {
	Mode  string `yaml:"mode,omitempty" json:"mode,omitempty"`
	Reply string `yaml:"reply,omitempty" json:"reply,omitempty"`
}

// PipelineCompaction configures summarize-then-drop for a realtime pipeline.
type PipelineCompaction struct {
	// Enabled turns summarize-then-drop on. Default false.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// TriggerItems is the high-water mark: once live items exceed it, overflow
	// above max_history_items is summarized and evicted. Must exceed
	// max_history_items; clamped up if not. Default: 2x max_history_items.
	TriggerItems int `yaml:"trigger_items,omitempty" json:"trigger_items,omitempty"`
	// SummaryModel optionally names a smaller/cheaper model for the summary
	// call. Empty uses the pipeline's own LLM.
	SummaryModel string `yaml:"summary_model,omitempty" json:"summary_model,omitempty"`
	// MaxSummaryTokens advises the summary length (fed to the prompt). Default 512.
	MaxSummaryTokens int `yaml:"max_summary_tokens,omitempty" json:"max_summary_tokens,omitempty"`
}

// ApplyReasoningEffort resolves the effective reasoning effort — a per-request
// value (requestEffort) overrides the config's own ReasoningEffort default —
// stores it on the config so gRPCPredictOpts forwards it to the backend as the
// reasoning_effort chat_template_kwarg, and maps it onto the enable_thinking
// toggle the backend also reads:
//   - "none" always disables thinking.
//   - any explicit level enables it, UNLESS the config already disabled reasoning
//     (an operator's explicit disable wins over a request asking to think).
//
// An empty requestEffort keeps the config's own default. With no effort set
// anywhere it is a no-op, leaving the model's reasoning settings untouched.
func (c *ModelConfig) ApplyReasoningEffort(requestEffort string) {
	effort := requestEffort
	if effort == "" {
		effort = c.ReasoningEffort
	}
	c.ReasoningEffort = effort
	switch strings.ToLower(effort) {
	case "none":
		disable := true
		c.ReasoningConfig.DisableReasoning = &disable
	case "minimal", "low", "medium", "high":
		if c.ReasoningConfig.DisableReasoning == nil || !*c.ReasoningConfig.DisableReasoning {
			enable := false
			c.ReasoningConfig.DisableReasoning = &enable
		}
	}
}

// coerceChatTemplateKwarg coerces a request-metadata string value for use as a
// jinja chat_template_kwarg. "true"/"false" become real booleans (so a jinja
// `{% if preserve_thinking %}` reads false correctly, since any non-empty string
// is truthy); everything else stays a string. Numeric/typed per-request values are
// out of scope - set those in the model YAML chat_template_kwargs (YAML keeps the type).
func coerceChatTemplateKwarg(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	default:
		return v
	}
}

// ResolveChatTemplateKwargs builds the final chat_template_kwargs map forwarded to
// the backend, layered: the model config map (base) < the coerced backend metadata
// (server reasoning levers + client request overrides). `meta` is the already-merged
// backend metadata string map. The reserved "chat_template_kwargs" key is skipped so
// a client cannot smuggle a nested blob. Returns nil when there is nothing to forward.
func (c *ModelConfig) ResolveChatTemplateKwargs(meta map[string]string) map[string]any {
	out := map[string]any{}
	for k, v := range c.ChatTemplateKwargs {
		out[k] = v
	}
	for k, v := range meta {
		if k == "chat_template_kwargs" {
			continue
		}
		out[k] = coerceChatTemplateKwarg(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// @Description PipelineStreaming toggles incremental delivery per realtime stage.
type PipelineStreaming struct {
	LLM           *bool `yaml:"llm,omitempty" json:"llm,omitempty"`
	TTS           *bool `yaml:"tts,omitempty" json:"tts,omitempty"`
	Transcription *bool `yaml:"transcription,omitempty" json:"transcription,omitempty"`
	// ClauseChunking splits the streamed LLM reply into speakable clauses and
	// synthesizes each as soon as it completes, instead of buffering the whole
	// message before TTS. Script-aware (CJK/Thai), so it does not rely on
	// whitespace sentence boundaries. Requires LLM streaming; unset buffers the
	// whole message (today's default).
	ClauseChunking *bool `yaml:"clause_chunking,omitempty" json:"clause_chunking,omitempty"`
}

// StreamLLM reports whether LLM tokens should be streamed for this pipeline.
func (p Pipeline) StreamLLM() bool { return p.Streaming.LLM != nil && *p.Streaming.LLM }

// StreamTTS reports whether TTS audio should be streamed for this pipeline.
func (p Pipeline) StreamTTS() bool { return p.Streaming.TTS != nil && *p.Streaming.TTS }

// StreamTranscription reports whether transcription text should be streamed.
func (p Pipeline) StreamTranscription() bool {
	return p.Streaming.Transcription != nil && *p.Streaming.Transcription
}

// ChunkClauses reports whether the streamed reply should be split into
// script-aware clauses and synthesized incrementally rather than buffered whole.
func (p Pipeline) ChunkClauses() bool {
	return p.Streaming.ClauseChunking != nil && *p.Streaming.ClauseChunking
}

// ThinkingDisabled reports whether the pipeline forces the LLM's thinking off.
func (p Pipeline) ThinkingDisabled() bool {
	return p.DisableThinking != nil && *p.DisableThinking
}

// Voice-recognition gate enum values.
const (
	VoiceGateModeIdentify = "identify"
	VoiceGateModeVerify   = "verify"
	VoiceGateWhenEvery    = "every"
	VoiceGateWhenFirst    = "first"
	VoiceGateRejectEvent  = "drop_event"
	VoiceGateRejectSilent = "drop_silent"

	// defaultVoiceGateThreshold is the cosine-distance default tuned for the
	// ECAPA-TDNN speaker encoder on VoxCeleb.
	defaultVoiceGateThreshold = 0.25
)

// @Description PipelineVoiceRecognition gates a realtime pipeline behind speaker verification.
type PipelineVoiceRecognition struct {
	// Model is the speaker-recognition backend model name.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	// Mode is "identify" (1:N against the voice registry) or "verify"
	// (1:few against reference audios).
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
	// Threshold is the maximum cosine distance that still counts as a match.
	Threshold float32 `yaml:"threshold,omitempty" json:"threshold,omitempty"`
	// When is "every" (verify each utterance) or "first" (verify once, then
	// trust the session).
	When string `yaml:"when,omitempty" json:"when,omitempty"`
	// OnReject is "drop_event" (drop + emit an error event) or "drop_silent"
	// (drop quietly).
	OnReject string `yaml:"on_reject,omitempty" json:"on_reject,omitempty"`
	// AntiSpoofing enables the backend liveness check (verify mode only).
	AntiSpoofing bool `yaml:"anti_spoofing,omitempty" json:"anti_spoofing,omitempty"`
	// Allow filters which registry identities are authorized (identify mode).
	Allow VoiceRecognitionAllow `yaml:"allow,omitempty" json:"allow,omitempty"`
	// References are the authorized reference speakers (verify mode).
	References []VoiceReference `yaml:"references,omitempty" json:"references,omitempty"`
	// Enforce controls the authorization gate. A nil value or true rejects
	// unauthorized speakers (the historical behavior). false resolves the
	// speaker's identity for surfacing/personalization but never drops a turn.
	Enforce *bool `yaml:"enforce,omitempty" json:"enforce,omitempty"`
	// Identity surfaces the recognized speaker to the client and the LLM. It is
	// independent of Enforce: identity can be surfaced without gating.
	Identity *VoiceIdentityConfig `yaml:"identity,omitempty" json:"identity,omitempty"`
}

// @Description VoiceRecognitionAllow filters authorized registry identities.
type VoiceRecognitionAllow struct {
	// Names matches registered Metadata.Name exactly.
	Names []string `yaml:"names,omitempty" json:"names,omitempty"`
	// Labels authorizes any identity carrying a matching label key.
	Labels []string `yaml:"labels,omitempty" json:"labels,omitempty"`
}

// @Description VoiceReference is one authorized reference speaker for verify mode.
type VoiceReference struct {
	Name  string `yaml:"name,omitempty" json:"name,omitempty"`
	Audio string `yaml:"audio,omitempty" json:"audio,omitempty"`
}

// @Description VoiceIdentityConfig surfaces the recognized speaker to the realtime
// client and the LLM. When set, identity is resolved on every turn even if the
// gate's When is "first" (the gate still authorizes only once).
type VoiceIdentityConfig struct {
	// Announce emits a conversation.item.speaker event to the client.
	Announce bool `yaml:"announce,omitempty" json:"announce,omitempty"`
	// AnnounceUnknown also emits the event when there is no confident match.
	AnnounceUnknown bool `yaml:"announce_unknown,omitempty" json:"announce_unknown,omitempty"`
	// Personalize informs the LLM who is speaking.
	Personalize bool `yaml:"personalize,omitempty" json:"personalize,omitempty"`
	// InjectName sets the per-message name field on each user turn.
	InjectName bool `yaml:"inject_name,omitempty" json:"inject_name,omitempty"`
	// InjectSystemNote maintains a "current speaker" note in the system message.
	InjectSystemNote bool `yaml:"inject_system_note,omitempty" json:"inject_system_note,omitempty"`
	// NoteUnknown adds a "the current speaker is unknown" note (enables the model
	// to ask who it is talking to).
	NoteUnknown bool `yaml:"note_unknown,omitempty" json:"note_unknown,omitempty"`
}

// VoiceGateEnabled reports whether a voice-recognition gate is configured. The
// mere presence of the block is the intent signal: a present-but-incomplete
// block (e.g. missing model) must fail closed at construction, not be silently
// skipped here.
func (p Pipeline) VoiceGateEnabled() bool {
	return p.VoiceRecognition != nil
}

// EnforceGate reports whether the gate rejects unauthorized speakers. A nil
// Enforce means "enforce" so existing configs keep gating.
func (p PipelineVoiceRecognition) EnforceGate() bool {
	return p.Enforce == nil || *p.Enforce
}

// IdentityEnabled reports whether the speaker's identity must be resolved for
// surfacing or personalization.
func (p PipelineVoiceRecognition) IdentityEnabled() bool {
	return p.Identity != nil && (p.Identity.Announce || p.Identity.Personalize)
}

// AnnounceEnabled reports whether to emit the conversation.item.speaker event.
func (p PipelineVoiceRecognition) AnnounceEnabled() bool {
	return p.Identity != nil && p.Identity.Announce
}

// PersonalizeEnabled reports whether to inform the LLM of the speaker.
func (p PipelineVoiceRecognition) PersonalizeEnabled() bool {
	return p.Identity != nil && p.Identity.Personalize
}

// Normalize fills in defaults in place for omitted fields.
func (v *PipelineVoiceRecognition) Normalize() {
	if v.Mode == "" {
		v.Mode = VoiceGateModeIdentify
	}
	if v.When == "" {
		v.When = VoiceGateWhenEvery
	}
	if v.OnReject == "" {
		v.OnReject = VoiceGateRejectEvent
	}
	if v.Threshold == 0 {
		v.Threshold = defaultVoiceGateThreshold
	}
}

// Validate checks shape and enum values. registryAvailable indicates whether a
// VoiceRegistry exists (required by identify mode). Empty When/OnReject/Mode are
// treated as valid because Normalize defaults them.
func (v PipelineVoiceRecognition) Validate(registryAvailable bool) error {
	if v.Model == "" {
		return fmt.Errorf("voice_recognition: model is required")
	}
	switch v.Mode {
	case "", VoiceGateModeIdentify:
		if !registryAvailable {
			return fmt.Errorf("voice_recognition mode 'identify' requires a voice registry")
		}
	case VoiceGateModeVerify:
		if len(v.References) == 0 {
			return fmt.Errorf("voice_recognition mode 'verify' requires at least one reference")
		}
		for i, r := range v.References {
			if r.Audio == "" {
				return fmt.Errorf("voice_recognition reference %d (%q) is missing an audio path", i, r.Name)
			}
		}
	default:
		return fmt.Errorf("voice_recognition: unknown mode %q", v.Mode)
	}
	switch v.When {
	case "", VoiceGateWhenEvery, VoiceGateWhenFirst:
	default:
		return fmt.Errorf("voice_recognition: unknown when %q", v.When)
	}
	switch v.OnReject {
	case "", VoiceGateRejectEvent, VoiceGateRejectSilent:
	default:
		return fmt.Errorf("voice_recognition: unknown on_reject %q", v.OnReject)
	}
	// A zero threshold means "unset" (Normalize defaults it); only validate an
	// explicitly-set value. Cosine distance ranges 0..2.
	if v.Threshold != 0 && (v.Threshold < 0 || v.Threshold > 2) {
		return fmt.Errorf("voice_recognition: threshold %v out of range (0..2)", v.Threshold)
	}
	return nil
}

// @Description PipelineTurnDetection sets realtime turn-detection defaults.
type PipelineTurnDetection struct {
	// Type selects the default turn_detection mode for sessions on this
	// pipeline: "server_vad" (silence-based) or "semantic_vad" (the
	// transcription model's end-of-utterance token drives a dynamic silence
	// window; needs a streaming-EOU transcription model such as
	// parakeet_realtime_eou_120m-v1, degrades to silence-only otherwise).
	Type string `yaml:"type,omitempty" json:"type,omitempty"`
	// Eagerness is the semantic_vad fallback when no end-of-utterance token
	// was seen: low waits 8s of silence, medium/auto 4s, high 2s.
	Eagerness string `yaml:"eagerness,omitempty" json:"eagerness,omitempty"`
	// Retranscribe (semantic_vad only) cross-checks every EOU-triggered
	// commit with an offline decode of the buffered turn: the commit only
	// proceeds when the batch decode also ends in the end-of-utterance token,
	// and its transcript is the one used. The streamed and batch transcripts
	// are compared in the logs — a diagnostic for streaming/batch alignment
	// at the cost of one extra decode per turn.
	Retranscribe *bool `yaml:"retranscribe,omitempty" json:"retranscribe,omitempty"`
}

// TurnDetectionSemantic reports whether this pipeline defaults sessions to
// semantic (EOU-driven) turn detection.
func (p Pipeline) TurnDetectionSemantic() bool {
	return strings.EqualFold(strings.TrimSpace(p.TurnDetection.Type), "semantic_vad")
}

// TurnDetectionRetranscribe reports whether semantic_vad commits should be
// cross-checked (and transcribed) by an offline decode of the buffered turn.
func (p Pipeline) TurnDetectionRetranscribe() bool {
	return p.TurnDetection.Retranscribe != nil && *p.TurnDetection.Retranscribe
}

// @Description File configuration for model downloads
type File struct {
	Filename string         `yaml:"filename,omitempty" json:"filename,omitempty"`
	SHA256   string         `yaml:"sha256,omitempty" json:"sha256,omitempty"`
	URI      downloader.URI `yaml:"uri,omitempty" json:"uri,omitempty"`
}

type FeatureFlag map[string]*bool

func (ff FeatureFlag) Enabled(s string) bool {
	if v, exists := ff[s]; exists && v != nil {
		return *v
	}
	return false
}

// @Description GRPC configuration
type GRPC struct {
	Attempts          int `yaml:"attempts,omitempty" json:"attempts,omitempty"`
	AttemptsSleepTime int `yaml:"attempts_sleep_time,omitempty" json:"attempts_sleep_time,omitempty"`
}

// @Description Diffusers configuration
type Diffusers struct {
	CUDA             bool   `yaml:"cuda,omitempty" json:"cuda,omitempty"`
	PipelineType     string `yaml:"pipeline_type,omitempty" json:"pipeline_type,omitempty"`
	SchedulerType    string `yaml:"scheduler_type,omitempty" json:"scheduler_type,omitempty"`
	EnableParameters string `yaml:"enable_parameters,omitempty" json:"enable_parameters,omitempty"` // A list of comma separated parameters to specify
	IMG2IMG          bool   `yaml:"img2img,omitempty" json:"img2img,omitempty"`                     // Image to Image Diffuser
	ClipSkip         int    `yaml:"clip_skip,omitempty" json:"clip_skip,omitempty"`                 // Skip every N frames
	ClipModel        string `yaml:"clip_model,omitempty" json:"clip_model,omitempty"`               // Clip model to use
	ClipSubFolder    string `yaml:"clip_subfolder,omitempty" json:"clip_subfolder,omitempty"`       // Subfolder to use for clip model
	ControlNet       string `yaml:"control_net,omitempty" json:"control_net,omitempty"`
}

// @Description LLMConfig is a struct that holds the configuration that are generic for most of the LLM backends.
type LLMConfig struct {
	SystemPrompt    string   `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"`
	TensorSplit     string   `yaml:"tensor_split,omitempty" json:"tensor_split,omitempty"`
	MainGPU         string   `yaml:"main_gpu,omitempty" json:"main_gpu,omitempty"`
	RMSNormEps      float32  `yaml:"rms_norm_eps,omitempty" json:"rms_norm_eps,omitempty"`
	NGQA            int32    `yaml:"ngqa,omitempty" json:"ngqa,omitempty"`
	PromptCachePath string   `yaml:"prompt_cache_path,omitempty" json:"prompt_cache_path,omitempty"`
	PromptCacheAll  *bool    `yaml:"prompt_cache_all,omitempty" json:"prompt_cache_all,omitempty"`
	PromptCacheRO   bool     `yaml:"prompt_cache_ro,omitempty" json:"prompt_cache_ro,omitempty"`
	MirostatETA     *float64 `yaml:"mirostat_eta,omitempty" json:"mirostat_eta,omitempty"`
	MirostatTAU     *float64 `yaml:"mirostat_tau,omitempty" json:"mirostat_tau,omitempty"`
	Mirostat        *int     `yaml:"mirostat,omitempty" json:"mirostat,omitempty"`
	NGPULayers      *int     `yaml:"gpu_layers,omitempty" json:"gpu_layers,omitempty"`
	MMap            *bool    `yaml:"mmap,omitempty" json:"mmap,omitempty"`
	MMlock          *bool    `yaml:"mmlock,omitempty" json:"mmlock,omitempty"`
	LowVRAM         *bool    `yaml:"low_vram,omitempty" json:"low_vram,omitempty"`
	Reranking       *bool    `yaml:"reranking,omitempty" json:"reranking,omitempty"`
	Grammar         string   `yaml:"grammar,omitempty" json:"grammar,omitempty"`
	StopWords       []string `yaml:"stopwords,omitempty" json:"stopwords,omitempty"`
	Cutstrings      []string `yaml:"cutstrings,omitempty" json:"cutstrings,omitempty"`
	ExtractRegex    []string `yaml:"extract_regex,omitempty" json:"extract_regex,omitempty"`
	TrimSpace       []string `yaml:"trimspace,omitempty" json:"trimspace,omitempty"`
	TrimSuffix      []string `yaml:"trimsuffix,omitempty" json:"trimsuffix,omitempty"`

	ContextSize          *int             `yaml:"context_size,omitempty" json:"context_size,omitempty"`
	NUMA                 bool             `yaml:"numa,omitempty" json:"numa,omitempty"`
	LoraAdapter          string           `yaml:"lora_adapter,omitempty" json:"lora_adapter,omitempty"`
	LoraBase             string           `yaml:"lora_base,omitempty" json:"lora_base,omitempty"`
	LoraAdapters         []string         `yaml:"lora_adapters,omitempty" json:"lora_adapters,omitempty"`
	LoraScales           []float32        `yaml:"lora_scales,omitempty" json:"lora_scales,omitempty"`
	LoraScale            float32          `yaml:"lora_scale,omitempty" json:"lora_scale,omitempty"`
	NoMulMatQ            bool             `yaml:"no_mulmatq,omitempty" json:"no_mulmatq,omitempty"`
	DraftModel           string           `yaml:"draft_model,omitempty" json:"draft_model,omitempty"`
	NDraft               int32            `yaml:"n_draft,omitempty" json:"n_draft,omitempty"`
	Quantization         string           `yaml:"quantization,omitempty" json:"quantization,omitempty"`
	LoadFormat           string           `yaml:"load_format,omitempty" json:"load_format,omitempty"`
	GPUMemoryUtilization float32          `yaml:"gpu_memory_utilization,omitempty" json:"gpu_memory_utilization,omitempty"` // vLLM
	TrustRemoteCode      bool             `yaml:"trust_remote_code,omitempty" json:"trust_remote_code,omitempty"`           // vLLM
	EnforceEager         bool             `yaml:"enforce_eager,omitempty" json:"enforce_eager,omitempty"`                   // vLLM
	SwapSpace            int              `yaml:"swap_space,omitempty" json:"swap_space,omitempty"`                         // vLLM
	MaxModelLen          int              `yaml:"max_model_len,omitempty" json:"max_model_len,omitempty"`                   // vLLM
	TensorParallelSize   int              `yaml:"tensor_parallel_size,omitempty" json:"tensor_parallel_size,omitempty"`     // vLLM
	DisableLogStatus     bool             `yaml:"disable_log_stats,omitempty" json:"disable_log_stats,omitempty"`           // vLLM
	DType                string           `yaml:"dtype,omitempty" json:"dtype,omitempty"`                                   // vLLM
	LimitMMPerPrompt     LimitMMPerPrompt `yaml:"limit_mm_per_prompt,omitempty" json:"limit_mm_per_prompt,omitempty"`       // vLLM
	// EngineArgs is a backend-native passthrough applied to the engine constructor
	// (e.g. vLLM AsyncEngineArgs). Values may be primitives or nested maps; nested
	// maps materialise into the backend's nested config dataclasses (e.g.
	// SpeculativeConfig, KVTransferConfig, CompilationConfig). Unknown keys cause
	// the backend to fail LoadModel with a list of valid names.
	EngineArgs map[string]any `yaml:"engine_args,omitempty" json:"engine_args,omitempty"`
	MMProj     string         `yaml:"mmproj,omitempty" json:"mmproj,omitempty"`

	FlashAttention *string `yaml:"flash_attention,omitempty" json:"flash_attention,omitempty"`
	NoKVOffloading bool    `yaml:"no_kv_offloading,omitempty" json:"no_kv_offloading,omitempty"`
	CacheTypeK     string  `yaml:"cache_type_k,omitempty" json:"cache_type_k,omitempty"`
	CacheTypeV     string  `yaml:"cache_type_v,omitempty" json:"cache_type_v,omitempty"`

	RopeScaling string `yaml:"rope_scaling,omitempty" json:"rope_scaling,omitempty"`
	ModelType   string `yaml:"type,omitempty" json:"type,omitempty"`

	YarnExtFactor  float32 `yaml:"yarn_ext_factor,omitempty" json:"yarn_ext_factor,omitempty"`
	YarnAttnFactor float32 `yaml:"yarn_attn_factor,omitempty" json:"yarn_attn_factor,omitempty"`
	YarnBetaFast   float32 `yaml:"yarn_beta_fast,omitempty" json:"yarn_beta_fast,omitempty"`
	YarnBetaSlow   float32 `yaml:"yarn_beta_slow,omitempty" json:"yarn_beta_slow,omitempty"`

	CFGScale float32 `yaml:"cfg_scale,omitempty" json:"cfg_scale,omitempty"` // Classifier-Free Guidance Scale
}

// @Description LimitMMPerPrompt is a struct that holds the configuration for the limit-mm-per-prompt config in vLLM
type LimitMMPerPrompt struct {
	LimitImagePerPrompt int `yaml:"image,omitempty" json:"image,omitempty"`
	LimitVideoPerPrompt int `yaml:"video,omitempty" json:"video,omitempty"`
	LimitAudioPerPrompt int `yaml:"audio,omitempty" json:"audio,omitempty"`
}

// @Description TemplateConfig is a struct that holds the configuration of the templating system
type TemplateConfig struct {
	// Chat is the template used in the chat completion endpoint
	Chat string `yaml:"chat,omitempty" json:"chat,omitempty"`

	// ChatMessage is the template used for chat messages
	ChatMessage string `yaml:"chat_message,omitempty" json:"chat_message,omitempty"`

	// Completion is the template used for completion requests
	Completion string `yaml:"completion,omitempty" json:"completion,omitempty"`

	// Edit is the template used for edit completion requests
	Edit string `yaml:"edit,omitempty" json:"edit,omitempty"`

	// Functions is the template used when tools are present in the client requests
	Functions string `yaml:"function,omitempty" json:"function,omitempty"`

	// UseTokenizerTemplate is a flag that indicates if the tokenizer template should be used.
	// Note: this is mostly consumed for backends such as vllm and transformers
	// that can use the tokenizers specified in the JSON config files of the models
	UseTokenizerTemplate bool `yaml:"use_tokenizer_template,omitempty" json:"use_tokenizer_template,omitempty"`

	// JoinChatMessagesByCharacter is a string that will be used to join chat messages together.
	// It defaults to \n
	JoinChatMessagesByCharacter *string `yaml:"join_chat_messages_by_character,omitempty" json:"join_chat_messages_by_character,omitempty"`

	Multimodal string `yaml:"multimodal,omitempty" json:"multimodal,omitempty"`

	ReplyPrefix string `yaml:"reply_prefix,omitempty" json:"reply_prefix,omitempty"`
}

func (c *ModelConfig) syncKnownUsecasesFromString() {
	c.KnownUsecases = GetUsecasesFromYAML(c.KnownUsecaseStrings)
	// Make sure the usecases are valid, we rewrite with what we identified
	c.KnownUsecaseStrings = []string{}
	for k, usecase := range GetAllModelConfigUsecases() {
		if c.HasUsecases(usecase) {
			c.KnownUsecaseStrings = append(c.KnownUsecaseStrings, k)
		}
	}
}

func (c *ModelConfig) UnmarshalYAML(value *yaml.Node) error {
	type BCAlias ModelConfig
	var aux BCAlias
	if err := value.Decode(&aux); err != nil {
		return err
	}

	mc := ModelConfig(aux)
	*c = mc
	c.syncKnownUsecasesFromString()
	return nil
}

func (c *ModelConfig) SetFunctionCallString(s string) {
	c.functionCallString = s
}

func (c *ModelConfig) SetFunctionCallNameString(s string) {
	c.functionCallNameString = s
}

func (c *ModelConfig) ShouldUseFunctions() bool {
	return ((c.functionCallString != "none" || c.functionCallString == "") || c.ShouldCallSpecificFunction())
}

func (c *ModelConfig) ShouldCallSpecificFunction() bool {
	return len(c.functionCallNameString) > 0
}

// MMProjFileName returns the filename of the MMProj file
// If the MMProj is a URL, it will return the MD5 of the URL which is the filename
func (c *ModelConfig) MMProjFileName() string {
	uri := downloader.URI(c.MMProj)
	if uri.LooksLikeURL() {
		f, _ := uri.FilenameFromUrl()
		return f
	}

	return c.MMProj
}

func (c *ModelConfig) IsMMProjURL() bool {
	uri := downloader.URI(c.MMProj)
	return uri.LooksLikeURL()
}

func (c *ModelConfig) IsModelURL() bool {
	uri := downloader.URI(c.Model)
	return uri.LooksLikeURL()
}

// ModelID returns the identifier used to reference this model across the
// system: the configured Name, falling back to Model when Name is empty.
// This is the single source of truth for the id fed to model.WithModelID and
// the prefix-cache chain salt; both MUST agree with the router's tracking key
// or the prefix-cache salt diverges silently.
func (c ModelConfig) ModelID() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Model
}

// ModelFileName returns the controller-managed snapshot when the model has a
// committed artifact, otherwise preserving the legacy URL/repository behavior.
func (c *ModelConfig) ModelFileName() string {
	if len(c.Artifacts) > 0 && c.Artifacts[0].Resolved != nil {
		relative, err := modelartifacts.RelativeSnapshotPath(c.Artifacts[0].Resolved.CacheKey)
		if err == nil {
			// Single-file snapshots (e.g. a GGUF) must resolve to the file inside
			// the snapshot directory; single-file backends load a file, not a dir.
			// Multi-file snapshots keep pointing at the directory.
			if primary := c.Artifacts[0].Resolved.PrimaryFile; primary != "" {
				return filepath.Join(relative, filepath.FromSlash(primary))
			}
			return relative
		}
	}
	uri := downloader.URI(c.Model)
	if uri.LooksLikeURL() {
		f, _ := uri.FilenameFromUrl()
		return f
	}

	return c.Model
}

func (c *ModelConfig) FunctionToCall() string {
	if c.functionCallNameString != "" &&
		c.functionCallNameString != "none" && c.functionCallNameString != "auto" {
		return c.functionCallNameString
	}

	return c.functionCallString
}

func (cfg *ModelConfig) SetDefaults(opts ...ConfigLoaderOption) {
	lo := &LoadOptions{}
	lo.Apply(opts...)

	ctx := lo.ctxSize
	threads := lo.threads
	f16 := lo.f16
	debug := lo.debug

	// Cloud-proxy: normalise empty Mode so downstream consumers
	// switch on two concrete values only. Validate accepts empty too,
	// but SetDefaults is the chokepoint that runs before any
	// inference path reads cfg.Proxy.Mode.
	if cfg.Proxy.Mode == "" {
		cfg.Proxy.Mode = ProxyModePassthrough
	}

	// When templating is delegated to the backend (use_tokenizer_template),
	// the backend also owns tool-call grammar generation and parsing. Sending
	// a LocalAI-generated grammar alongside overrides the backend's native
	// (name-first) tool pipeline and makes it stream the tool-call JSON back as
	// plain content (issue #10052). The GGUF auto-import path already couples
	// these two flags; enforce it here so gallery and hand-written configs that
	// set use_tokenizer_template directly stay consistent.
	if cfg.TemplateConfig.UseTokenizerTemplate {
		cfg.FunctionsConfig.GrammarConfig.NoGrammar = true
	}

	// Apply model-family-specific inference defaults before generic fallbacks.
	// This ensures gallery-installed and runtime-loaded models get optimal parameters.
	ApplyInferenceDefaults(cfg, cfg.Name, cfg.Model)

	// Apply serving-policy defaults (device-independent): cross-request prefix
	// caching. Propagates to distributed nodes via the model options.
	ApplyServingDefaults(cfg)

	// Generic fallback defaults (sampling params + runtime flags), applied after
	// the model-family / hardware / serving tiers above. Only fills unset values.
	ApplyGenericDefaults(cfg)

	trueV := true
	falseV := false

	if threads == 0 {
		// Threads can't be 0
		threads = 4
	}

	if cfg.Threads == nil {
		cfg.Threads = &threads
	}

	if cfg.F16 == nil {
		cfg.F16 = &f16
	}

	if cfg.Debug == nil {
		cfg.Debug = &falseV
	}

	if debug {
		cfg.Debug = &trueV
	}

	// If a context size was provided via LoadOptions, apply it before hooks so they
	// don't override it with their own defaults.
	if ctx != 0 && cfg.ContextSize == nil {
		cfg.ContextSize = &ctx
	}
	runBackendHooks(cfg, lo.modelPath)

	// Apply hardware-driven defaults (e.g. a larger physical batch on Blackwell)
	// LAST, after the context size is fully resolved (explicit config, LoadOptions,
	// then the GGUF guess inside runBackendHooks): the Blackwell batch guard sizes
	// the per-device compute buffer against this model's context, so it must see
	// the final value, not a pre-guess nil. Uses the local GPU here; in distributed
	// mode the router re-applies the same heuristics for the selected node's GPU
	// before loading. Explicit config always wins.
	ApplyHardwareDefaults(cfg, localGPU())

	cfg.syncKnownUsecasesFromString()
}

func (c *ModelConfig) Validate() (bool, error) {
	if c.IsAlias() && len(c.Artifacts) > 0 {
		return false, fmt.Errorf("alias model %q cannot declare artifacts", c.Name)
	}
	seenArtifacts := make(map[string]struct{}, len(c.Artifacts))
	primaries := 0
	for i, artifact := range c.Artifacts {
		normalized, err := artifact.Normalize()
		if err != nil {
			return false, fmt.Errorf("artifact %d: %w", i, err)
		}
		if _, exists := seenArtifacts[normalized.Name]; exists {
			return false, fmt.Errorf("duplicate artifact name %q", normalized.Name)
		}
		seenArtifacts[normalized.Name] = struct{}{}
		if normalized.Target == modelartifacts.TargetModel {
			primaries++
			// Artifacts[0] is the load target for every consumer (ModelFileName,
			// size estimation, staging), so the primary has to occupy that slot
			// rather than merely exist somewhere in the list.
			if i != 0 {
				return false, fmt.Errorf("the primary artifact must be declared first, found it at position %d", i)
			}
		}
	}
	if len(c.Artifacts) > 0 && primaries != 1 {
		return false, fmt.Errorf("a config with artifacts must declare exactly one %q target, found %d", modelartifacts.TargetModel, primaries)
	}

	// An alias is a pure redirect: validate only its own shape here. Target
	// existence and the no-chain rule need the full config set, so the loader
	// (load-time) and the create/swap endpoints enforce those.
	if c.IsAlias() {
		if c.Name == "" {
			return false, fmt.Errorf("alias config requires a name")
		}
		if c.Alias == c.Name {
			return false, fmt.Errorf("alias %q cannot point to itself", c.Name)
		}
		if c.Backend != "" || c.Model != "" {
			return false, fmt.Errorf("alias config %q must not set backend or parameters.model: an alias is a pure redirect", c.Name)
		}
		return true, nil
	}

	downloadedFileNames := []string{}
	for _, f := range c.DownloadFiles {
		downloadedFileNames = append(downloadedFileNames, f.Filename)
	}
	validationTargets := []string{c.Backend, c.Model, c.MMProj}
	validationTargets = append(validationTargets, downloadedFileNames...)
	// Simple validation to make sure the model can be correctly loaded
	for _, n := range validationTargets {
		if n == "" {
			continue
		}
		if strings.HasPrefix(n, string(os.PathSeparator)) ||
			strings.Contains(n, "..") {
			return false, fmt.Errorf("invalid file path: %s", n)
		}
	}

	if c.Backend != "" {
		// a regex that checks that is a string name with no special characters, except '-' and '_'
		re := regexp.MustCompile(`^[a-zA-Z0-9-_]+$`)
		if !re.MatchString(c.Backend) {
			return false, fmt.Errorf("invalid backend name: %s", c.Backend)
		}
	}

	// Validate MCP configuration if present
	if c.MCP.Servers != "" || c.MCP.Stdio != "" {
		if _, _, err := c.MCP.MCPConfigFromYAML(); err != nil {
			return false, fmt.Errorf("invalid MCP configuration: %w", err)
		}
	}

	// engine_args crosses the gRPC boundary as a JSON-encoded string. Reject
	// unmarshalable values here so a config that would silently lose user-set
	// options at load time is rejected at parse time instead.
	if len(c.EngineArgs) > 0 {
		if _, err := json.Marshal(c.EngineArgs); err != nil {
			return false, fmt.Errorf("engine_args is not JSON-serialisable: %w", err)
		}
	}

	// Cloud-proxy: at most one of api_key_env / api_key_file may be
	// set. Both empty means no Authorization header (no-auth upstream
	// or a development passthrough). The mode field accepts the empty
	// string (defaults to passthrough), "passthrough", or "translate".
	if c.Proxy.APIKeyEnv != "" && c.Proxy.APIKeyFile != "" {
		return false, fmt.Errorf("proxy: api_key_env and api_key_file are mutually exclusive")
	}
	switch c.Proxy.Mode {
	case "", ProxyModePassthrough, ProxyModeTranslate:
		// Empty is accepted at validate-time and normalised to
		// passthrough by SetDefaults so it never reaches runtime.
	default:
		return false, fmt.Errorf("proxy: unknown mode %q (expected %s or %s)",
			c.Proxy.Mode, ProxyModePassthrough, ProxyModeTranslate)
	}
	if c.Proxy.Mode == ProxyModeTranslate && c.Proxy.Provider == "" {
		return false, fmt.Errorf("proxy: translate mode requires provider (%s, %s)",
			ProxyProviderOpenAI, ProxyProviderAnthropic)
	}

	// Score on llama-cpp runs through the slot loop (SERVER_TASK_TYPE_SCORE,
	// see backend/cpp/llama-cpp/patches/), so it is safe to combine with
	// chat/completion/embeddings on one config — no conflict check needed.

	// Pattern detector: validate built-in names and that each operator-defined
	// pattern is a well-formed, anchored, bounded restricted-regex. Reject at
	// load so a bad pattern surfaces as a clear config error rather than a
	// silent no-op (or a fail-closed block) at request time.
	if c.IsPatternDetector() {
		for _, name := range c.PIIDetection.Builtins {
			if _, ok := piipattern.LookupBuiltin(name); !ok {
				return false, fmt.Errorf("pii_detection: unknown built-in pattern %q", name)
			}
		}
		for _, p := range c.PIIDetection.Patterns {
			if p.Name == "" {
				return false, fmt.Errorf("pii_detection: pattern is missing a name")
			}
			if err := piipattern.ValidatePattern(p.Match); err != nil {
				return false, fmt.Errorf("pii_detection: pattern %q: %w", p.Name, err)
			}
		}
	}

	// router.score_normalization is consumed lazily by the score
	// classifier at first-request time; without load-time validation
	// a typo wouldn't surface until the first router request panicked
	// inside NewScoreClassifier. Reject unknown values here so the
	// operator sees the offending key at startup.
	switch c.Router.ScoreNormalization {
	case "", ScoreNormalizationRaw, ScoreNormalizationMean:
		// ok
	default:
		return false, fmt.Errorf("router: unknown score_normalization %q (expected %q or %q)",
			c.Router.ScoreNormalization, ScoreNormalizationRaw, ScoreNormalizationMean)
	}

	// router.classifier_system_template parses as Go text/template
	// (Sprig funcs available at execution time). Reject malformed
	// templates at load time so the operator sees the parse error
	// at startup rather than as a 500 on the first router request.
	if c.Router.ClassifierSystemTemplate != "" {
		if _, err := template.New("classifier_system").Parse(c.Router.ClassifierSystemTemplate); err != nil {
			return false, fmt.Errorf("router: classifier_system_template parse error: %w", err)
		}
	}

	return true, nil
}

// Score normalisation modes mirror router.ScoreNormalization* —
// duplicated as constants on the config package so ModelConfig.Validate
// can reject unknown values without taking a dependency on the router
// package (which already depends on config).
const (
	ScoreNormalizationRaw  = "raw"
	ScoreNormalizationMean = "mean"
)

func (c *ModelConfig) HasTemplate() bool {
	return c.TemplateConfig.Completion != "" || c.TemplateConfig.Edit != "" || c.TemplateConfig.Chat != "" || c.TemplateConfig.ChatMessage != "" || c.TemplateConfig.UseTokenizerTemplate
}

func (c *ModelConfig) GetModelConfigFile() string {
	return c.modelConfigFile
}

// GetModelTemplate returns the model's chat template if available
func (c *ModelConfig) GetModelTemplate() string {
	return c.modelTemplate
}

// IsDisabled returns true if the model is disabled
func (c *ModelConfig) IsDisabled() bool {
	return c.Disabled != nil && *c.Disabled
}

// IsPinned returns true if the model is pinned (excluded from idle unloading and eviction)
func (c *ModelConfig) IsPinned() bool {
	return c.Pinned != nil && *c.Pinned
}

// GetConcurrencyGroups returns the model's concurrency groups, normalized:
// trimmed of whitespace, empty entries dropped, deduped. Returns nil when no
// effective groups remain. The result is a fresh slice; the caller may
// mutate it without affecting the config.
func (c *ModelConfig) GetConcurrencyGroups() []string {
	if len(c.ConcurrencyGroups) == 0 {
		return nil
	}
	out := make([]string, 0, len(c.ConcurrencyGroups))
	for _, g := range c.ConcurrencyGroups {
		g = strings.TrimSpace(g)
		if g == "" || slices.Contains(out, g) {
			continue
		}
		out = append(out, g)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type ModelConfigUsecase int

const (
	FLAG_ANY                 ModelConfigUsecase = 0b000000000000
	FLAG_CHAT                ModelConfigUsecase = 0b000000000001
	FLAG_COMPLETION          ModelConfigUsecase = 0b000000000010
	FLAG_EDIT                ModelConfigUsecase = 0b000000000100
	FLAG_EMBEDDINGS          ModelConfigUsecase = 0b000000001000
	FLAG_RERANK              ModelConfigUsecase = 0b000000010000
	FLAG_IMAGE               ModelConfigUsecase = 0b000000100000
	FLAG_TRANSCRIPT          ModelConfigUsecase = 0b000001000000
	FLAG_TTS                 ModelConfigUsecase = 0b000010000000
	FLAG_SOUND_GENERATION    ModelConfigUsecase = 0b000100000000
	FLAG_TOKENIZE            ModelConfigUsecase = 0b001000000000
	FLAG_VAD                 ModelConfigUsecase = 0b010000000000
	FLAG_VIDEO               ModelConfigUsecase = 0b100000000000
	FLAG_DETECTION           ModelConfigUsecase = 0b1000000000000
	FLAG_VISION              ModelConfigUsecase = 0b10000000000000
	FLAG_FACE_RECOGNITION    ModelConfigUsecase = 0b100000000000000
	FLAG_SPEAKER_RECOGNITION ModelConfigUsecase = 0b1000000000000000
	FLAG_AUDIO_TRANSFORM     ModelConfigUsecase = 0b10000000000000000
	FLAG_DIARIZATION         ModelConfigUsecase = 0b100000000000000000
	FLAG_REALTIME_AUDIO      ModelConfigUsecase = 0b1000000000000000000
	// Marks a model as wired for the Score gRPC primitive (joint
	// log-prob of candidate continuations under a shared prompt). Must
	// be declared explicitly via `known_usecases: [score]` — there's
	// no heuristic for it. On llama-cpp, Score runs through the slot
	// loop (SERVER_TASK_TYPE_SCORE), so it may combine freely with
	// chat/completion/embeddings on one config and shares the slot's
	// prompt cache with generation.
	FLAG_SCORE ModelConfigUsecase = 0b10000000000000000000

	// Marks a model as wired for the Depth gRPC primitive (per-pixel
	// metric depth + camera pose + 3D point cloud via Depth Anything 3).
	FLAG_DEPTH ModelConfigUsecase = 0b100000000000000000000

	// Marks a model as wired for the TokenClassify gRPC primitive (the
	// openai-privacy-filter PII NER tier — per-token BIOES classification).
	// Like FLAG_SCORE it must be declared explicitly via
	// `known_usecases: [token_classify]`; there's no heuristic. Requires
	// TOKEN_CLS pooling, which is loaded via the embeddings flag. On
	// llama-cpp the classification windows ride the embedding task queue,
	// so it may combine freely with other usecases.
	FLAG_TOKEN_CLASSIFY ModelConfigUsecase = 0b1000000000000000000000

	// Marks a model as wired for the SoundDetection gRPC primitive
	// (audio tagging / sound-event classification — scored AudioSet
	// labels via the SoundDetection RPC, e.g. ced).
	FLAG_SOUND_CLASSIFICATION ModelConfigUsecase = 0b10000000000000000000000

	// Common Subsets
	FLAG_LLM ModelConfigUsecase = FLAG_CHAT | FLAG_COMPLETION | FLAG_EDIT
)

// ModalityGroups defines groups of usecases that belong to the same modality.
// Flags within the same group are NOT orthogonal (e.g., chat and completion are
// both text/language). A model is multimodal when its usecases span 2+ groups.
var ModalityGroups = []ModelConfigUsecase{
	FLAG_CHAT | FLAG_COMPLETION | FLAG_EDIT,                           // text/language
	FLAG_VISION | FLAG_DETECTION,                                      // visual understanding
	FLAG_TRANSCRIPT | FLAG_REALTIME_AUDIO | FLAG_SOUND_CLASSIFICATION, // audio input — realtime_audio is any-to-any, so it counts here too
	FLAG_TTS | FLAG_SOUND_GENERATION | FLAG_REALTIME_AUDIO,            // audio output — and here, so a lone realtime_audio flag still reads as multimodal
	FLAG_AUDIO_TRANSFORM,                                              // audio in/out transforms
	FLAG_IMAGE | FLAG_VIDEO,                                           // visual generation
}

// IsMultimodal returns true if the given usecases span two or more orthogonal
// modality groups. For example chat+vision is multimodal, but chat+completion
// is not (both belong to the text/language group).
func IsMultimodal(usecases ModelConfigUsecase) bool {
	groupCount := 0
	for _, group := range ModalityGroups {
		if usecases&group != 0 {
			groupCount++
			if groupCount >= 2 {
				return true
			}
		}
	}
	return false
}

func GetAllModelConfigUsecases() map[string]ModelConfigUsecase {
	return map[string]ModelConfigUsecase{
		// Note: FLAG_ANY is intentionally excluded from this map
		// because it's 0 and would always match in HasUsecases checks
		"FLAG_CHAT":                 FLAG_CHAT,
		"FLAG_COMPLETION":           FLAG_COMPLETION,
		"FLAG_EDIT":                 FLAG_EDIT,
		"FLAG_EMBEDDINGS":           FLAG_EMBEDDINGS,
		"FLAG_RERANK":               FLAG_RERANK,
		"FLAG_IMAGE":                FLAG_IMAGE,
		"FLAG_TRANSCRIPT":           FLAG_TRANSCRIPT,
		"FLAG_TTS":                  FLAG_TTS,
		"FLAG_SOUND_GENERATION":     FLAG_SOUND_GENERATION,
		"FLAG_TOKENIZE":             FLAG_TOKENIZE,
		"FLAG_VAD":                  FLAG_VAD,
		"FLAG_LLM":                  FLAG_LLM,
		"FLAG_VIDEO":                FLAG_VIDEO,
		"FLAG_DETECTION":            FLAG_DETECTION,
		"FLAG_VISION":               FLAG_VISION,
		"FLAG_FACE_RECOGNITION":     FLAG_FACE_RECOGNITION,
		"FLAG_SPEAKER_RECOGNITION":  FLAG_SPEAKER_RECOGNITION,
		"FLAG_AUDIO_TRANSFORM":      FLAG_AUDIO_TRANSFORM,
		"FLAG_DIARIZATION":          FLAG_DIARIZATION,
		"FLAG_SOUND_CLASSIFICATION": FLAG_SOUND_CLASSIFICATION,
		"FLAG_REALTIME_AUDIO":       FLAG_REALTIME_AUDIO,
		"FLAG_SCORE":                FLAG_SCORE,
		"FLAG_DEPTH":                FLAG_DEPTH,
		"FLAG_TOKEN_CLASSIFY":       FLAG_TOKEN_CLASSIFY,
	}
}

func stringToFlag(s string) string {
	return "FLAG_" + strings.ToUpper(s)
}

func GetUsecasesFromYAML(input []string) *ModelConfigUsecase {
	if len(input) == 0 {
		return nil
	}
	result := FLAG_ANY
	flags := GetAllModelConfigUsecases()
	for _, str := range input {
		for _, flag := range []string{stringToFlag(str), str} {
			f, exists := flags[flag]
			if exists {
				result |= f
			}
		}
	}
	return &result
}

// HasUsecases examines a ModelConfig and determines which endpoints have a chance of success.
//
// Declared known_usecases are normally additive — the guessing heuristic
// still adds whatever it can infer from backend/templates. The exceptions
// are FLAG_SCORE and FLAG_TOKEN_CLASSIFY: when the operator declared
// either, they reserved the model for an internal direct-decode primitive
// (the router classifier, or the PII NER tier). Letting GuessUsecases
// paint chat/completion/embeddings on top would surface it in pickers it
// was deliberately kept out of. So a declared score or token_classify
// list is authoritative; declare the generation usecases explicitly
// alongside score to serve both from one config.
func (c *ModelConfig) HasUsecases(u ModelConfigUsecase) bool {
	if c.KnownUsecases != nil {
		if (u & *c.KnownUsecases) == u {
			return true
		}
		if (*c.KnownUsecases & (FLAG_SCORE | FLAG_TOKEN_CLASSIFY)) != 0 {
			return false
		}
	}
	return c.GuessUsecases(u)
}

// GuessUsecases is a **heuristic based** function, as the backend in question may not be loaded yet, and the config may not record what it's useful at.
// In its current state, this function should ideally check for properties of the config like templates, rather than the direct backend name checks for the lower half.
// This avoids the maintenance burden of updating this list for each new backend - but unfortunately, that's the best option for some services currently.
func (c *ModelConfig) GuessUsecases(u ModelConfigUsecase) bool {
	// Backends that are clearly not text-generation
	nonTextGenBackends := []string{
		"whisper", "piper", "kokoro",
		"diffusers", "stablediffusion", "stablediffusion-ggml",
		"rerankers", "silero-vad", "rfdetr", "insightface", "speaker-recognition",
		"transformers-musicgen", "ace-step", "acestep-cpp",
	}

	if (u & FLAG_CHAT) == FLAG_CHAT {
		// A router model is a chat dispatcher: it carries no chat
		// template of its own (those live on the candidates it routes
		// to) and is invoked through the chat endpoint, so the router
		// block stands in for chat capability.
		if !c.HasRouter() {
			if c.TemplateConfig.Chat == "" && c.TemplateConfig.ChatMessage == "" && !c.TemplateConfig.UseTokenizerTemplate {
				return false
			}
			if slices.Contains(nonTextGenBackends, c.Backend) {
				return false
			}
			if c.Embeddings != nil && *c.Embeddings {
				return false
			}
		}
	}
	if (u & FLAG_COMPLETION) == FLAG_COMPLETION {
		if c.TemplateConfig.Completion == "" {
			return false
		}
		if slices.Contains(nonTextGenBackends, c.Backend) {
			return false
		}
	}
	if (u & FLAG_EDIT) == FLAG_EDIT {
		if c.TemplateConfig.Edit == "" {
			return false
		}
	}
	if (u & FLAG_EMBEDDINGS) == FLAG_EMBEDDINGS {
		if c.Embeddings == nil || !*c.Embeddings {
			return false
		}
	}
	if (u & FLAG_IMAGE) == FLAG_IMAGE {
		imageBackends := []string{"diffusers", "stablediffusion", "stablediffusion-ggml"}
		if !slices.Contains(imageBackends, c.Backend) {
			return false
		}

		if c.Backend == "diffusers" && c.Diffusers.PipelineType == "" {
			return false
		}

	}
	if (u & FLAG_VIDEO) == FLAG_VIDEO {
		videoBackends := []string{"diffusers", "stablediffusion", "vllm-omni"}
		if !slices.Contains(videoBackends, c.Backend) {
			return false
		}

		if c.Backend == "diffusers" && c.Diffusers.PipelineType == "" {
			return false
		}

	}
	if (u & FLAG_RERANK) == FLAG_RERANK {
		if c.Backend != "rerankers" && (c.Reranking == nil || !*c.Reranking) {
			return false
		}
	}
	if (u & FLAG_TRANSCRIPT) == FLAG_TRANSCRIPT {
		if c.Backend != "whisper" {
			return false
		}
		// whisper models with vad_only option are VAD, not transcription
		if slices.Contains(c.Options, "vad_only") {
			return false
		}
	}
	if (u & FLAG_TTS) == FLAG_TTS {
		ttsBackends := []string{"piper", "transformers-musicgen", "kokoro"}
		if !slices.Contains(ttsBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_DETECTION) == FLAG_DETECTION {
		detectionBackends := []string{"rfdetr", "sam3-cpp", "insightface"}
		if !slices.Contains(detectionBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_DEPTH) == FLAG_DEPTH {
		depthBackends := []string{"depth-anything"}
		if !slices.Contains(depthBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_FACE_RECOGNITION) == FLAG_FACE_RECOGNITION {
		faceBackends := []string{"insightface"}
		if !slices.Contains(faceBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_SPEAKER_RECOGNITION) == FLAG_SPEAKER_RECOGNITION {
		speakerBackends := []string{"speaker-recognition"}
		if !slices.Contains(speakerBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_AUDIO_TRANSFORM) == FLAG_AUDIO_TRANSFORM {
		audioTransformBackends := []string{"localvqe"}
		if !slices.Contains(audioTransformBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_SOUND_GENERATION) == FLAG_SOUND_GENERATION {
		soundGenBackends := []string{"transformers-musicgen", "ace-step", "acestep-cpp", "mock-backend"}
		if !slices.Contains(soundGenBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_TOKENIZE) == FLAG_TOKENIZE {
		tokenizeCapableBackends := []string{"llama.cpp", "rwkv"}
		if !slices.Contains(tokenizeCapableBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_VAD) == FLAG_VAD {
		if c.Backend != "silero-vad" && c.Backend != "sherpa-onnx" && !(c.Backend == "whisper" && slices.Contains(c.Options, "vad_only")) {
			return false
		}
	}

	if (u & FLAG_DIARIZATION) == FLAG_DIARIZATION {
		// vibevoice-cpp emits speaker-labelled segments natively from its
		// ASR pass; sherpa-onnx pipes pyannote segmentation + speaker
		// embeddings + clustering. Both surface as a Diarize gRPC.
		diarizationBackends := []string{"vibevoice-cpp", "sherpa-onnx"}
		if !slices.Contains(diarizationBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_SOUND_CLASSIFICATION) == FLAG_SOUND_CLASSIFICATION {
		// ced is a sound-event tagger (AudioSet labels) surfaced via the
		// SoundDetection gRPC. Models without an explicit known_usecases
		// still surface when they run on one of these backends.
		soundClassificationBackends := []string{"ced"}
		if !slices.Contains(soundClassificationBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_REALTIME_AUDIO) == FLAG_REALTIME_AUDIO {
		// Backends that own a single any-to-any loop and implement
		// AudioToAudioStream — listed here so models without an explicit
		// known_usecases still surface on the Talk page.
		realtimeAudioBackends := []string{"liquid-audio"}
		if !slices.Contains(realtimeAudioBackends, c.Backend) {
			return false
		}
	}

	if (u & FLAG_SCORE) == FLAG_SCORE {
		// No heuristic: Score-intent is a deliberate operator choice
		// (it keeps the model out of pickers it wasn't meant for), so
		// HasUsecases(FLAG_SCORE) is true only when KnownUsecases
		// declares it explicitly.
		return false
	}

	if (u & FLAG_TOKEN_CLASSIFY) == FLAG_TOKEN_CLASSIFY {
		// No heuristic: token-classification intent is a deliberate
		// operator choice (it reserves the model from generation traffic
		// on llama-cpp, and the model's TOKEN_CLS head isn't useful as
		// general embeddings), so HasUsecases(FLAG_TOKEN_CLASSIFY) is true
		// only when KnownUsecases declares it explicitly.
		return false
	}

	return true
}

// BuildCogitoOptions generates cogito options from the model configuration
// It accepts a context, MCP sessions, and optional callback functions for status, reasoning, tool calls, and tool results
func (c *ModelConfig) BuildCogitoOptions() []cogito.Option {
	cogitoOpts := []cogito.Option{
		cogito.WithIterations(3),  // default to 3 iterations
		cogito.WithMaxAttempts(3), // default to 3 attempts
		cogito.WithForceReasoning(),
	}

	// Apply agent configuration options
	if c.Agent.EnableReasoning {
		cogitoOpts = append(cogitoOpts, cogito.WithForceReasoning())
	}

	if c.Agent.EnablePlanning {
		cogitoOpts = append(cogitoOpts, cogito.EnableAutoPlan)
	}

	if c.Agent.EnableMCPPrompts {
		cogitoOpts = append(cogitoOpts, cogito.EnableMCPPrompts)
	}

	if c.Agent.EnablePlanReEvaluator {
		cogitoOpts = append(cogitoOpts, cogito.EnableAutoPlanReEvaluator)
	}

	if c.Agent.MaxIterations != 0 {
		cogitoOpts = append(cogitoOpts, cogito.WithIterations(c.Agent.MaxIterations))
	}

	if c.Agent.MaxAttempts != 0 {
		cogitoOpts = append(cogitoOpts, cogito.WithMaxAttempts(c.Agent.MaxAttempts))
	}

	if c.Agent.DisableSinkState {
		cogitoOpts = append(cogitoOpts, cogito.DisableSinkState)
	}

	if c.Agent.LoopDetection != 0 {
		cogitoOpts = append(cogitoOpts, cogito.WithLoopDetection(c.Agent.LoopDetection))
	}

	if c.Agent.MaxAdjustmentAttempts != 0 {
		cogitoOpts = append(cogitoOpts, cogito.WithMaxAdjustmentAttempts(c.Agent.MaxAdjustmentAttempts))
	}

	if c.Agent.ForceReasoningTool {
		cogitoOpts = append(cogitoOpts, cogito.WithForceReasoningTool())
	}

	return cogitoOpts
}
