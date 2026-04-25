package backend

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// recordModelLoadFailure records a backend trace when model loading fails.
func recordModelLoadFailure(appConfig *config.ApplicationConfig, modelName, backend string, err error, data map[string]any) {
	if !appConfig.EnableTracing {
		return
	}
	trace.InitBackendTracingIfEnabled(appConfig.TracingMaxItems)
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

func ModelOptions(c config.ModelConfig, so *config.ApplicationConfig, opts ...model.Option) []model.Option {
	name := c.Name
	if name == "" {
		name = c.Model
	}

	defOpts := []model.Option{
		model.WithBackendString(c.Backend),
		model.WithModel(c.Model),
		model.WithContext(so.Context),
		model.WithModelID(name),
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

func grpcModelOpts(c config.ModelConfig, modelPath string) *pb.ModelOptions {
	b := 512
	if c.Batch != 0 {
		b = c.Batch
	}

	flashAttention := "auto"

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

	ctxSize := 4096
	if c.ContextSize != nil {
		ctxSize = *c.ContextSize
	}

	mmlock := false
	if c.MMlock != nil {
		mmlock = *c.MMlock
	}

	nGPULayers := 9999999
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
		Options:              c.Options,
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

	if c.MMProj != "" {
		opts.MMProj = filepath.Join(modelPath, c.MMProj)
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

	pbOpts := &pb.PredictOptions{
		Temperature:         float32(*c.Temperature),
		TopP:                float32(*c.TopP),
		NDraft:              c.NDraft,
		TopK:                int32(*c.TopK),
		MinP:                float32(*c.MinP),
		Tokens:              int32(*c.Maxtokens),
		Threads:             int32(*c.Threads),
		PromptCacheAll:      c.PromptCacheAll,
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
	pbOpts.Metadata = metadata

	// Logprobs and TopLogprobs are set by the caller if provided
	return pbOpts
}
