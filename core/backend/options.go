package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	"github.com/mudler/LocalAI/pkg/downloader"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/vram"
	"github.com/mudler/xlog"
)

// ModelLoadTraceObserver returns the ModelLoader load observer that records
// a model_load backend trace for every successful real load (backend process
// spawn + LoadModel RPC; cache hits never reach the observer). Failures are
// deliberately skipped here: the modality wrappers already record them via
// recordModelLoadFailure with request context, and the backend auto-discovery
// scan probes several backends before one succeeds — tracing every probe
// failure would bury the buffer in noise.
//
// The traced data includes the resolved backend runtime (the installed
// backend's launcher path, which names the variant directory) — that is what
// identifies WHICH build served the load. A stale installed backend is
// invisible in the model config but obvious here.
func ModelLoadTraceObserver(appConfig *config.ApplicationConfig) func(model.BackendLoadEvent) {
	return func(ev model.BackendLoadEvent) {
		if ev.Err != nil || !appConfig.EnableTracing {
			return
		}
		trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
		trace.RecordBackendTrace(trace.BackendTrace{
			Timestamp: time.Now(),
			Duration:  ev.Duration,
			Type:      trace.BackendTraceModelLoad,
			ModelName: ev.ModelID,
			Backend:   ev.Backend,
			Summary:   "Model loaded",
			Data: map[string]any{
				"model_file":      ev.ModelName,
				"backend_runtime": ev.BackendURI,
			},
		})
	}
}

// PreloadModel warms a model into memory without running any inference, so the
// first real request doesn't pay the backend's cold-start load cost. It uses
// the same ModelOptions + ml.Load path the modality functions use, so a
// subsequent inference call hits the loader cache instead of reloading. Load
// failures are recorded and returned; callers that warm models opportunistically
// (e.g. realtime session warm-up) typically log and continue, since the lazy
// path will retry on first use.
func PreloadModel(ctx context.Context, ml *model.ModelLoader, modelConfig config.ModelConfig, appConfig *config.ApplicationConfig) error {
	opts := ModelOptions(modelConfig, appConfig, model.WithContext(ctx))
	if _, err := ml.Load(opts...); err != nil {
		recordModelLoadFailure(appConfig, modelConfig.Name, modelConfig.Backend, err, nil)
		return err
	}
	return nil
}

// recordModelLoadFailure records a backend trace when model loading fails.
func recordModelLoadFailure(appConfig *config.ApplicationConfig, modelName, backend string, err error, data map[string]any) {
	if !appConfig.EnableTracing {
		return
	}
	trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems, appConfig.TracingMaxBodyBytes)
	trace.RecordBackendTrace(trace.BackendTrace{
		Timestamp: time.Now(),
		Type:      trace.BackendTraceModelLoad,
		ModelName: modelName,
		Backend:   backend,
		Summary:   "Model load failed",
		Error:     err.Error(),
		Data:      data,
	})
}

