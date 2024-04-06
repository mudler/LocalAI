package backend

import (
	"math/rand"
	"os"
	"path/filepath"

	"github.com/go-skynet/LocalAI/core/config"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	model "github.com/go-skynet/LocalAI/pkg/model"
)

func modelOpts(bc *config.BackendConfig, so *config.ApplicationConfig, opts []model.Option) []model.Option {
	if so.SingleBackend {
		opts = append(opts, model.WithSingleActiveBackend())
	}

	if so.ParallelBackendRequests {
		opts = append(opts, model.EnableParallelRequests)
	}

	if bc.GRPC.Attempts != 0 {
		opts = append(opts, model.WithGRPCAttempts(bc.GRPC.Attempts))
	}

	if bc.GRPC.AttemptsSleepTime != 0 {
		opts = append(opts, model.WithGRPCAttemptsDelay(bc.GRPC.AttemptsSleepTime))
	}

	for k, v := range so.ExternalGRPCBackends {
		opts = append(opts, model.WithExternalBackend(k, v))
	}

	return opts
}

func getSeed(c *config.BackendConfig) int32 {
	seed := int32(*c.Seed)
	if seed == config.RAND_SEED {
		seed = rand.Int31()
	}

	return seed
}

func gRPCModelOpts(c *config.BackendConfig) *pb.ModelOptions {
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
		MMProj:               c.MMProj,
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

func gRPCPredictOpts(bc *config.BackendConfig, modelPath string) *pb.PredictOptions {
	promptCachePath := ""
	if bc.PromptCachePath != "" {
		p := filepath.Join(modelPath, bc.PromptCachePath)
		os.MkdirAll(filepath.Dir(p), 0755)
		promptCachePath = p
	}

	return &pb.PredictOptions{
		Temperature:         float32(*bc.Temperature),
		TopP:                float32(*bc.TopP),
		NDraft:              bc.NDraft,
		TopK:                int32(*bc.TopK),
		Tokens:              int32(*bc.Maxtokens),
		Threads:             int32(*bc.Threads),
		PromptCacheAll:      bc.PromptCacheAll,
		PromptCacheRO:       bc.PromptCacheRO,
		PromptCachePath:     promptCachePath,
		F16KV:               *bc.F16,
		DebugMode:           *bc.Debug,
		Grammar:             bc.Grammar,
		NegativePromptScale: bc.NegativePromptScale,
		RopeFreqBase:        bc.RopeFreqBase,
		RopeFreqScale:       bc.RopeFreqScale,
		NegativePrompt:      bc.NegativePrompt,
		Mirostat:            int32(*bc.LLMConfig.Mirostat),
		MirostatETA:         float32(*bc.LLMConfig.MirostatETA),
		MirostatTAU:         float32(*bc.LLMConfig.MirostatTAU),
		Debug:               *bc.Debug,
		StopPrompts:         bc.StopWords,
		Repeat:              int32(bc.RepeatPenalty),
		NKeep:               int32(bc.Keep),
		Batch:               int32(bc.Batch),
		IgnoreEOS:           bc.IgnoreEOS,
		Seed:                getSeed(bc),
		FrequencyPenalty:    float32(bc.FrequencyPenalty),
		MLock:               *bc.MMlock,
		MMap:                *bc.MMap,
		MainGPU:             bc.MainGPU,
		TensorSplit:         bc.TensorSplit,
		TailFreeSamplingZ:   float32(*bc.TFZ),
		TypicalP:            float32(*bc.TypicalP),
	}
}
