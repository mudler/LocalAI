package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"
)

// ScorerFactory returns a backend.Scorer bound to a named classifier
// model. The score classifier uses it to compute joint log-prob of
// every policy label against the routing prompt.
type ScorerFactory func(modelName string) backend.Scorer

// EmbedderFactory returns a backend.Embedder bound to a named model.
// Used by the L2 embedding cache. Returning nil signals "model not
// loadable" — the middleware then falls back to the uncached
// classifier so routing still happens.
type EmbedderFactory func(modelName string) backend.Embedder

// EmbedderFingerprintFactory returns the identity of the embedding space a
// named model currently produces. Persisted KNN vectors are only reusable
// while this identity is unchanged.
type EmbedderFingerprintFactory func(modelName string) (string, error)

// VectorStoreFactory returns a backend.VectorStore bound to a named
// collection. Each router model's cache lives in its own collection
// so two routers can't poison each other's hits.
type VectorStoreFactory func(storeName string) backend.VectorStore

// RerankerFactory returns a backend.Reranker bound to a named model.
// Used by the colbert classifier to score policy descriptions against
// the prompt via LocalAI's rerankers backend. Returning nil signals
// "model not loadable" — buildClassifier reports a config error.
type RerankerFactory func(modelName string) backend.Reranker

// ModelConfigLookup resolves a model name to its config, or nil when
// unknown. Used by buildClassifier to confirm the classifier_model
// declared the score usecase — the actual usecase-conflict check
// lives in ModelConfig.Validate() and runs at config load/save time.
type ModelConfigLookup func(modelName string) *config.ModelConfig

// CorpusLoader syncs a router's persisted KNN corpus into the live
// vector index when the knn classifier is built. Implemented by
// corpus.Manager; declared here (consumer side) so the middleware
// doesn't depend on the corpus package. Optional — when nil, the knn
// classifier serves whatever the index already holds (tests, embedded
// callers).
type CorpusLoader interface {
	EnsureLoaded(ctx context.Context, storeName, embeddingModel, embeddingFingerprint string, embedder backend.Embedder, store backend.VectorStore) (int, error)
}

// ClassifierDeps bundles the backend factories the router middleware
// needs to build a classifier and its optional L2 cache. Bundled into
// one struct because RouteModel already takes many positional
// arguments — additions to the dependency surface go here instead of
// growing the signature.
//
// Embedder and VectorStore are optional: when both are non-nil and the
// router config declares an embedding_cache block, the score
// classifier is wrapped in EmbeddingCacheClassifier. Otherwise the
// score classifier runs unwrapped and the embedding-cache YAML is
// ignored with a warning.
type ClassifierDeps struct {
	Scorer   ScorerFactory
	Embedder EmbedderFactory
	// EmbedderFingerprint identifies the weights/config behind Embedder so
	// KNN corpus vectors cannot be queried across embedding spaces.
	EmbedderFingerprint EmbedderFingerprintFactory
	VectorStore         VectorStoreFactory
	Reranker            RerankerFactory

	// Corpus loads the persisted KNN corpus into the vector index when
	// a knn classifier is built. Optional; nil skips the load.
	Corpus CorpusLoader

	// ModelLookup resolves the classifier_model name to its config so
	// buildClassifier can reject misconfigurations that would
	// otherwise crash the llama-cpp backend at request time. Optional
	// — when nil, the check is skipped (tests, embedded callers that
	// haven't wired the loader).
	ModelLookup ModelConfigLookup

	// Registry is the shared classifier cache. Both the OpenAI and
	// Anthropic routes pass the same registry so the admin stats
	// endpoint sees every live classifier. Nil falls back to a local
	// registry — tests that don't need cross-route stats use this.
	Registry *router.Registry

	// Evaluator renders the classifier model's chat template around
	// the routing system + user prompt. Optional — when nil, the
	// score classifier falls back to a built-in ChatML envelope,
	// which is correct for Arch-Router/Qwen but wrong for non-ChatML
	// routing models. Production wiring passes the app-wide
	// templates.Evaluator so any model the operator points at gets
	// its own chat template applied.
	Evaluator *templates.Evaluator

	// TokenCounter binds the classifier model's tokenizer for the score
	// classifier's token-trim path. Optional; nil falls back to the
	// backend's n_ctx guard. Plain func type so core/application supplies
	// it as a method value without importing this package.
	TokenCounter func(modelName string) func(text string) (int, error)
}

