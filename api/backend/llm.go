package backend

import (
	"context"
	"regexp"
	"strings"
	"sync"

	"github.com/donomii/go-rwkv.cpp"
	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/pkg/grpc"
	"github.com/go-skynet/LocalAI/pkg/langchain"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/bloomz.cpp"
)

func ModelInference(s string, loader *model.ModelLoader, c config.Config, o *options.Option, tokenCallback func(string) bool) (func() (string, error), error) {
	supportStreams := false
	modelFile := c.Model

	grpcOpts := gRPCModelOpts(c)

	var inferenceModel interface{}
	var err error

	opts := []model.Option{
		model.WithLoadGRPCOpts(grpcOpts),
		model.WithThreads(uint32(c.Threads)), // GPT4all uses this
		model.WithAssetDir(o.AssetsDestination),
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
		l := Lock(modelFile)
		defer l.Unlock()

		res, err := fn()
		if tokenCallback != nil && !supportStreams {
			tokenCallback(res)
		}
		return res, err
	}, nil
}

var cutstrings map[string]*regexp.Regexp = make(map[string]*regexp.Regexp)
var mu sync.Mutex = sync.Mutex{}

func Finetune(config config.Config, input, prediction string) string {
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
