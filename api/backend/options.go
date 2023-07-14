package backend

import (
	"os"
	"path/filepath"

	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"

	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/pkg/langchain"
	"github.com/go-skynet/bloomz.cpp"
)

func langchainOptions(c config.Config) []langchain.PredictOption {
	return []langchain.PredictOption{
		langchain.SetModel(c.Model),
		langchain.SetMaxTokens(c.Maxtokens),
		langchain.SetTemperature(c.Temperature),
		langchain.SetStopWords(c.StopWords),
	}
}

func bloomzOptions(c config.Config) []bloomz.PredictOption {
	// Generate the prediction using the language model
	predictOptions := []bloomz.PredictOption{
		bloomz.SetTemperature(c.Temperature),
		bloomz.SetTopP(c.TopP),
		bloomz.SetTopK(c.TopK),
		bloomz.SetTokens(c.Maxtokens),
		bloomz.SetThreads(c.Threads),
	}

	if c.Seed != 0 {
		predictOptions = append(predictOptions, bloomz.SetSeed(c.Seed))
	}
	return predictOptions
}
func gRPCModelOpts(c config.Config) *pb.ModelOptions {
	b := 512
	if c.Batch != 0 {
		b = c.Batch
	}
	return &pb.ModelOptions{
		ContextSize: int32(c.ContextSize),
		Seed:        int32(c.Seed),
		NBatch:      int32(b),
		F16Memory:   c.F16,
		MLock:       c.MMlock,
		NUMA:        c.NUMA,
		Embeddings:  c.Embeddings,
		LowVRAM:     c.LowVRAM,
		NGPULayers:  int32(c.NGPULayers),
		MMap:        c.MMap,
		MainGPU:     c.MainGPU,
		Threads:     int32(c.Threads),
		TensorSplit: c.TensorSplit,
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
		Temperature:     float32(c.Temperature),
		TopP:            float32(c.TopP),
		TopK:            int32(c.TopK),
		Tokens:          int32(c.Maxtokens),
		Threads:         int32(c.Threads),
		PromptCacheAll:  c.PromptCacheAll,
		PromptCacheRO:   c.PromptCacheRO,
		PromptCachePath: promptCachePath,
		F16KV:           c.F16,
		DebugMode:       c.Debug,
		Grammar:         c.Grammar,

		Mirostat:          int32(c.Mirostat),
		MirostatETA:       float32(c.MirostatETA),
		MirostatTAU:       float32(c.MirostatTAU),
		Debug:             c.Debug,
		StopPrompts:       c.StopWords,
		Repeat:            int32(c.RepeatPenalty),
		NKeep:             int32(c.Keep),
		Batch:             int32(c.Batch),
		IgnoreEOS:         c.IgnoreEOS,
		Seed:              int32(c.Seed),
		FrequencyPenalty:  float32(c.FrequencyPenalty),
		MLock:             c.MMlock,
		MMap:              c.MMap,
		MainGPU:           c.MainGPU,
		TensorSplit:       c.TensorSplit,
		TailFreeSamplingZ: float32(c.TFZ),
		TypicalP:          float32(c.TypicalP),
	}
}
