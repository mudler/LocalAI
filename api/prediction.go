package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/donomii/go-rwkv.cpp"
	"github.com/go-skynet/LocalAI/pkg/grpc"
	pb "github.com/go-skynet/LocalAI/pkg/grpc/proto"
	"github.com/go-skynet/LocalAI/pkg/langchain"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/stablediffusion"
	"github.com/go-skynet/bloomz.cpp"
	bert "github.com/go-skynet/go-bert.cpp"
	transformers "github.com/go-skynet/go-ggml-transformers.cpp"

	gpt4all "github.com/nomic-ai/gpt4all/gpt4all-bindings/golang"
)

// mutex still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
var mutexMap sync.Mutex
var mutexes map[string]*sync.Mutex = make(map[string]*sync.Mutex)

func gRPCModelOpts(c Config) *pb.ModelOptions {
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
		TensorSplit: c.TensorSplit,
	}
}

func gRPCPredictOpts(c Config, modelPath string) *pb.PredictOptions {
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

func ImageGeneration(height, width, mode, step, seed int, positive_prompt, negative_prompt, dst string, loader *model.ModelLoader, c Config, o *Option) (func() error, error) {
	if c.Backend != model.StableDiffusionBackend {
		return nil, fmt.Errorf("endpoint only working with stablediffusion models")
	}

	inferenceModel, err := loader.BackendLoader(
		model.WithBackendString(c.Backend),
		model.WithAssetDir(o.assetsDestination),
		model.WithThreads(uint32(c.Threads)),
		model.WithModelFile(c.ImageGenerationAssets),
	)
	if err != nil {
		return nil, err
	}

	var fn func() error
	switch model := inferenceModel.(type) {
	case *stablediffusion.StableDiffusion:
		fn = func() error {
			return model.GenerateImage(height, width, mode, step, seed, positive_prompt, negative_prompt, dst)
		}

	default:
		fn = func() error {
			return fmt.Errorf("creation of images not supported by the backend")
		}
	}

	return func() error {
		// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
		mutexMap.Lock()
		l, ok := mutexes[c.Backend]
		if !ok {
			m := &sync.Mutex{}
			mutexes[c.Backend] = m
			l = m
		}
		mutexMap.Unlock()
		l.Lock()
		defer l.Unlock()

		return fn()
	}, nil
}

func ModelEmbedding(s string, tokens []int, loader *model.ModelLoader, c Config, o *Option) (func() ([]float32, error), error) {
	if !c.Embeddings {
		return nil, fmt.Errorf("endpoint disabled for this model by API configuration")
	}

	modelFile := c.Model

	grpcOpts := gRPCModelOpts(c)

	var inferenceModel interface{}
	var err error

	opts := []model.Option{
		model.WithLoadGRPCOpts(grpcOpts),
		model.WithThreads(uint32(c.Threads)),
		model.WithAssetDir(o.assetsDestination),
		model.WithModelFile(modelFile),
	}

	if c.Backend == "" {
		inferenceModel, err = loader.GreedyLoader(opts...)
	} else {
		opts = append(opts, model.WithBackendString(c.Backend))
		inferenceModel, err = loader.BackendLoader(opts...)
	}
	if err != nil {
		return nil, err
	}

	var fn func() ([]float32, error)
	switch model := inferenceModel.(type) {
	case *grpc.Client:
		fn = func() ([]float32, error) {
			predictOptions := gRPCPredictOpts(c, loader.ModelPath)
			if len(tokens) > 0 {
				embeds := []int32{}

				for _, t := range tokens {
					embeds = append(embeds, int32(t))
				}
				predictOptions.EmbeddingTokens = embeds

				res, err := model.Embeddings(context.TODO(), predictOptions)
				if err != nil {
					return nil, err
				}

				return res.Embeddings, nil
			}
			predictOptions.Embeddings = s

			res, err := model.Embeddings(context.TODO(), predictOptions)
			if err != nil {
				return nil, err
			}

			return res.Embeddings, nil
		}

	// bert embeddings
	case *bert.Bert:
		fn = func() ([]float32, error) {
			if len(tokens) > 0 {
				return model.TokenEmbeddings(tokens, bert.SetThreads(c.Threads))
			}
			return model.Embeddings(s, bert.SetThreads(c.Threads))
		}
	default:
		fn = func() ([]float32, error) {
			return nil, fmt.Errorf("embeddings not supported by the backend")
		}
	}

	return func() ([]float32, error) {
		// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
		mutexMap.Lock()
		l, ok := mutexes[modelFile]
		if !ok {
			m := &sync.Mutex{}
			mutexes[modelFile] = m
			l = m
		}
		mutexMap.Unlock()
		l.Lock()
		defer l.Unlock()

		embeds, err := fn()
		if err != nil {
			return embeds, err
		}
		// Remove trailing 0s
		for i := len(embeds) - 1; i >= 0; i-- {
			if embeds[i] == 0.0 {
				embeds = embeds[:i]
			} else {
				break
			}
		}
		return embeds, nil
	}, nil
}

func ModelInference(s string, loader *model.ModelLoader, c Config, o *Option, tokenCallback func(string) bool) (func() (string, error), error) {
	supportStreams := false
	modelFile := c.Model

	grpcOpts := gRPCModelOpts(c)

	var inferenceModel interface{}
	var err error

	opts := []model.Option{
		model.WithLoadGRPCOpts(grpcOpts),
		model.WithThreads(uint32(c.Threads)),
		model.WithAssetDir(o.assetsDestination),
		model.WithModelFile(modelFile),
	}

	if c.Backend == "" {
		inferenceModel, err = loader.GreedyLoader(opts...)
	} else {
		opts = append(opts, model.WithBackendString(c.Backend))
		inferenceModel, err = loader.BackendLoader(opts...)
	}
	if err != nil {
		return nil, err
	}

	var fn func() (string, error)

	switch model := inferenceModel.(type) {
	case *rwkv.RwkvState:
		supportStreams = true

		fn = func() (string, error) {
			stopWord := "\n"
			if len(c.StopWords) > 0 {
				stopWord = c.StopWords[0]
			}

			if err := model.ProcessInput(s); err != nil {
				return "", err
			}

			response := model.GenerateResponse(c.Maxtokens, stopWord, float32(c.Temperature), float32(c.TopP), tokenCallback)

			return response, nil
		}
	case *transformers.GPTNeoX:
		fn = func() (string, error) {
			// Generate the prediction using the language model
			predictOptions := []transformers.PredictOption{
				transformers.SetTemperature(c.Temperature),
				transformers.SetTopP(c.TopP),
				transformers.SetTopK(c.TopK),
				transformers.SetTokens(c.Maxtokens),
				transformers.SetThreads(c.Threads),
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, transformers.SetBatch(c.Batch))
			}

			if c.Seed != 0 {
				predictOptions = append(predictOptions, transformers.SetSeed(c.Seed))
			}

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	case *transformers.Replit:
		fn = func() (string, error) {
			// Generate the prediction using the language model
			predictOptions := []transformers.PredictOption{
				transformers.SetTemperature(c.Temperature),
				transformers.SetTopP(c.TopP),
				transformers.SetTopK(c.TopK),
				transformers.SetTokens(c.Maxtokens),
				transformers.SetThreads(c.Threads),
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, transformers.SetBatch(c.Batch))
			}

			if c.Seed != 0 {
				predictOptions = append(predictOptions, transformers.SetSeed(c.Seed))
			}

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	case *transformers.Starcoder:
		fn = func() (string, error) {
			// Generate the prediction using the language model
			predictOptions := []transformers.PredictOption{
				transformers.SetTemperature(c.Temperature),
				transformers.SetTopP(c.TopP),
				transformers.SetTopK(c.TopK),
				transformers.SetTokens(c.Maxtokens),
				transformers.SetThreads(c.Threads),
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, transformers.SetBatch(c.Batch))
			}

			if c.Seed != 0 {
				predictOptions = append(predictOptions, transformers.SetSeed(c.Seed))
			}

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	case *transformers.MPT:
		fn = func() (string, error) {
			// Generate the prediction using the language model
			predictOptions := []transformers.PredictOption{
				transformers.SetTemperature(c.Temperature),
				transformers.SetTopP(c.TopP),
				transformers.SetTopK(c.TopK),
				transformers.SetTokens(c.Maxtokens),
				transformers.SetThreads(c.Threads),
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, transformers.SetBatch(c.Batch))
			}

			if c.Seed != 0 {
				predictOptions = append(predictOptions, transformers.SetSeed(c.Seed))
			}

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	case *bloomz.Bloomz:
		fn = func() (string, error) {
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

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	case *transformers.Falcon:
		fn = func() (string, error) {
			// Generate the prediction using the language model
			predictOptions := []transformers.PredictOption{
				transformers.SetTemperature(c.Temperature),
				transformers.SetTopP(c.TopP),
				transformers.SetTopK(c.TopK),
				transformers.SetTokens(c.Maxtokens),
				transformers.SetThreads(c.Threads),
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, transformers.SetBatch(c.Batch))
			}

			if c.Seed != 0 {
				predictOptions = append(predictOptions, transformers.SetSeed(c.Seed))
			}

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	case *transformers.GPTJ:
		fn = func() (string, error) {
			// Generate the prediction using the language model
			predictOptions := []transformers.PredictOption{
				transformers.SetTemperature(c.Temperature),
				transformers.SetTopP(c.TopP),
				transformers.SetTopK(c.TopK),
				transformers.SetTokens(c.Maxtokens),
				transformers.SetThreads(c.Threads),
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, transformers.SetBatch(c.Batch))
			}

			if c.Seed != 0 {
				predictOptions = append(predictOptions, transformers.SetSeed(c.Seed))
			}

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	case *transformers.Dolly:
		fn = func() (string, error) {
			// Generate the prediction using the language model
			predictOptions := []transformers.PredictOption{
				transformers.SetTemperature(c.Temperature),
				transformers.SetTopP(c.TopP),
				transformers.SetTopK(c.TopK),
				transformers.SetTokens(c.Maxtokens),
				transformers.SetThreads(c.Threads),
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, transformers.SetBatch(c.Batch))
			}

			if c.Seed != 0 {
				predictOptions = append(predictOptions, transformers.SetSeed(c.Seed))
			}

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	case *transformers.GPT2:
		fn = func() (string, error) {
			// Generate the prediction using the language model
			predictOptions := []transformers.PredictOption{
				transformers.SetTemperature(c.Temperature),
				transformers.SetTopP(c.TopP),
				transformers.SetTopK(c.TopK),
				transformers.SetTokens(c.Maxtokens),
				transformers.SetThreads(c.Threads),
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, transformers.SetBatch(c.Batch))
			}

			if c.Seed != 0 {
				predictOptions = append(predictOptions, transformers.SetSeed(c.Seed))
			}

			return model.Predict(
				s,
				predictOptions...,
			)
		}
	case *gpt4all.Model:
		supportStreams = true

		fn = func() (string, error) {
			if tokenCallback != nil {
				model.SetTokenCallback(tokenCallback)
			}

			// Generate the prediction using the language model
			predictOptions := []gpt4all.PredictOption{
				gpt4all.SetTemperature(c.Temperature),
				gpt4all.SetTopP(c.TopP),
				gpt4all.SetTopK(c.TopK),
				gpt4all.SetTokens(c.Maxtokens),
			}

			if c.Batch != 0 {
				predictOptions = append(predictOptions, gpt4all.SetBatch(c.Batch))
			}

			str, er := model.Predict(
				s,
				predictOptions...,
			)
			// Seems that if we don't free the callback explicitly we leave functions registered (that might try to send on closed channels)
			// For instance otherwise the API returns: {"error":{"code":500,"message":"send on closed channel","type":""}}
			// after a stream event has occurred
			model.SetTokenCallback(nil)
			return str, er
		}
	case *grpc.Client:
		// in GRPC, the backend is supposed to answer to 1 single token if stream is not supported
		supportStreams = true
		fn = func() (string, error) {

			opts := gRPCPredictOpts(c, loader.ModelPath)
			opts.Prompt = s
			if tokenCallback != nil {
				ss := ""
				err := model.PredictStream(context.TODO(), opts, func(s string) {
					tokenCallback(s)
					ss += s
				})
				return ss, err
			} else {
				reply, err := model.Predict(context.TODO(), opts)
				return reply.Message, err
			}
		}
	case *langchain.HuggingFace:
		fn = func() (string, error) {

			// Generate the prediction using the language model
			predictOptions := []langchain.PredictOption{
				langchain.SetModel(c.Model),
				langchain.SetMaxTokens(c.Maxtokens),
				langchain.SetTemperature(c.Temperature),
				langchain.SetStopWords(c.StopWords),
			}

			pred, er := model.PredictHuggingFace(s, predictOptions...)
			if er != nil {
				return "", er
			}
			return pred.Completion, nil
		}
	}

	return func() (string, error) {
		// This is still needed, see: https://github.com/ggerganov/llama.cpp/discussions/784
		mutexMap.Lock()
		l, ok := mutexes[modelFile]
		if !ok {
			m := &sync.Mutex{}
			mutexes[modelFile] = m
			l = m
		}
		mutexMap.Unlock()
		l.Lock()
		defer l.Unlock()

		res, err := fn()
		if tokenCallback != nil && !supportStreams {
			tokenCallback(res)
		}
		return res, err
	}, nil
}

func ComputeChoices(predInput string, input *OpenAIRequest, config *Config, o *Option, loader *model.ModelLoader, cb func(string, *[]Choice), tokenCallback func(string) bool) ([]Choice, error) {
	result := []Choice{}

	n := input.N

	if input.N == 0 {
		n = 1
	}

	// get the model function to call for the result
	predFunc, err := ModelInference(predInput, loader, *config, o, tokenCallback)
	if err != nil {
		return result, err
	}

	for i := 0; i < n; i++ {
		prediction, err := predFunc()
		if err != nil {
			return result, err
		}

		prediction = Finetune(*config, predInput, prediction)
		cb(prediction, &result)

		//result = append(result, Choice{Text: prediction})

	}
	return result, err
}

var cutstrings map[string]*regexp.Regexp = make(map[string]*regexp.Regexp)
var mu sync.Mutex = sync.Mutex{}

func Finetune(config Config, input, prediction string) string {
	if config.Echo {
		prediction = input + prediction
	}

	for _, c := range config.Cutstrings {
		mu.Lock()
		reg, ok := cutstrings[c]
		if !ok {
			cutstrings[c] = regexp.MustCompile(c)
			reg = cutstrings[c]
		}
		mu.Unlock()
		prediction = reg.ReplaceAllString(prediction, "")
	}

	for _, c := range config.TrimSpace {
		prediction = strings.TrimSpace(strings.TrimPrefix(prediction, c))
	}
	return prediction

}
