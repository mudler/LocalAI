package backend

import (
	"regexp"
	"strings"
	"sync"

	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/pkg/grpc"
	model "github.com/go-skynet/LocalAI/pkg/model"
)

func ModelInference(s string, loader *model.ModelLoader, c config.Config, o *options.Option, tokenCallback func(string) bool) (func() (string, error), error) {
	modelFile := c.Model

	grpcOpts := gRPCModelOpts(c)

	var inferenceModel *grpc.Client
	var err error

	opts := []model.Option{
		model.WithLoadGRPCLLMModelOpts(grpcOpts),
		model.WithThreads(uint32(c.Threads)), // some models uses this to allocate threads during startup
		model.WithAssetDir(o.AssetsDestination),
		model.WithModelFile(modelFile),
		model.WithContext(o.Context),
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

	// in GRPC, the backend is supposed to answer to 1 single token if stream is not supported
	fn := func() (string, error) {
		opts := gRPCPredictOpts(c, loader.ModelPath)
		opts.Prompt = s
		if tokenCallback != nil {
			ss := ""
			err := inferenceModel.PredictStream(o.Context, opts, func(s string) {
				tokenCallback(s)
				ss += s
			})
			return ss, err
		} else {
			reply, err := inferenceModel.Predict(o.Context, opts)
			return reply.Message, err
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

		return fn()
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
