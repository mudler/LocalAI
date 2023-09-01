package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	grpc "github.com/go-skynet/LocalAI/pkg/grpc"
	"github.com/hashicorp/go-multierror"
	"github.com/phayes/freeport"
	"github.com/rs/zerolog/log"
)

const (
	LlamaBackend        = "llama"
	LlamaStableBackend  = "llama-stable"
	BloomzBackend       = "bloomz"
	StarcoderBackend    = "starcoder"
	GPTJBackend         = "gptj"
	DollyBackend        = "dolly"
	MPTBackend          = "mpt"
	GPTNeoXBackend      = "gptneox"
	ReplitBackend       = "replit"
	Gpt2Backend         = "gpt2"
	Gpt4AllLlamaBackend = "gpt4all-llama"
	Gpt4AllMptBackend   = "gpt4all-mpt"
	Gpt4AllJBackend     = "gpt4all-j"
	Gpt4All             = "gpt4all"
	FalconBackend       = "falcon"
	FalconGGMLBackend   = "falcon-ggml"

	BertEmbeddingsBackend  = "bert-embeddings"
	RwkvBackend            = "rwkv"
	WhisperBackend         = "whisper"
	StableDiffusionBackend = "stablediffusion"
	PiperBackend           = "piper"
	LCHuggingFaceBackend   = "langchain-huggingface"
)

var AutoLoadBackends []string = []string{
	LlamaBackend,
	LlamaStableBackend,
	Gpt4All,
	FalconBackend,
	GPTNeoXBackend,
	BertEmbeddingsBackend,
	FalconGGMLBackend,
	GPTJBackend,
	Gpt2Backend,
	DollyBackend,
	MPTBackend,
	ReplitBackend,
	StarcoderBackend,
	BloomzBackend,
	RwkvBackend,
	WhisperBackend,
	StableDiffusionBackend,
	PiperBackend,
}

// starts the grpcModelProcess for the backend, and returns a grpc client
// It also loads the model
func (ml *ModelLoader) grpcModel(backend string, o *Options) func(string, string) (*grpc.Client, error) {
	return func(modelName, modelFile string) (*grpc.Client, error) {
		log.Debug().Msgf("Loading GRPC Model %s: %+v", backend, *o)

		var client *grpc.Client

		getFreeAddress := func() (string, error) {
			port, err := freeport.GetFreePort()
			if err != nil {
				return "", fmt.Errorf("failed allocating free ports: %s", err.Error())
			}
			return fmt.Sprintf("127.0.0.1:%d", port), nil
		}

		// Check if the backend is provided as external
		if uri, ok := o.externalBackends[backend]; ok {
			log.Debug().Msgf("Loading external backend: %s", uri)
			// check if uri is a file or a address
			if _, err := os.Stat(uri); err == nil {
				serverAddress, err := getFreeAddress()
				if err != nil {
					return nil, fmt.Errorf("failed allocating free ports: %s", err.Error())
				}
				// Make sure the process is executable
				if err := ml.startProcess(uri, o.model, serverAddress); err != nil {
					return nil, err
				}

				log.Debug().Msgf("GRPC Service Started")

				client = grpc.NewClient(serverAddress)
			} else {
				// address
				client = grpc.NewClient(uri)
			}
		} else {
			grpcProcess := filepath.Join(o.assetDir, "backend-assets", "grpc", backend)
			// Check if the file exists
			if _, err := os.Stat(grpcProcess); os.IsNotExist(err) {
				return nil, fmt.Errorf("grpc process not found: %s. some backends(stablediffusion, tts) require LocalAI compiled with GO_TAGS", grpcProcess)
			}

			serverAddress, err := getFreeAddress()
			if err != nil {
				return nil, fmt.Errorf("failed allocating free ports: %s", err.Error())
			}

			// Make sure the process is executable
			if err := ml.startProcess(grpcProcess, o.model, serverAddress); err != nil {
				return nil, err
			}

			log.Debug().Msgf("GRPC Service Started")

			client = grpc.NewClient(serverAddress)
		}

		// Wait for the service to start up
		ready := false
		for i := 0; i < o.grpcAttempts; i++ {
			if client.HealthCheck(context.Background()) {
				log.Debug().Msgf("GRPC Service Ready")
				ready = true
				break
			}
			time.Sleep(time.Duration(o.grpcAttemptsDelay) * time.Second)
		}

		if !ready {
			log.Debug().Msgf("GRPC Service NOT ready")
			return nil, fmt.Errorf("grpc service not ready")
		}

		options := *o.gRPCOptions
		options.Model = modelName
		options.ModelFile = modelFile

		log.Debug().Msgf("GRPC: Loading model with options: %+v", options)

		res, err := client.LoadModel(o.context, &options)
		if err != nil {
			return nil, fmt.Errorf("could not load model: %w", err)
		}
		if !res.Success {
			return nil, fmt.Errorf("could not load model (no success): %s", res.Message)
		}

		return client, nil
	}
}