// NewClassifierDeps assembles the full classifier dependency set from
// the application container. Every entry point that runs the router —
// OpenAI chat, Anthropic messages, realtime, the decision oracle, the
// corpus API — builds its deps here, so a new ClassifierDeps field is
// wired once instead of hand-copied per call site (where a missed site
// silently degrades that path rather than failing).
func NewClassifierDeps(app *application.Application) ClassifierDeps {
	return ClassifierDeps{
		Scorer:              app.Scorer,
		Corpus:              app.RouterCorpus(),
		TokenCounter:        app.TokenCounter,
		Embedder:            app.Embedder,
		EmbedderFingerprint: app.EmbedderFingerprint,
		VectorStore:         app.VectorStore,
		Reranker:            app.Reranker,
		ModelLookup:         app.ModelConfigLookup(),
		Registry:            app.RouterClassifierRegistry(),
		Evaluator:           app.TemplatesEvaluator(),
	}
}

// ProbeExtractor pulls the prompt content out of a parsed request so
// the classifier can inspect it without taking a dependency on the
// schema package. One extractor per request shape — wired by the
// route registration site (mirrors the piiadapter pattern).
//
// Returns ok=false when the parsed value isn't the expected type — the
// middleware then passes through without engaging the router.
type ProbeExtractor func(parsed any) (router.Probe, bool)

// RouteModel runs after SetModelAndConfig and the schema-specific
// SetXRequest, looks at the resolved model's Router config, and (when
// present) reclassifies the request to one of the candidates.
//
// The middleware:
//
//  1. Loads MODEL_CONFIG from the echo context. If nil or HasRouter()
//     is false, passes through.
//  2. Extracts the probe via the supplied ProbeExtractor.
//  3. Invokes the classifier matching cfg.Router.Classifier
//     ("score" or "colbert"). If the classifier can't be built —
//     missing classifier_model, misconfigured policies, etc. — the
//     request fails with 503. cfg.Router.Fallback only catches
//     Classify-time errors and label-coverage misses, not config
//     bugs that would otherwise be silent.
//  4. Resolves the chosen candidate to its model name. Reloads the
//     ModelConfig for that model and asserts depth-1 (the candidate
//     must NOT itself have a Router). Violation returns 500 — config
//     bug, not a request bug.
//  5. Updates input.Model in place, replaces MODEL_CONFIG with the
//     candidate's config, and stamps RequestedModel/ServedModel on the
//     context so UsageMiddleware records the routing.
//  6. Writes a DecisionRecord to the store for the admin page.
//
// store may be nil when --disable-stats turns off the routing log;
// classification still runs.
//
// Composition with SmartRouter (distributed mode): this middleware
// only does *model* selection. Node selection still happens in
// SmartRouter.Route() downstream of this middleware.
// RouteModel wires the router middleware. source is the value written to
// DecisionRecord.Source (router.SourceChat / SourceAnthropic / ...) so
// the admin page can split decisions by entry point. Pass
// router.SourceChat for the OpenAI chat endpoint, router.SourceAnthropic
// for the Anthropic messages endpoint.
func RouteModel(loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig, store router.DecisionStore, fallbackUser *auth.User, extractor ProbeExtractor, source string, deps ClassifierDeps) echo.MiddlewareFunc {
	registry := deps.Registry
	if registry == nil {
		registry = router.NewRegistry()
	}
	candidateLoader := func(name string) (*config.ModelConfig, error) {
		return loader.LoadModelConfigFileByNameDefaultOptions(name, appConfig)
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			cfg, ok := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
			if !ok || cfg == nil || !cfg.HasRouter() {
				return next(c)
			}

			parsed := c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST)
			if parsed == nil {
				return next(c)
			}

			probe, probeOK := extractor(parsed)
			if !probeOK {
				return next(c)
			}

			classifier, err := GetOrBuildClassifier(registry, cfg, deps)
			if err != nil {
				// Build-time failures are config bugs (missing
				// classifier_model, undeclared usecase, policy
				// validation, ...). Silently falling back would hide
				// them and make the router look "working" while the
				// classifier model is never invoked — surface as 503
				// with the underlying reason so operators see it.
				xlog.Warn("router: classifier build failed",
					"router_model", cfg.Name, "classifier", cfg.Router.Classifier, "error", err)
				return echo.NewHTTPError(503, "router classifier unavailable: "+err.Error())
			}

			result, err := router.Resolve(c.Request().Context(), cfg, classifier, candidateLoader, probe)
			if err != nil {
				xlog.Warn("router: resolve failed", "router_model", cfg.Name, "error", err)
				return echo.NewHTTPError(500, err.Error())
			}

			if req, ok := parsed.(schema.LocalAIRequest); ok {
				chosen := result.ChosenModel
				req.ModelName(&chosen)
			}

			c.Set(CONTEXT_LOCALS_KEY_MODEL_CONFIG, result.ChosenConfig)
			// Preserve an upstream requested model (e.g. an alias that points
			// at this router model) so accounting keeps the name the client
			// actually sent. Served always reflects the final candidate.
			if c.Get(ContextKeyRequestedModel) == nil {
				c.Set(ContextKeyRequestedModel, result.RouterModel)
			}
			c.Set(ContextKeyServedModel, result.ChosenModel)

			if store != nil {
				recordHTTPDecision(c, store, result, fallbackUser, source)
			}
			return next(c)
		}
	}
}

