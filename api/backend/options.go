package backend

import (
	"os"
	"path/filepath"

	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"

	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
)

func modelOpts(c config.Config, o *options.Option, opts []model.Option) []model.Option {
	if o.SingleBackend {
		opts = append(opts, model.WithSingleActiveBackend())
	}

	if o.ParallelBackendRequests {
		opts = append(opts, model.EnableParallelRequests)
	}

	if c.GRPC.Attempts != 0 {
		opts = append(opts, model.WithGRPCAttempts(c.GRPC.Attempts))
	}

	if c.GRPC.AttemptsSleepTime != 0 {
		opts = append(opts, model.WithGRPCAttemptsDelay(c.GRPC.AttemptsSleepTime))
	}

	for k, v := range o.ExternalGRPCBackends {
		opts = append(opts, model.WithExternalBackend(k, v))
	}

	return opts
}

func gRPCModelOpts(c config.Config) *pb.ModelOptions {
	b := 512
	if c.Batch != 0 {
		b = c.Batch
	}

	return &pb.ModelOptions{
		ContextSize:    int32(c.ContextSize),
		Seed:           int32(c.Seed),
		NBatch:         int32(b),
		NoMulMatQ:      c.NoMulMatQ,
		CUDA:           c.CUDA, // diffusers, transformers
		DraftModel:     c.DraftModel,
		AudioPath:      c.VallE.AudioPath,
		Quantization:   c.Quantization,
		MMProj:         c.MMProj,
		YarnExtFactor:  c.YarnExtFactor,
		YarnAttnFactor: c.YarnAttnFactor,
		YarnBetaFast:   c.YarnBetaFast,
		YarnBetaSlow:   c.YarnBetaSlow,
		LoraAdapter:    c.LoraAdapter,
		LoraBase:       c.LoraBase,
		LoraScale:      c.LoraScale,
		NGQA:           c.NGQA,
		RMSNormEps:     c.RMSNormEps,
		F16Memory:      c.F16,
		MLock:          c.MMlock,
		RopeFreqBase:   c.RopeFreqBase,
		RopeScaling:    c.RopeScaling,
		Type:           c.ModelType,
		RopeFreqScale:  c.RopeFreqScale,
		NUMA:           c.NUMA,
		Embeddings:     c.Embeddings,
		LowVRAM:        c.LowVRAM,
		NGPULayers:     int32(c.NGPULayers),
		MMap:           c.MMap,
		MainGPU:        c.MainGPU,
		Threads:        int32(c.Threads),
		TensorSplit:    c.TensorSplit,
		// AutoGPTQ
		ModelBaseName:    c.AutoGPTQ.ModelBaseName,
		Device:           c.AutoGPTQ.Device,
		UseTriton:        c.AutoGPTQ.Triton,
		UseFastTokenizer: c.AutoGPTQ.UseFastTokenizer,
		// RWKV
		Tokenizer: c.Tokenizer,
	}
}

func gRPCPredictOpts(c config.Config, modelPath string) *pb.PredictOptions {
	promptCachePath := ""
	if c.PromptCachePath != "" {
		p := filepath.Join(modelPath, c.PromptCachePath)
		os.MkdirAll(filepath.Dir(p), 0755)
		promptCachePath = p
	}
	return &pb.PredictOptions{
		Temperature:         float32(c.Temperature),
		TopP:                float32(c.TopP),
		NDraft:              c.NDraft,
		TopK:                int32(c.TopK),
		Tokens:              int32(c.Maxtokens),
		Threads:             int32(c.Threads),
		PromptCacheAll:      c.PromptCacheAll,
		PromptCacheRO:       c.PromptCacheRO,
		PromptCachePath:     promptCachePath,
		F16KV:               c.F16,
		DebugMode:           c.Debug,
		Grammar:             c.Grammar,
		NegativePromptScale: c.NegativePromptScale,
		RopeFreqBase:        c.RopeFreqBase,
		RopeFreqScale:       c.RopeFreqScale,
		NegativePrompt:      c.NegativePrompt,
		Mirostat:            int32(c.LLMConfig.Mirostat),
		MirostatETA:         float32(c.LLMConfig.MirostatETA),
		MirostatTAU:         float32(c.LLMConfig.MirostatTAU),
		Debug:               c.Debug,
		StopPrompts:         c.StopWords,
		Repeat:              int32(c.RepeatPenalty),
		NKeep:               int32(c.Keep),
		Batch:               int32(c.Batch),
		IgnoreEOS:           c.IgnoreEOS,
		Seed:                int32(c.Seed),
		FrequencyPenalty:    float32(c.FrequencyPenalty),
		MLock:               c.MMlock,
		MMap:                c.MMap,
		MainGPU:             c.MainGPU,
		TensorSplit:         c.TensorSplit,
		TailFreeSamplingZ:   float32(c.TFZ),
		TypicalP:            float32(c.TypicalP),
	}
}
