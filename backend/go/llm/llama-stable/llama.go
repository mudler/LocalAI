package llama

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"

	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/go-llama.cpp"
)

type LLM struct {
	base.SingleThread

	llama *llama.LLama
}

func (llm *LLM) Load(opts *pb.ModelOptions) error {
	ropeFreqBase := float32(10000)
	ropeFreqScale := float32(1)

	if opts.RopeFreqBase != 0 {
		ropeFreqBase = opts.RopeFreqBase
	}
	if opts.RopeFreqScale != 0 {
		ropeFreqScale = opts.RopeFreqScale
	}

	llamaOpts := []llama.ModelOption{
		llama.WithRopeFreqBase(ropeFreqBase),
		llama.WithRopeFreqScale(ropeFreqScale),
	}

	if opts.NGQA != 0 {
		llamaOpts = append(llamaOpts, llama.WithGQA(int(opts.NGQA)))
	}

	if opts.RMSNormEps != 0 {
		llamaOpts = append(llamaOpts, llama.WithRMSNormEPS(opts.RMSNormEps))
	}

	if opts.ContextSize != 0 {
		llamaOpts = append(llamaOpts, llama.SetContext(int(opts.ContextSize)))
	}
	if opts.F16Memory {
		llamaOpts = append(llamaOpts, llama.EnableF16Memory)
	}
	if opts.Embeddings {
		llamaOpts = append(llamaOpts, llama.EnableEmbeddings)
	}
	if opts.NGPULayers != 0 {
		llamaOpts = append(llamaOpts, llama.SetGPULayers(int(opts.NGPULayers)))
	}

	llamaOpts = append(llamaOpts, llama.SetMMap(opts.MMap))
	llamaOpts = append(llamaOpts, llama.SetMainGPU(opts.MainGPU))
	llamaOpts = append(llamaOpts, llama.SetTensorSplit(opts.TensorSplit))
	if opts.NBatch != 0 {
		llamaOpts = append(llamaOpts, llama.SetNBatch(int(opts.NBatch)))
	} else {
		llamaOpts = append(llamaOpts, llama.SetNBatch(512))
	}

	if opts.NUMA {
		llamaOpts = append(llamaOpts, llama.EnableNUMA)
	}

	if opts.LowVRAM {
		llamaOpts = append(llamaOpts, llama.EnabelLowVRAM)
	}

	model, err := llama.New(opts.ModelFile, llamaOpts...)
	llm.llama = model

	return err
}

func buildPredictOptions(opts *pb.PredictOptions) []llama.PredictOption {
	ropeFreqBase := float32(10000)
	ropeFreqScale := float32(1)

	if opts.RopeFreqBase != 0 {
		ropeFreqBase = opts.RopeFreqBase
	}
	if opts.RopeFreqScale != 0 {
		ropeFreqScale = opts.RopeFreqScale
	}
	predictOptions := []llama.PredictOption{
		llama.SetTemperature(opts.Temperature),
		llama.SetTopP(opts.TopP),
		llama.SetTopK(int(opts.TopK)),
		llama.SetTokens(int(opts.Tokens)),
		llama.SetThreads(int(opts.Threads)),
		llama.WithGrammar(opts.Grammar),
		llama.SetRopeFreqBase(ropeFreqBase),
		llama.SetRopeFreqScale(ropeFreqScale),
		llama.SetNegativePromptScale(opts.NegativePromptScale),
		llama.SetNegativePrompt(opts.NegativePrompt),
	}

	if opts.PromptCacheAll {
		predictOptions = append(predictOptions, llama.EnablePromptCacheAll)
	}

	if opts.PromptCacheRO {
		predictOptions = append(predictOptions, llama.EnablePromptCacheRO)
	}

	// Expected absolute path
	if opts.PromptCachePath != "" {
		predictOptions = append(predictOptions, llama.SetPathPromptCache(opts.PromptCachePath))
	}

	if opts.Mirostat != 0 {
		predictOptions = append(predictOptions, llama.SetMirostat(int(opts.Mirostat)))
	}

	if opts.MirostatETA != 0 {
		predictOptions = append(predictOptions, llama.SetMirostatETA(opts.MirostatETA))
	}

	if opts.MirostatTAU != 0 {
		predictOptions = append(predictOptions, llama.SetMirostatTAU(opts.MirostatTAU))
	}

	if opts.Debug {
		predictOptions = append(predictOptions, llama.Debug)
	}

	predictOptions = append(predictOptions, llama.SetStopWords(opts.StopPrompts...))

	if opts.PresencePenalty != 0 {
		predictOptions = append(predictOptions, llama.SetPenalty(opts.PresencePenalty))
	}

	if opts.NKeep != 0 {
		predictOptions = append(predictOptions, llama.SetNKeep(int(opts.NKeep)))
	}

	if opts.Batch != 0 {
		predictOptions = append(predictOptions, llama.SetBatch(int(opts.Batch)))
	}

	if opts.F16KV {
		predictOptions = append(predictOptions, llama.EnableF16KV)
	}

	if opts.IgnoreEOS {
		predictOptions = append(predictOptions, llama.IgnoreEOS)
	}

	if opts.Seed != 0 {
		predictOptions = append(predictOptions, llama.SetSeed(int(opts.Seed)))
	}

	//predictOptions = append(predictOptions, llama.SetLogitBias(c.Seed))

	predictOptions = append(predictOptions, llama.SetFrequencyPenalty(opts.FrequencyPenalty))
	predictOptions = append(predictOptions, llama.SetMlock(opts.MLock))
	predictOptions = append(predictOptions, llama.SetMemoryMap(opts.MMap))
	predictOptions = append(predictOptions, llama.SetPredictionMainGPU(opts.MainGPU))
	predictOptions = append(predictOptions, llama.SetPredictionTensorSplit(opts.TensorSplit))
	predictOptions = append(predictOptions, llama.SetTailFreeSamplingZ(opts.TailFreeSamplingZ))
	predictOptions = append(predictOptions, llama.SetTypicalP(opts.TypicalP))
	return predictOptions
}

func (llm *LLM) Predict(opts *pb.PredictOptions) (string, error) {
	return llm.llama.Predict(opts.Prompt, buildPredictOptions(opts)...)
}

func (llm *LLM) PredictStream(opts *pb.PredictOptions, results chan string) error {
	predictOptions := buildPredictOptions(opts)

	predictOptions = append(predictOptions, llama.SetTokenCallback(func(token string) bool {
		results <- token
		return true
	}))

	go func() {
		_, err := llm.llama.Predict(opts.Prompt, predictOptions...)
		if err != nil {
			fmt.Println("err: ", err)
		}
		close(results)
	}()

	return nil
}

func (llm *LLM) Embeddings(opts *pb.PredictOptions) ([]float32, error) {
	predictOptions := buildPredictOptions(opts)

	if len(opts.EmbeddingTokens) > 0 {
		tokens := []int{}
		for _, t := range opts.EmbeddingTokens {
			tokens = append(tokens, int(t))
		}
		return llm.llama.TokenEmbeddings(tokens, predictOptions...)
	}

	return llm.llama.Embeddings(opts.Embeddings, predictOptions...)
}