// recordHTTPDecision writes the resolved decision to the store with
// HTTP-shaped audit metadata (correlation id from header, user from
// auth middleware, fallback to the synthetic local user). Realtime
// has its own recorder that supplies session-derived metadata
// instead.
func recordHTTPDecision(c echo.Context, store router.DecisionStore, result *router.ResolveResult, fallbackUser *auth.User, source string) {
	correlationID, _ := c.Get(ContextKeyCorrelationID).(string)
	if correlationID == "" {
		correlationID = c.Response().Header().Get("X-Correlation-ID")
	}
	userID := ""
	if u := auth.GetUser(c); u != nil {
		userID = u.ID
	} else if fallbackUser != nil {
		userID = fallbackUser.ID
	}
	_ = store.Record(context.Background(), result.ToDecisionRecord(newDecisionID(), correlationID, userID, source))
}

// GetOrBuildClassifier looks up a built Classifier for the named router
// model in the registry and builds it on miss. Exported so the
// /api/router/decide decision-oracle endpoint can share the same
// build-once cache that the in-band RouteModel middleware uses.
func GetOrBuildClassifier(registry *router.Registry, cfg *config.ModelConfig, deps ClassifierDeps) (router.Classifier, error) {
	// Fingerprint folds the classifier model's renderer-affecting
	// fields (chat templates + stopwords) in alongside the router
	// config. Without this, hot-reloading the classifier model's
	// YAML (via ReloadModelsEndpoint, /import-model, or the MCP
	// reload_models tool) wouldn't rebuild the cached classifier —
	// the candidates slice and renderer closure are baked at build
	// time from those fields and would silently keep the stale
	// stop token / template until process restart.
	var classifierCfg *config.ModelConfig
	if deps.ModelLookup != nil {
		classifierCfg = deps.ModelLookup(cfg.Router.ClassifierModel)
	}
	embeddingFingerprint, err := resolvedKNNEmbeddingFingerprint(cfg, deps)
	if err != nil {
		return nil, err
	}
	fp := routerConfigFingerprint(cfg.Router, classifierCfg, embeddingFingerprint)
	if cached, ok := registry.Get(cfg.Name, fp); ok {
		return cached, nil
	}
	c, err := buildClassifier(cfg, deps)
	if err != nil {
		return nil, err
	}
	registry.Put(cfg.Name, fp, c)
	return c, nil
}