// estimateModelSizeBytes uses the unified EstimateModel entry point to compute
// the total weight-file size for a model config.  It collects all weight files
// from DownloadFiles, Model, and MMProj, and also extracts the HuggingFace
// repo ID so EstimateModel can fall back to the HF API when local file
// metadata is unavailable (e.g. not-yet-downloaded models).
func estimateModelSizeBytes(c config.ModelConfig, modelsPath string) int64 {
	seen := make(map[string]bool)
	input := vram.ModelEstimateInput{}
	managedPrimary := len(c.Artifacts) > 0 && c.Artifacts[0].Resolved != nil

	addFile := func(uri string, size int64) {
		if !vram.IsWeightFile(uri) {
			return
		}
		resolved := uri
		if !strings.Contains(uri, "://") {
			resolved = "file://" + filepath.Join(modelsPath, uri)
		}
		if seen[resolved] {
			return
		}
		seen[resolved] = true
		input.Files = append(input.Files, vram.FileInput{URI: resolved, Size: size})
	}

	// tryHFRepo resolves any huggingface:// or hf:// URI to an HTTPS URL and
	// then extracts the org/model repo ID for use as the HF fallback path.
	tryHFRepo := func(uri string) {
		if input.HFRepo != "" {
			return
		}
		resolved := downloader.URI(uri).ResolveURL()
		if repoID, ok := vram.ExtractHFRepoID(resolved); ok {
			input.HFRepo = repoID
		}
	}

	for _, f := range c.DownloadFiles {
		uriStr := string(f.URI)
		addFile(uriStr, 0)
		if !managedPrimary {
			tryHFRepo(uriStr)
		}
	}
	if managedPrimary {
		// The snapshot directory is derived from the cache key, not from
		// ModelFileName(): for a single-file artifact ModelFileName() resolves to
		// the file inside the snapshot, whereas the manifest and every artifact
		// file live relative to the snapshot directory itself.
		if snapshotDir, err := modelartifacts.RelativeSnapshotPath(c.Artifacts[0].Resolved.CacheKey); err == nil {
			manifest, err := modelartifacts.ReadManifest(filepath.Join(modelsPath, filepath.Dir(snapshotDir), "manifest.json"))
			if err == nil {
				for _, file := range manifest.Files {
					addFile(filepath.Join(snapshotDir, filepath.FromSlash(file.Path)), file.Size)
				}
			}
		}
	} else {
		addFile(c.Model, 0)
		tryHFRepo(c.Model)
	}
	if c.MMProj != "" {
		addFile(c.MMProj, 0)
	}

	if len(input.Files) == 0 && input.HFRepo == "" {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := vram.EstimateModelMultiContext(ctx, input, nil)
	if err != nil || result.SizeBytes == 0 {
		return 0
	}
	return int64(result.SizeBytes)
}

func ModelOptions(c config.ModelConfig, so *config.ApplicationConfig, opts ...model.Option) []model.Option {
	defOpts := []model.Option{
		model.WithBackendString(c.Backend),
		model.WithModel(c.Model),
		model.WithContext(so.Context),
		model.WithModelID(c.ModelID()),
	}
	managedPrimary := len(c.Artifacts) > 0 && c.Artifacts[0].Resolved != nil
	if managedPrimary {
		defOpts = append(defOpts, model.WithModelFile(c.ModelFileName()))
	}

	threads := 1

	if c.Threads != nil {
		threads = *c.Threads
	}

	if so.Threads != 0 {
		threads = so.Threads
	}

	c.Threads = &threads

	grpcOpts := grpcModelOpts(c, so.SystemState.Model.ModelsPath)
	defOpts = append(defOpts, model.WithLoadGRPCLoadModelOpts(grpcOpts))

	defOpts = append(defOpts, model.EnableParallelRequests)

	if c.GRPC.Attempts != 0 {
		defOpts = append(defOpts, model.WithGRPCAttempts(c.GRPC.Attempts))
	}

	if c.GRPC.AttemptsSleepTime != 0 {
		defOpts = append(defOpts, model.WithGRPCAttemptsDelay(c.GRPC.AttemptsSleepTime))
	}

	for k, v := range so.ExternalGRPCBackends {
		defOpts = append(defOpts, model.WithExternalBackend(k, v))
	}

	if sizeBytes := estimateModelSizeBytes(c, so.SystemState.Model.ModelsPath); sizeBytes > 0 {
		defOpts = append(defOpts, model.WithModelSizeBytes(sizeBytes))
	}

	return append(defOpts, opts...)
}

func getSeed(c config.ModelConfig) int32 {
	var seed int32 = config.RAND_SEED

	if c.Seed != nil {
		seed = int32(*c.Seed)
	}

	if seed == config.RAND_SEED {
		seed = rand.Int32()
	}

	return seed
}

// DefaultContextSize and DefaultBatchSize are the backend's fallbacks when a
// model config leaves them unset. Exported so callers that must respect the
// effective decode window — notably the router's prompt trimmer — resolve the
// same numbers grpcModelOpts does instead of guessing. The values are owned by
// core/config (single source of truth shared with the config default tiers).
const (
	DefaultContextSize = config.DefaultContextSize
	DefaultBatchSize   = config.DefaultPhysicalBatch
)

// EffectiveContextSize is the context window the backend will run with: the
// configured value, or DefaultContextSize when unset. A negative value (the
// context_size: -1 auto-max sentinel) that survived config resolution, e.g. on
// a backend that never ran the GGUF resolver, is clamped here so a negative
// n_ctx never reaches a backend.
func EffectiveContextSize(c config.ModelConfig) int {
	if c.ContextSize != nil && *c.ContextSize > 0 {
		return *c.ContextSize
	}
	return DefaultContextSize
}

// localGPU resolves the device that will run the model, for single-pass batch
// sizing. It is a package var so tests inject a deterministic device; production
// reads config.LocalGPU, whose detection is sync.Once-cached in xsysinfo — so the
// per-request call from the router's prompt trimmer (modelTokenTrim) stays cheap.
var localGPU = config.LocalGPU

// EffectiveBatchSize is the single-decode batch the backend will run with.
// Score, embedding and rerank all process the whole input in one pass: score
// decodes prompt+candidate (asserts n_tokens <= n_batch), and embedding/rerank
// pool over the full sequence in one physical batch (n_ubatch). Ideally the batch
// covers the whole context so any input that fits the context fits one pass,
// avoiding both the GGML_ASSERT crash and the "input is too large to process"
// error — BUT a full ctx-sized n_ubatch makes the per-device CUDA compute buffer
// multi-GiB (it scales ~ n_ubatch * n_ctx and can't be split across GPUs), so a
// large-context embedding model aborts on load with free VRAM to spare (#10485).
// So we cap the batch to the largest that fits the per-device VRAM headroom; an
// input longer than that cap is the accepted tradeoff (it can't be pooled in one
// pass, but the load no longer OOMs). Explicit `batch:` always wins.
func EffectiveBatchSize(c config.ModelConfig) int {
	if c.Batch != 0 {
		return c.Batch
	}
	singlePass := c.HasUsecases(config.FLAG_SCORE) ||
		c.HasUsecases(config.FLAG_EMBEDDINGS) ||
		c.HasUsecases(config.FLAG_RERANK)
	if ctx := EffectiveContextSize(c); singlePass && ctx > DefaultBatchSize {
		return config.SinglePassBatchForContext(localGPU(), ctx)
	}
	return DefaultBatchSize
}

// withCompanionArtifactOptions surfaces each resolved companion snapshot to the
// backend as "<artifact name>:<snapshot path>", reusing the key:value option
// convention backends already parse.
//
// The value is deliberately relative to the models directory and deliberately
// not persisted to the config YAML. It is derived from a content-addressed cache
// key that only exists after the artifact resolves, so a static gallery override
// could not carry it, and a persisted copy would rot the moment a re-resolve
// produced a new key. Staying relative also lets a remote worker resolve it
// under its own ModelPath after staging rewrites the model root.
//
// An option the author set explicitly always wins: pinning a companion to a
// local checkout has to beat the managed snapshot.
//
// A companion that is declared but NOT resolved falls back to its source
// repository id rather than being dropped: a dropped companion is invisible to
// the backend, which then loads its own hardcoded default and fails far away
// from the cause. The repo-id fallback trades the staging fast path (the weights
// are fetched on the worker) for correctness, and logs a warning so the missing
// controller-side resolution is diagnosable.
func withCompanionArtifactOptions(options []string, artifacts []modelartifacts.Spec) []string {
	configured := make(map[string]struct{}, len(options))
	for _, option := range options {
		if name, _, found := strings.Cut(option, ":"); found {
			configured[name] = struct{}{}
		}
	}

	// Copy before appending: opts.Options would otherwise share (and could
	// reallocate away from) the config's own slice.
	combined := slices.Clone(options)
	for _, artifact := range artifacts {
		if artifact.Target != modelartifacts.TargetCompanion {
			continue
		}
		if _, exists := configured[artifact.Name]; exists {
			xlog.Debug("keeping the configured companion option over the managed snapshot", "artifact", artifact.Name)
			continue
		}

		// Preferred fast path: a resolved companion is surfaced as its staged,
		// models-relative snapshot directory. Staging materializes exactly this
		// path on a remote worker and the backend resolves it under its own
		// ModelPath, so the weights are never fetched again at load time.
		if artifact.Resolved != nil {
			if snapshot, err := modelartifacts.RelativeSnapshotPath(artifact.Resolved.CacheKey); err == nil {
				xlog.Debug("surfacing resolved companion snapshot to the backend", "artifact", artifact.Name, "path", snapshot)
				combined = append(combined, artifact.Name+":"+snapshot)
				continue
			} else {
				xlog.Warn("companion artifact has an unusable cache key; falling back to its source repository", "artifact", artifact.Name, "error", err)
			}
		}

		// Fallback: the companion reached load time without a resolved snapshot
		// (its resolved state never made it into the config the loader is serving
		// from, e.g. after a controller restart or a peer-replica config reload).
		// Emitting nothing here is what makes the failure so hard to see: the
		// backend then falls back to its OWN hardcoded default companion, which on
		// a distributed longcat-video worker meant fetching the wrong base model
		// and failing "base_model must point to a LongCat-Video checkpoint". Name
		// the DECLARED repository instead, so the backend at least fetches the
		// artifact the config actually asked for. It is a warn because it means the
		// no-download fast path was lost: the controller-side materialization or
		// persistence for this companion needs investigating.
		if repo := strings.TrimSpace(artifact.Source.Repo); repo != "" {
			xlog.Warn("companion artifact is not resolved on the controller; the backend will fetch it by repository id (no staging fast path)",
				"artifact", artifact.Name, "repo", repo)
			combined = append(combined, artifact.Name+":"+repo)
			continue
		}
		xlog.Warn("companion artifact is neither resolved nor has a source repository; the backend will get no option for it", "artifact", artifact.Name)
	}
	return combined
}

func grpcModelOpts(c config.ModelConfig, modelPath string) *pb.ModelOptions {
	ctxSize := EffectiveContextSize(c)
	b := EffectiveBatchSize(c)

	flashAttention := config.DefaultFlashAttention

	if c.FlashAttention != nil {
		flashAttention = *c.FlashAttention
	}

	f16 := false
	if c.F16 != nil {
		f16 = *c.F16
	}

	embeddings := false
	if c.Embeddings != nil {
		embeddings = *c.Embeddings
	}

	lowVRAM := false
	if c.LowVRAM != nil {
		lowVRAM = *c.LowVRAM
	}

	reranking := false
	if c.Reranking != nil {
		reranking = *c.Reranking
	}

	mmap := false
	if c.MMap != nil {
		mmap = *c.MMap
	}

	// Intel SYCL backend has issues with mmap enabled
	// See: https://github.com/mudler/LocalAI/issues/9012
	// Automatically disable mmap for Intel SYCL backends
	if c.Backend != "" {
		if strings.Contains(strings.ToLower(c.Backend), "intel") || strings.Contains(strings.ToLower(c.Backend), "sycl") {
			mmap = false
			xlog.Info("Auto-disabling mmap for Intel SYCL backend", "backend", c.Backend)
		}
	}

	mmlock := false
	if c.MMlock != nil {
		mmlock = *c.MMlock
	}

	nGPULayers := config.DefaultNGPULayers
	if c.NGPULayers != nil {
		nGPULayers = *c.NGPULayers
	}

	triggers := make([]*pb.GrammarTrigger, 0)
	for _, t := range c.FunctionsConfig.GrammarConfig.GrammarTriggers {
		triggers = append(triggers, &pb.GrammarTrigger{
			Word: t.Word,
		})
	}

	engineArgsJSON := ""
	if len(c.EngineArgs) > 0 {
		buf, err := json.Marshal(c.EngineArgs)
		if err != nil {
			// ModelConfig.Validate() rejects unmarshalable engine_args at
			// config load, so reaching here means the validator was bypassed.
			// Silently dropping user-set options would change runtime behaviour
			// without warning — fail loud instead.
			panic(fmt.Sprintf("engine_args marshal failed for model %q: %v (Validate() should have caught this)", c.Model, err))
		}
		engineArgsJSON = string(buf)
	}

	opts := &pb.ModelOptions{
		CUDA:                 c.CUDA || c.Diffusers.CUDA,
		SchedulerType:        c.Diffusers.SchedulerType,
		GrammarTriggers:      triggers,
		PipelineType:         c.Diffusers.PipelineType,
		CFGScale:             c.CFGScale,
		LoraAdapter:          c.LoraAdapter,
		LoraScale:            c.LoraScale,
		LoraAdapters:         c.LoraAdapters,
		LoraScales:           c.LoraScales,
		F16Memory:            f16,
		LoraBase:             c.LoraBase,
		IMG2IMG:              c.Diffusers.IMG2IMG,
		CLIPModel:            c.Diffusers.ClipModel,
		CLIPSubfolder:        c.Diffusers.ClipSubFolder,
		Options:              withCompanionArtifactOptions(c.Options, c.Artifacts),
		Overrides:            c.Overrides,
		EngineArgs:           engineArgsJSON,
		CLIPSkip:             int32(c.Diffusers.ClipSkip),
		ControlNet:           c.Diffusers.ControlNet,
		ContextSize:          int32(ctxSize),
		Seed:                 getSeed(c),
		NBatch:               int32(b),
		NoMulMatQ:            c.NoMulMatQ,
		DraftModel:           c.DraftModel,
		AudioPath:            c.AudioPath,
		Quantization:         c.Quantization,
		LoadFormat:           c.LoadFormat,
		GPUMemoryUtilization: c.GPUMemoryUtilization,
		TrustRemoteCode:      c.TrustRemoteCode,
		EnforceEager:         c.EnforceEager,
		SwapSpace:            int32(c.SwapSpace),
		MaxModelLen:          int32(c.MaxModelLen),
		TensorParallelSize:   int32(c.TensorParallelSize),
		DisableLogStatus:     c.DisableLogStatus,
		DType:                c.DType,
		// LimitMMPerPrompt vLLM
		LimitImagePerPrompt: int32(c.LimitMMPerPrompt.LimitImagePerPrompt),
		LimitVideoPerPrompt: int32(c.LimitMMPerPrompt.LimitVideoPerPrompt),
		LimitAudioPerPrompt: int32(c.LimitMMPerPrompt.LimitAudioPerPrompt),
		FlashAttention:      flashAttention,
		CacheTypeKey:        c.CacheTypeK,
		CacheTypeValue:      c.CacheTypeV,
		NoKVOffload:         c.NoKVOffloading,
		YarnExtFactor:       c.YarnExtFactor,
		YarnAttnFactor:      c.YarnAttnFactor,
		YarnBetaFast:        c.YarnBetaFast,
		YarnBetaSlow:        c.YarnBetaSlow,
		NGQA:                c.NGQA,
		RMSNormEps:          c.RMSNormEps,
		MLock:               mmlock,
		RopeFreqBase:        c.RopeFreqBase,
		RopeScaling:         c.RopeScaling,
		Type:                c.ModelType,
		RopeFreqScale:       c.RopeFreqScale,
		NUMA:                c.NUMA,
		Embeddings:          embeddings,
		Reranking:           reranking,
		LowVRAM:             lowVRAM,
		NGPULayers:          int32(nGPULayers),
		MMap:                mmap,
		MainGPU:             c.MainGPU,
		Threads:             int32(*c.Threads),
		TensorSplit:         c.TensorSplit,
		// RWKV
		Tokenizer: c.Tokenizer,
	}

	if c.Backend == "cloud-proxy" {
		opts.Proxy = &pb.ProxyOptions{
			UpstreamUrl:           c.Proxy.UpstreamURL,
			Mode:                  c.Proxy.Mode,
			Provider:              c.Proxy.Provider,
			ApiKeyEnv:             c.Proxy.APIKeyEnv,
			ApiKeyFile:            c.Proxy.APIKeyFile,
			UpstreamModel:         c.Proxy.UpstreamModel,
			RequestTimeoutSeconds: int32(c.Proxy.RequestTimeoutSeconds),
		}
	}

	if c.MMProj != "" {
		opts.MMProj = filepath.Join(modelPath, c.MMProj)
	}

	// Resolve draft_model against the models directory, mirroring the
	// handling of parameters.model and mmproj. Always joining (without an
	// IsAbs shortcut) prevents user-supplied configs from pointing the
	// backend at arbitrary host files via an absolute path.
	if c.DraftModel != "" {
		opts.DraftModel = filepath.Join(modelPath, c.DraftModel)
	}

	return opts
}

func gRPCPredictOpts(c config.ModelConfig, modelPath string) *pb.PredictOptions {
	promptCachePath := ""
	if c.PromptCachePath != "" {
		p := filepath.Join(modelPath, c.PromptCachePath)
		err := os.MkdirAll(filepath.Dir(p), 0750)
		if err == nil {
			promptCachePath = p
		} else {
			xlog.Error("error creating prompt cache folder", "error", err, "promptCachePath", promptCachePath)
		}
	}

	// TopK may be nil after SetDefaults for backends that don't use llama.cpp's
	// top_k=40 default (issue #6632, e.g. mlx). proto3 int32 can't be unset, so
	// send 0 — the value mlx actually wants (top-k disabled).
	var topK int32
	if c.TopK != nil {
		topK = int32(*c.TopK)
	}

	pbOpts := &pb.PredictOptions{
		// c.Model, not c.ModelID()/c.ModelFileName(): this must be the SAME
		// expression ModelOptions feeds to model.WithModel, which is what the
		// backend receives as ModelOptions.Model at LoadModel time. Both are
		// read from this same config value, so the backend's equality check
		// cannot false-reject. See PredictOptions.ModelIdentity in
		// backend/backend.proto and #10952.
		ModelIdentity:       c.Model,
		Temperature:         float32(*c.Temperature),
		TopP:                float32(*c.TopP),
		NDraft:              c.NDraft,
		TopK:                topK,
		MinP:                float32(*c.MinP),
		Tokens:              int32(*c.Maxtokens),
		Threads:             int32(*c.Threads),
		PromptCacheAll:      *c.PromptCacheAll,
		PromptCacheRO:       c.PromptCacheRO,
		PromptCachePath:     promptCachePath,
		F16KV:               *c.F16,
		DebugMode:           *c.Debug,
		Grammar:             c.Grammar,
		NegativePromptScale: c.NegativePromptScale,
		RopeFreqBase:        c.RopeFreqBase,
		RopeFreqScale:       c.RopeFreqScale,
		NegativePrompt:      c.NegativePrompt,
		Mirostat:            int32(*c.LLMConfig.Mirostat),
		MirostatETA:         float32(*c.LLMConfig.MirostatETA),
		MirostatTAU:         float32(*c.LLMConfig.MirostatTAU),
		Debug:               *c.Debug,
		StopPrompts:         c.StopWords,
		Repeat:              int32(c.RepeatLastN),
		FrequencyPenalty:    float32(c.FrequencyPenalty),
		PresencePenalty:     float32(c.PresencePenalty),
		Penalty:             float32(c.RepeatPenalty),
		NKeep:               int32(c.Keep),
		Batch:               int32(c.Batch),
		IgnoreEOS:           c.IgnoreEOS,
		Seed:                getSeed(c),
		MLock:               *c.MMlock,
		MMap:                *c.MMap,
		MainGPU:             c.MainGPU,
		TensorSplit:         c.TensorSplit,
		TailFreeSamplingZ:   float32(*c.TFZ),
		TypicalP:            float32(*c.TypicalP),
	}

	metadata := map[string]string{}
	if c.ReasoningConfig.DisableReasoning != nil {
		if *c.ReasoningConfig.DisableReasoning {
			metadata["enable_thinking"] = "false"
		} else {
			metadata["enable_thinking"] = "true"
		}
	}
	// Forward the effective reasoning effort so the backend can pass it to the
	// jinja chat template (chat_template_kwargs.reasoning_effort) — the lever
	// models like gpt-oss / LFM2.5 actually read, distinct from enable_thinking.
	if c.ReasoningEffort != "" {
		metadata["reasoning_effort"] = c.ReasoningEffort
	}
	// Client request metadata overrides the server-derived reasoning levers and
	// reaches every backend through these standalone string keys (Python backends
	// read them directly). The reserved blob key is server-owned and skipped.
	for k, v := range c.RequestMetadata {
		if k == "chat_template_kwargs" {
			continue
		}
		metadata[k] = v
	}
	// Build the generic chat_template_kwargs blob (model config map + coerced
	// metadata) for llama.cpp and write it LAST so a client cannot clobber it.
	if blob := c.ResolveChatTemplateKwargs(metadata); len(blob) > 0 {
		b, err := json.Marshal(blob)
		if err != nil {
			xlog.Warn("failed to marshal chat_template_kwargs", "error", err)
		} else {
			metadata["chat_template_kwargs"] = string(b)
		}
	}
	pbOpts.Metadata = metadata

	// Logprobs and TopLogprobs are set by the caller if provided
	return pbOpts
}
