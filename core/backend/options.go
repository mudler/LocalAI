package backend

import (
	"math/rand"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

func modelOpts(c config.BackendConfig, so *config.ApplicationConfig, opts []model.Option) []model.Option {
	if so.SingleBackend {
		opts = append(opts, model.WithSingleActiveBackend())
	}

	if so.ParallelBackendRequests {
		opts = append(opts, model.EnableParallelRequests)
	}

	if c.GRPC.Attempts != 0 {
		opts = append(opts, model.WithGRPCAttempts(c.GRPC.Attempts))
	}

	if c.GRPC.AttemptsSleepTime != 0 {
		opts = append(opts, model.WithGRPCAttemptsDelay(c.GRPC.AttemptsSleepTime))
	}

	for k, v := range so.ExternalGRPCBackends {
		opts = append(opts, model.WithExternalBackend(k, v))
	}

	return opts
}

func getSeed(c config.BackendConfig) int32 {
	seed := int32(*c.Seed)
	if seed == config.RAND_SEED {
		seed = rand.Int31()
	}

	return seed
}

func gRPCModelOpts(c config.BackendConfig) *pb.ModelOptions {
	b := 512
	if c.Batch != 0 {
		b = c.Batch
	}
	return &pb.ModelOptions{
		CUDA:                 c.CUDA || c.Diffusers.CUDA,
		SchedulerType:        c.Diffusers.SchedulerType,
		PipelineType:         c.Diffusers.PipelineType,
		CFGScale:             c.Diffusers.CFGScale,
		LoraAdapter:          c.LoraAdapter,
		LoraScale:            c.LoraScale,
		F16Memory:            *c.F16,
		LoraBase:             c.LoraBase,
		IMG2IMG:              c.Diffusers.IMG2IMG,
		CLIPModel:            c.Diffusers.ClipModel,
		CLIPSubfolder:        c.Diffusers.ClipSubFolder,
		CLIPSkip:             int32(c.Diffusers.ClipSkip),
		ControlNet:           c.Diffusers.ControlNet,
		ContextSize:          int32(*c.ContextSize),
		Seed:                 getSeed(c),
		NBatch:               int32(b),
		NoMulMatQ:            c.NoMulMatQ,
		DraftModel:           c.DraftModel,
		AudioPath:            c.VallE.AudioPath,
		Quantization:         c.Quantization,
		GPUMemoryUtilization: c.GPUMemoryUtilization,
		TrustRemoteCode:      c.TrustRemoteCode,
		EnforceEager:         c.EnforceEager,
		SwapSpace:            int32(c.SwapSpace),
		MaxModelLen:          int32(c.MaxModelLen),
		TensorParallelSize:   int32(c.TensorParallelSize),
		MMProj:               c.MMProj,
		FlashAttention:       c.FlashAttention,
		NoKVOffload:          c.NoKVOffloading,
		YarnExtFactor:        c.YarnExtFactor,
		YarnAttnFactor:       c.YarnAttnFactor,
		YarnBetaFast:         c.YarnBetaFast,
		YarnBetaSlow:         c.YarnBetaSlow,
		NGQA:                 c.NGQA,
		RMSNormEps:           c.RMSNormEps,
		MLock:                *c.MMlock,
		RopeFreqBase:         c.RopeFreqBase,
		RopeScaling:          c.RopeScaling,
		Type:                 c.ModelType,
		RopeFreqScale:        c.RopeFreqScale,
		NUMA:                 c.NUMA,
		Embeddings:           c.Embeddings,
		LowVRAM:              *c.LowVRAM,
		NGPULayers:           int32(*c.NGPULayers),
		MMap:                 *c.MMap,
		MainGPU:              c.MainGPU,
		Threads:              int32(*c.Threads),
		TensorSplit:          c.TensorSplit,
		// AutoGPTQ
		ModelBaseName:    c.AutoGPTQ.ModelBaseName,
		Device:           c.AutoGPTQ.Device,
		UseTriton:        c.AutoGPTQ.Triton,
		UseFastTokenizer: c.AutoGPTQ.UseFastTokenizer,
		// RWKV
		Tokenizer: c.Tokenizer,
	}
}

func gRPCPredictOpts(c config.BackendConfig, modelPath string) *pb.PredictOptions {
	promptCachePath := ""
	if c.PromptCachePath != "" {
		p := filepath.Join(modelPath, c.PromptCachePath)
		err := os.MkdirAll(filepath.Dir(p), 0750)
		if err == nil {
			promptCachePath = p
		} else {
			log.Error().Err(err).Str("promptCachePath", promptCachePath).Msg("error creating prompt cache folder")
		}
	}

	return &pb.PredictOptions{
		Temperature:         float32(*c.Temperature),
		TopP:                float32(*c.TopP),
		NDraft:              c.NDraft,
		TopK:                int32(*c.TopK),
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
		Repeat:              int32(c.RepeatPenalty),
		NKeep:               int32(c.Keep),
		Batch:               int32(c.Batch),
		IgnoreEOS:           c.IgnoreEOS,
		Seed:                getSeed(c),
		FrequencyPenalty:    float32(c.FrequencyPenalty),
		MLock:               *c.MMlock,
		MMap:                *c.MMap,
		MainGPU:             c.MainGPU,
		TensorSplit:         c.TensorSplit,
		TailFreeSamplingZ:   float32(*c.TFZ),
		TypicalP:            float32(*c.TypicalP),
	}
}