// routerConfigFingerprint is a stable cache key for the (router cfg,
// classifier model cfg) tuple. FNV-64 over the YAML form of the
// router block plus the renderer-affecting fields of the classifier
// model — equality-only, not cryptographic. YAML-marshal picks up
// any future RouterConfig field without this function needing to be
// touched; for the classifier model we hash a narrow projection so
// unrelated changes (parameters, files, ...) don't burst the cache.
// Pass classifierCfg=nil when no lookup is wired — the fingerprint
// degenerates to the router-only form, matching pre-refactor behaviour.
func routerConfigFingerprint(rc config.RouterConfig, classifierCfg *config.ModelConfig, additionalFingerprints ...string) uint64 {
	bytes, err := yaml.Marshal(rc)
	if err != nil {
		// Marshalling a value type can't fail in practice; fall
		// back to a hash that varies per call so we don't quietly
		// share a cache entry across distinct configs.
		return uint64(time.Now().UnixNano())
	}
	h := fnv.New64a()
	h.Write(bytes)
	if classifierCfg != nil {
		// Narrow projection: only the fields buildClassifier reads (renderer,
		// stop tokens, context_size → MaxContextTokens). Hashing the whole
		// ModelConfig would invalidate the cache on irrelevant changes;
		// omitting context_size would let a reload leave a stale token budget.
		h.Write([]byte{0}) // separator so empty fields don't collide
		h.Write([]byte(classifierCfg.TemplateConfig.Chat))
		h.Write([]byte{0})
		h.Write([]byte(classifierCfg.TemplateConfig.ChatMessage))
		h.Write([]byte{0})
		for _, sw := range classifierCfg.StopWords {
			h.Write([]byte(sw))
			h.Write([]byte{0})
		}
		h.Write([]byte{0})
		if classifierCfg.ContextSize != nil {
			h.Write([]byte(strconv.Itoa(*classifierCfg.ContextSize)))
		}
	}
	for _, fingerprint := range additionalFingerprints {
		h.Write([]byte{0})
		h.Write([]byte(fingerprint))
	}
	return h.Sum64()
}

func resolvedKNNEmbeddingFingerprint(cfg *config.ModelConfig, deps ClassifierDeps) (string, error) {
	name := cfg.Router.Classifier
	if name == "" {
		name = router.ClassifierScore
	}
	if name != router.ClassifierKNN || cfg.Router.KNN == nil || deps.EmbedderFingerprint == nil {
		return "", nil
	}
	modelFingerprint, err := deps.EmbedderFingerprint(cfg.Router.KNN.EmbeddingModel)
	if err != nil {
		return "", fmt.Errorf("router classifier knn: fingerprint embedding_model %q: %w", cfg.Router.KNN.EmbeddingModel, err)
	}
	if modelFingerprint == "" {
		return "", fmt.Errorf("router classifier knn: embedding_model %q returned an empty fingerprint", cfg.Router.KNN.EmbeddingModel)
	}
	return cfg.Router.KNN.ResolvedEmbeddingFingerprint(modelFingerprint), nil
}

