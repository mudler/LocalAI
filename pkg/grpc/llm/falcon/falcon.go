package falcon

// This is a wrapper to statisfy the GRPC service interface
// It is meant to be used by the main executable that is the server for the specific backend type (falcon, gpt3, etc)
import (
	"fmt"

	"github.com/go-skynet/LocalAI/pkg/grpc/base"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"

	ggllm "github.com/mudler/go-ggllm.cpp"
)

type LLM struct {
	base.SingleThread

	falcon *ggllm.Falcon
}

func (llm *LLM) Load(opts *pb.ModelOptions) error {
	ggllmOpts := []ggllm.ModelOption{}
	if opts.ContextSize != 0 {
		ggllmOpts = append(ggllmOpts, ggllm.SetContext(int(opts.ContextSize)))
	}
	// F16 doesn't seem to produce good output at all!
	//if c.F16 {
	//	llamaOpts = append(llamaOpts, llama.EnableF16Memory)
	//}

	if opts.NGPULayers != 0 {
		ggllmOpts = append(ggllmOpts, ggllm.SetGPULayers(int(opts.NGPULayers)))
	}

	ggllmOpts = append(ggllmOpts, ggllm.SetMMap(opts.MMap))
	ggllmOpts = append(ggllmOpts, ggllm.SetMainGPU(opts.MainGPU))
	ggllmOpts = append(ggllmOpts, ggllm.SetTensorSplit(opts.TensorSplit))
	if opts.NBatch != 0 {
		ggllmOpts = append(ggllmOpts, ggllm.SetNBatch(int(opts.NBatch)))
	} else {
		ggllmOpts = append(ggllmOpts, ggllm.SetNBatch(512))
	}

	model, err := ggllm.New(opts.ModelFile, ggllmOpts...)
	llm.falcon = model
	return err
}

func buildPredictOptions(opts *pb.PredictOptions) []ggllm.PredictOption {
	predictOptions := []ggllm.PredictOption{
		ggllm.SetTemperature(float64(opts.Temperature)),
		ggllm.SetTopP(float64(opts.TopP)),
		ggllm.SetTopK(int(opts.TopK)),
		ggllm.SetTokens(int(opts.Tokens)),
		ggllm.SetThreads(int(opts.Threads)),
	}

	if opts.PromptCacheAll {
		predictOptions = append(predictOptions, ggllm.EnablePromptCacheAll)
	}

	if opts.PromptCacheRO {
		predictOptions = append(predictOptions, ggllm.EnablePromptCacheRO)
	}

	// Expected absolute path
	if opts.PromptCachePath != "" {
		predictOptions = append(predictOptions, ggllm.SetPathPromptCache(opts.PromptCachePath))
	}

	if opts.Mirostat != 0 {
		predictOptions = append(predictOptions, ggllm.SetMirostat(int(opts.Mirostat)))
	}

	if opts.MirostatETA != 0 {
		predictOptions = append(predictOptions, ggllm.SetMirostatETA(float64(opts.MirostatETA)))
	}

	if opts.MirostatTAU != 0 {
		predictOptions = append(predictOptions, ggllm.SetMirostatTAU(float64(opts.MirostatTAU)))
	}

	if opts.Debug {
		predictOptions = append(predictOptions, ggllm.Debug)
	}

	predictOptions = append(predictOptions, ggllm.SetStopWords(opts.StopPrompts...))

	if opts.PresencePenalty != 0 {
		predictOptions = append(predictOptions, ggllm.SetPenalty(float64(opts.PresencePenalty)))
	}

	if opts.NKeep != 0 {
		predictOptions = append(predictOptions, ggllm.SetNKeep(int(opts.NKeep)))
	}

	if opts.Batch != 0 {
		predictOptions = append(predictOptions, ggllm.SetBatch(int(opts.Batch)))
	}

	if opts.IgnoreEOS {
		predictOptions = append(predictOptions, ggllm.IgnoreEOS)
	}

	if opts.Seed != 0 {
		predictOptions = append(predictOptions, ggllm.SetSeed(int(opts.Seed)))
	}

	//predictOptions = append(predictOptions, llama.SetLogitBias(c.Seed))

	predictOptions = append(predictOptions, ggllm.SetFrequencyPenalty(float64(opts.FrequencyPenalty)))
	predictOptions = append(predictOptions, ggllm.SetMlock(opts.MLock))
	predictOptions = append(predictOptions, ggllm.SetMemoryMap(opts.MMap))
	predictOptions = append(predictOptions, ggllm.SetPredictionMainGPU(opts.MainGPU))
	predictOptions = append(predictOptions, ggllm.SetPredictionTensorSplit(opts.TensorSplit))
	predictOptions = append(predictOptions, ggllm.SetTailFreeSamplingZ(float64(opts.TailFreeSamplingZ)))
	predictOptions = append(predictOptions, ggllm.SetTypicalP(float64(opts.TypicalP)))
	return predictOptions
}

func (llm *LLM) Predict(opts *pb.PredictOptions) (string, error) {
	return llm.falcon.Predict(opts.Prompt, buildPredictOptions(opts)...)
}

func (llm *LLM) PredictStream(opts *pb.PredictOptions, results chan string) error {

	predictOptions := buildPredictOptions(opts)

	predictOptions = append(predictOptions, ggllm.SetTokenCallback(func(token string) bool {
		if token == "<|endoftext|>" {
			return true
		}
		results <- token
		return true
	}))

	go func() {
		_, err := llm.falcon.Predict(opts.Prompt, predictOptions...)
		if err != nil {
			fmt.Println("err: ", err)
		}
		close(results)
	}()

	return nil
}