func (ml *ModelLoader) BackendLoader(opts ...Option) (model *grpc.Client, err error) {
	o := NewOptions(opts...)

	log.Debug().Msgf("Loading model %s from %s", o.backendString, o.model)

	backend := strings.ToLower(o.backendString)

	if o.singleActiveBackend {
		ml.mu.Lock()
		log.Debug().Msgf("Stopping all backends except '%s'", o.model)
		ml.StopAllExcept(o.model)
		ml.mu.Unlock()
	}

	// if an external backend is provided, use it
	_, externalBackendExists := o.externalBackends[backend]
	if externalBackendExists {
		return ml.LoadModel(o.model, ml.grpcModel(backend, o))
	}

	switch backend {
	case LlamaBackend, LlamaStableBackend, GPTJBackend, DollyBackend,
		MPTBackend, Gpt2Backend, FalconBackend,
		GPTNeoXBackend, ReplitBackend, StarcoderBackend, BloomzBackend,
		RwkvBackend, LCHuggingFaceBackend, BertEmbeddingsBackend, FalconGGMLBackend, StableDiffusionBackend, WhisperBackend:
		return ml.LoadModel(o.model, ml.grpcModel(backend, o))
	case Gpt4AllLlamaBackend, Gpt4AllMptBackend, Gpt4AllJBackend, Gpt4All:
		o.gRPCOptions.LibrarySearchPath = filepath.Join(o.assetDir, "backend-assets", "gpt4all")
		return ml.LoadModel(o.model, ml.grpcModel(Gpt4All, o))
	case PiperBackend:
		o.gRPCOptions.LibrarySearchPath = filepath.Join(o.assetDir, "backend-assets", "espeak-ng-data")
		return ml.LoadModel(o.model, ml.grpcModel(PiperBackend, o))
	default:
		return nil, fmt.Errorf("backend unsupported: %s", o.backendString)
	}
}

func (ml *ModelLoader) GreedyLoader(opts ...Option) (*grpc.Client, error) {
	o := NewOptions(opts...)

	ml.mu.Lock()
	// Return earlier if we have a model already loaded
	// (avoid looping through all the backends)
	if m := ml.CheckIsLoaded(o.model); m != nil {
		log.Debug().Msgf("Model '%s' already loaded", o.model)
		ml.mu.Unlock()
		return m, nil
	}
	// If we can have only one backend active, kill all the others (except external backends)
	if o.singleActiveBackend {
		log.Debug().Msgf("Stopping all backends except '%s'", o.model)
		ml.StopAllExcept(o.model)
	}
	ml.mu.Unlock()

	var err error

	// autoload also external backends
	allBackendsToAutoLoad := []string{}
	allBackendsToAutoLoad = append(allBackendsToAutoLoad, AutoLoadBackends...)
	for _, b := range o.externalBackends {
		allBackendsToAutoLoad = append(allBackendsToAutoLoad, b)
	}
	log.Debug().Msgf("Loading model '%s' greedly from all the available backends: %s", o.model, strings.Join(allBackendsToAutoLoad, ", "))

	for _, b := range allBackendsToAutoLoad {
		log.Debug().Msgf("[%s] Attempting to load", b)
		options := []Option{
			WithBackendString(b),
			WithModel(o.model),
			WithLoadGRPCLoadModelOpts(o.gRPCOptions),
			WithThreads(o.threads),
			WithAssetDir(o.assetDir),
		}

		for k, v := range o.externalBackends {
			options = append(options, WithExternalBackend(k, v))
		}

		model, modelerr := ml.BackendLoader(options...)
		if modelerr == nil && model != nil {
			log.Debug().Msgf("[%s] Loads OK", b)
			return model, nil
		} else if modelerr != nil {
			err = multierror.Append(err, modelerr)
			log.Debug().Msgf("[%s] Fails: %s", b, modelerr.Error())
		} else if model == nil {
			err = multierror.Append(err, fmt.Errorf("backend returned no usable model"))
			log.Debug().Msgf("[%s] Fails: %s", b, "backend returned no usable model")
		}
	}

	return nil, fmt.Errorf("could not load model - all backends returned error: %s", err.Error())
}