func buildClassifier(cfg *config.ModelConfig, deps ClassifierDeps) (router.Classifier, error) {
	rc := cfg.Router
	name := rc.Classifier
	if name == "" {
		name = router.ClassifierScore
	}
	policies, err := validateRouterPolicies(name, rc)
	if err != nil {
		return nil, err
	}
	cacheCap := rc.ClassifierCacheSize
	if cacheCap == 0 {
		cacheCap = 1024
	}

	var inner router.Classifier
	switch name {
	case router.ClassifierScore:
		if rc.ClassifierModel == "" {
			return nil, fmt.Errorf("router classifier score requires classifier_model")
		}
		if deps.Scorer == nil {
			return nil, fmt.Errorf("router classifier score unavailable: no scorer factory wired")
		}
		if err := assertClassifierDeclaresScore(rc.ClassifierModel, deps.ModelLookup); err != nil {
			return nil, err
		}
		scorer := deps.Scorer(rc.ClassifierModel)
		if scorer == nil {
			return nil, fmt.Errorf("router classifier score: classifier_model %q not loadable", rc.ClassifierModel)
		}
		opts := router.ScoreClassifierOptions{
			CacheCap:             cacheCap,
			ActivationThreshold:  rc.ActivationThreshold,
			Normalization:        rc.ScoreNormalization,
			SystemPromptTemplate: rc.ClassifierSystemTemplate,
		}
		// Build the prompt renderer + stop token from the classifier
		// model's own config when available. Without ModelLookup
		// (tests, embedded callers) the score classifier's built-in
		// ChatML defaults kick in, which is correct for Arch-Router.
		if deps.ModelLookup != nil {
			if classifierCfg := deps.ModelLookup(rc.ClassifierModel); classifierCfg != nil {
				if deps.Evaluator != nil {
					// The router renders the scoring prompt client-side, so the
					// classifier model MUST carry a chat template — refusing
					// here beats silently falling back to a generic ChatML
					// envelope the model may not have been trained on.
					renderer := newTemplateRenderer(deps.Evaluator, classifierCfg)
					if renderer == nil {
						return nil, fmt.Errorf(
							"router classifier score: classifier_model %q has no chat template "+
								"(set template.chat and template.chat_message in its config). The router "+
								"renders the scoring prompt with the classifier model's own template; "+
								"without it the prompt format would not match the model",
							rc.ClassifierModel)
					}
					opts.PromptRenderer = renderer
				}
				if st := pickAssistantTurnEnd(classifierCfg.StopWords, classifierCfg.TemplateConfig.ChatMessage); st != "" {
					opts.StopToken = st
				}
				// Token-exact conversation trim — score classifier drops the
				// oldest turns using the model's own tokenizer.
				if count, ctxTokens := modelTokenTrim(rc.ClassifierModel, deps); count != nil {
					opts.TokenCounter = count
					opts.MaxContextTokens = ctxTokens
				}
			}
		}
		inner = router.NewScoreClassifier(policies, scorer, opts)
	case router.ClassifierColbert:
		if rc.ClassifierModel == "" {
			return nil, fmt.Errorf("router classifier colbert requires classifier_model")
		}
		if deps.Reranker == nil {
			return nil, fmt.Errorf("router classifier colbert unavailable: no reranker factory wired")
		}
		reranker := deps.Reranker(rc.ClassifierModel)
		if reranker == nil {
			return nil, fmt.Errorf("router classifier colbert: classifier_model %q not loadable", rc.ClassifierModel)
		}
		rerankClassifier := router.NewRerankClassifier(policies, reranker, cacheCap, rc.ActivationThreshold)
		if count, ctxTokens := modelTokenTrim(rc.ClassifierModel, deps); count != nil {
			rerankClassifier = rerankClassifier.WithTokenTrim(count, ctxTokens)
		}
		inner = rerankClassifier
	case router.ClassifierKNN:
		if rc.KNN == nil || rc.KNN.EmbeddingModel == "" {
			return nil, fmt.Errorf("router classifier knn requires a knn block with embedding_model")
		}
		if deps.Embedder == nil || deps.VectorStore == nil {
			return nil, fmt.Errorf("router classifier knn unavailable: embedder/vector-store factories not wired")
		}
		embeddingFingerprint, err := resolvedKNNEmbeddingFingerprint(cfg, deps)
		if err != nil {
			return nil, err
		}
		if deps.Corpus != nil && embeddingFingerprint == "" {
			return nil, fmt.Errorf("router classifier knn unavailable: embedder fingerprint factory not wired")
		}
		embedder := deps.Embedder(rc.KNN.EmbeddingModel)
		if embedder == nil {
			return nil, fmt.Errorf("router classifier knn: embedding_model %q not loadable", rc.KNN.EmbeddingModel)
		}
		storeName := rc.KNN.ResolvedStoreName(cfg.Name)
		vstore := deps.VectorStore(storeName)
		if vstore == nil {
			return nil, fmt.Errorf("router classifier knn: vector store %q not loadable", storeName)
		}
		if deps.Corpus != nil {
			// Loading fails closed: a live index from a different embedding
			// space may have the same vector width and return plausible but
			// incorrect routes.
			if n, err := deps.Corpus.EnsureLoaded(context.Background(), storeName, rc.KNN.EmbeddingModel, embeddingFingerprint, embedder, vstore); err != nil {
				return nil, fmt.Errorf("router classifier knn: load corpus %q: %w", storeName, err)
			} else if n > 0 {
				xlog.Info("router: knn corpus loaded",
					"router_model", cfg.Name, "store", storeName, "entries", n)
			}
		}
		knnClassifier := router.NewKNNClassifier(embedder, vstore, router.KNNClassifierOptions{
			K:                   rc.KNN.K,
			SimilarityThreshold: rc.KNN.SimilarityThreshold,
			VoteThreshold:       rc.KNN.VoteThreshold,
		})
		if count, ctxTokens := modelTokenTrim(rc.KNN.EmbeddingModel, deps); count != nil {
			knnClassifier = knnClassifier.WithTokenTrim(count, ctxTokens)
		}
		if rc.EmbeddingCache != nil {
			// The knn classifier IS an embedding-KNN lookup — wrapping it
			// in the embedding cache would embed every probe twice to
			// answer the same question. Ignore the block rather than fail
			// routing. Returning from the arm (instead of name-checking in
			// the shared wrap below) keeps the opt-out local: the next
			// embedding-based classifier makes its own wrapping decision.
			xlog.Warn("router: embedding_cache ignored for knn classifier",
				"router_model", cfg.Name)
		}
		return knnClassifier, nil
	default:
		return nil, fmt.Errorf("router: unknown classifier %q (supported: %s)", name, strings.Join(router.AllClassifiers, ", "))
	}

	if rc.EmbeddingCache == nil {
		return inner, nil
	}
	wrapped, err := wrapWithEmbeddingCache(cfg, inner, deps)
	if err != nil {
		// Caching plumbing problems must not break routing — log,
		// drop the cache layer, and return the uncached classifier.
		// The admin UI surfaces the warning via the classifier-build
		// error path used elsewhere.
		xlog.Warn("router: embedding cache disabled",
			"router_model", cfg.Name, "error", err)
		return inner, nil
	}
	return wrapped, nil
}

// assertClassifierDeclaresScore refuses to build the score classifier
// unless classifier_model's config declares FLAG_SCORE. This check only
// refuses to bind a model that never declared itself for Score in the
// first place; that model could be a misconfigured chat model the
// operator pointed at by accident.
//
// When lookup is nil (test wiring) the check is skipped.
func assertClassifierDeclaresScore(classifierModel string, lookup ModelConfigLookup) error {
	if lookup == nil {
		return nil
	}
	cfg := lookup(classifierModel)
	if cfg == nil {
		// Unknown model — Scorer() will produce a clearer "not
		// loadable" error a few lines down.
		return nil
	}
	if !cfg.HasUsecases(config.FLAG_SCORE) {
		return fmt.Errorf(
			"router classifier score: classifier_model %q does not declare the "+
				"score usecase. Add `known_usecases: [score]` (alongside any other "+
				"usecases the model serves) to its config",
			classifierModel)
	}
	return nil
}

// validateRouterPolicies checks the invariants every classifier relies
// on (non-empty policies, every candidate label declared as a policy,
// every candidate has a model + at least one label) and returns the
// parsed []ScorePolicy. Per-classifier requirements (classifier_model,
// knn block, ...) live in the buildClassifier arms, not here.
func validateRouterPolicies(classifierName string, rc config.RouterConfig) ([]router.ScorePolicy, error) {
	if len(rc.Policies) == 0 {
		return nil, fmt.Errorf("router classifier %s requires at least one policy", classifierName)
	}
	policies := make([]router.ScorePolicy, 0, len(rc.Policies))
	for _, p := range rc.Policies {
		if p.Label == "" {
			return nil, fmt.Errorf("router classifier %s: policy with empty label", classifierName)
		}
		if p.Description == "" {
			return nil, fmt.Errorf("router classifier %s: policy %q has no description", classifierName, p.Label)
		}
		policies = append(policies, router.ScorePolicy{Label: p.Label, Description: p.Description})
	}
	policyLabels := make(map[string]struct{}, len(policies))
	for _, p := range policies {
		policyLabels[p.Label] = struct{}{}
	}
	for _, c := range rc.Candidates {
		if c.Model == "" {
			return nil, fmt.Errorf("router classifier %s: candidate has empty model field", classifierName)
		}
		if len(c.Labels) == 0 {
			return nil, fmt.Errorf("router classifier %s: candidate %q has no labels", classifierName, c.Model)
		}
		for _, l := range c.Labels {
			if _, ok := policyLabels[l]; !ok {
				return nil, fmt.Errorf("router classifier %s: candidate %q references unknown label %q (not in policies)", classifierName, c.Model, l)
			}
		}
	}
	return policies, nil
}

// newTemplateRenderer adapts the templates.Evaluator + the classifier
// model's config into the router.PromptRenderer callback. The
// resulting renderer pushes the routing system + user prompt through
// the classifier model's full chat-template pipeline — per-role
// formatting via TemplateConfig.ChatMessage, then the outer
// TemplateConfig.Chat — so non-ChatML routing models render
// correctly without router-package awareness of the template format.
//
// We must go through TemplateMessages, not EvaluateTemplateForPrompt
// directly: the gallery's outer Chat templates are uniformly
// `{{.Input -}}<|im_start|>assistant` (or the Llama-3 equivalent)
// and reference {{.Input}} only — never {{.SystemPrompt}}. Passing
// our routing system prompt through .SystemPrompt would silently
// drop it because Go text/template ignores unreferenced fields.
// TemplateMessages instead renders each role through ChatMessage and
// joins them into the .Input the outer template DOES read.
//
// Returns nil (forcing the score classifier's chatMLRenderer
// fallback) when either template piece is missing — partial
// templating would still drop content.
func newTemplateRenderer(eval *templates.Evaluator, classifierCfg *config.ModelConfig) router.PromptRenderer {
	if classifierCfg.TemplateConfig.Chat == "" || classifierCfg.TemplateConfig.ChatMessage == "" {
		return nil
	}
	cfgCopy := *classifierCfg
	return func(system, user string) (string, error) {
		messages := []schema.Message{
			{Role: "system", StringContent: system},
			{Role: "user", StringContent: user},
		}
		rendered := eval.TemplateMessages(schema.OpenAIRequest{}, messages, &cfgCopy, nil, false)
		if rendered == "" {
			return "", fmt.Errorf("router: classifier %q chat template produced empty output", cfgCopy.Name)
		}
		return rendered, nil
	}
}

// pickAssistantTurnEnd returns the classifier model's assistant
// turn-end token — the one to suffix candidates with so the model's
// "I'm done" signal folds into the per-candidate joint log-prob.
//
// Strategy: prefer the stopword that *literally appears* in the
// chat_message template, because that token is the assistant
// turn-end by construction. ChatML's chat_message ends with
// "<|im_end|>", Llama-3's ends with "<|eot_id|>", etc. — the
// template is the source of truth.
//
// Fallback: the first non-empty stopword. That's right for
// well-ordered configs (ChatML conventionally lists <|im_end|>
// first) but wrong for some gallery Llama-3 templates that defensively
// list <|im_end|> first even though the actual turn-end is <|eot_id|>.
// The template-scan above catches those.
//
// When no stopwords are configured at all, return "" — caller falls
// back to defaultStopToken (<|im_end|>) inside the score classifier.
func pickAssistantTurnEnd(words []string, chatMessageTemplate string) string {
	if chatMessageTemplate != "" {
		for _, w := range words {
			if w != "" && strings.Contains(chatMessageTemplate, w) {
				return w
			}
		}
	}
	for _, w := range words {
		if w != "" {
			return w
		}
	}
	return ""
}

func wrapWithEmbeddingCache(cfg *config.ModelConfig, inner router.Classifier, deps ClassifierDeps) (router.Classifier, error) {
	ec := cfg.Router.EmbeddingCache
	if ec.EmbeddingModel == "" {
		return nil, fmt.Errorf("embedding_cache requires embedding_model")
	}
	if deps.Embedder == nil || deps.VectorStore == nil {
		return nil, fmt.Errorf("embedding cache factories not wired")
	}
	embedder := deps.Embedder(ec.EmbeddingModel)
	if embedder == nil {
		return nil, fmt.Errorf("embedding_model %q not loadable", ec.EmbeddingModel)
	}
	storeName := ec.StoreName
	if storeName == "" {
		storeName = "router-cache-" + cfg.Name
	}
	vstore := deps.VectorStore(storeName)
	if vstore == nil {
		return nil, fmt.Errorf("vector store %q not loadable", storeName)
	}
	cache := router.NewEmbeddingCacheClassifier(inner, embedder, vstore, ec.SimilarityThreshold, ec.ConfidenceThreshold)
	// Trim the probe to the embedder model's own context (e.g. nomic-embed at
	// 8k) rather than a fixed guess — otherwise the cache key is an embedding
	// of a silently-truncated conversation.
	if count, ctxTokens := modelTokenTrim(ec.EmbeddingModel, deps); count != nil {
		cache = cache.WithTokenTrim(count, ctxTokens)
	}
	return cache, nil
}

// modelTokenTrim returns a model's own tokenizer and the token ceiling its
// probe must fit, or (nil, 0) when no tokenizer is available (only then can we
// not trim exactly). The ceiling is min(effective context, effective batch):
// score/embed/rerank all decode the whole prompt in one pass, so it must fit
// both the context window and a single batch. Using the backend's *effective*
// values — not the raw config fields — means trimming still works when
// context_size and batch are unset; otherwise a non-trivial prompt overflows
// the default window and every classification fails.
func modelTokenTrim(modelName string, deps ClassifierDeps) (func(string) (int, error), int) {
	if deps.TokenCounter == nil || deps.ModelLookup == nil {
		return nil, 0
	}
	cfg := deps.ModelLookup(modelName)
	if cfg == nil {
		return nil, 0
	}
	count := deps.TokenCounter(modelName)
	if count == nil {
		return nil, 0
	}
	ceiling := backend.EffectiveContextSize(*cfg)
	if b := backend.EffectiveBatchSize(*cfg); b < ceiling {
		ceiling = b
	}
	return count, ceiling
}

func newDecisionID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "rd_" + hex.EncodeToString(b[:])
}

// OpenAIProbe extracts a router.Probe from a parsed *schema.OpenAIRequest.
// Concatenates message contents (string-form or text blocks of the
// structured `[]any` content) so the classifier sees a single corpus
// for length and content-shape rules. Image blocks are skipped — a
// future multimodal classifier can take a different route.
func OpenAIProbe(parsed any) (router.Probe, bool) {
	req, ok := parsed.(*schema.OpenAIRequest)
	if !ok || req == nil {
		return router.Probe{}, false
	}
	return OpenAIProbeFromRequest(req), true
}

// messageText flattens a chat message's Content to plain text: string content
// verbatim; []any structured content contributes only its "text" blocks.
func messageText(content any) string {
	switch ct := content.(type) {
	case string:
		return ct
	case []any:
		var b strings.Builder
		for _, block := range ct {
			if bm, ok := block.(map[string]any); ok && bm["type"] == "text" {
				if t, ok := bm["text"].(string); ok {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(t)
				}
			}
		}
		return b.String()
	}
	return ""
}

// messageProbeParts drops empty (e.g. image-only) messages so they don't
// consume budget or emit blank lines.
func messageProbeParts(texts []string) []string {
	parts := make([]string, 0, len(texts))
	for _, t := range texts {
		if t != "" {
			parts = append(parts, t)
		}
	}
	return parts
}

// OpenAIProbeFromRequest is the typed counterpart of OpenAIProbe — same
// extraction logic, but takes the request struct directly. Realtime and
// other non-HTTP callers use it to feed a probe to router.Resolve
// without going through an echo.Context first.
func OpenAIProbeFromRequest(req *schema.OpenAIRequest) router.Probe {
	if req == nil {
		return router.Probe{}
	}
	texts := make([]string, len(req.Messages))
	for i := range req.Messages {
		texts[i] = messageText(req.Messages[i].Content)
	}
	parts := messageProbeParts(texts)
	// Prompt carries the full conversation; each classifier trims it to its own
	// model's context (see modelTokenTrim). Messages preserves the per-turn
	// split the trimmer drops oldest-first.
	return router.Probe{Prompt: router.JoinTurns(parts), Messages: parts}
}

// AnthropicProbe is the AnthropicRequest analogue of OpenAIProbe.
func AnthropicProbe(parsed any) (router.Probe, bool) {
	req, ok := parsed.(*schema.AnthropicRequest)
	if !ok || req == nil {
		return router.Probe{}, false
	}
	texts := make([]string, len(req.Messages))
	for i := range req.Messages {
		texts[i] = messageText(req.Messages[i].Content)
	}
	parts := messageProbeParts(texts)
	return router.Probe{Prompt: router.JoinTurns(parts), Messages: parts}, true
}
