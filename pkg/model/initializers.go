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

var Aliases map[string]string = map[string]string{
	"go-llama": GoLlamaBackend,
	"llama":    LLamaCPP,
}

const (
	GoLlamaBackend      = "llama"
	LlamaGGML           = "llama-ggml"
	LLamaCPP            = "llama-cpp"
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
	FalconGGMLBackend   = "falcon-ggml"

	BertEmbeddingsBackend  = "bert-embeddings"
	RwkvBackend            = "rwkv"
	WhisperBackend         = "whisper"
	StableDiffusionBackend = "stablediffusion"
	TinyDreamBackend       = "tinydream"
	PiperBackend           = "piper"
	LCHuggingFaceBackend   = "langchain-huggingface"

	// External Backends that need special handling within LocalAI:
	TransformersMusicGen = "transformers-musicgen"
)

var AutoLoadBackends []string = []string{
	LLamaCPP,
	LlamaGGML,
	GoLlamaBackend,
	Gpt4All,
	GPTNeoXBackend,
	BertEmbeddingsBackend,
	FalconGGMLBackend,
	GPTJBackend,
	Gpt2Backend,
	DollyBackend,
	MPTBackend,
	ReplitBackend,
	StarcoderBackend,
	RwkvBackend,
	WhisperBackend,
	StableDiffusionBackend,
	TinyDreamBackend,
	PiperBackend,
}

// starts the grpcModelProcess for the backend, and returns a grpc client
// It also loads the model
func (ml *ModelLoader) grpcModel(backend string, o *Options) func(string, string) (ModelAddress, error) {
	return func(modelName, modelFile string) (ModelAddress, error) {
		log.Debug().Msgf("Loading Model %s with gRPC (file: %s) (backend: %s): %+v", modelName, modelFile, backend, *o)

		var client ModelAddress

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
					return "", fmt.Errorf("failed allocating free ports: %s", err.Error())
				}
				// Make sure the process is executable
				if err := ml.startProcess(uri, o.model, serverAddress); err != nil {
					return "", err
				}

				log.Debug().Msgf("GRPC Service Started")

				client = ModelAddress(serverAddress)
			} else {
				// address
				client = ModelAddress(uri)
			}
		} else {
			grpcProcess := filepath.Join(o.assetDir, "backend-assets", "grpc", backend)
			// Check if the file exists
			if _, err := os.Stat(grpcProcess); os.IsNotExist(err) {
				return "", fmt.Errorf("grpc process not found: %s. some backends(stablediffusion, tts) require LocalAI compiled with GO_TAGS", grpcProcess)
			}

			serverAddress, err := getFreeAddress()
			if err != nil {
				return "", fmt.Errorf("failed allocating free ports: %s", err.Error())
			}

			// Make sure the process is executable
			if err := ml.startProcess(grpcProcess, o.model, serverAddress); err != nil {
				return "", err
			}

			log.Debug().Msgf("GRPC Service Started")

			client = ModelAddress(serverAddress)
		}

		// Wait for the service to start up
		ready := false
		for i := 0; i < o.grpcAttempts; i++ {
			alive, err := client.GRPC(o.parallelRequests, ml.wd).HealthCheck(context.Background())
			if alive {
				log.Debug().Msgf("GRPC Service Ready")
				ready = true
				break
			}
			if err != nil && i == o.grpcAttempts-1 {
				log.Error().Msgf("Failed starting/connecting to the gRPC service: %s", err.Error())
			}
			time.Sleep(time.Duration(o.grpcAttemptsDelay) * time.Second)
		}

		if !ready {
			log.Debug().Msgf("GRPC Service NOT ready")
			return "", fmt.Errorf("grpc service not ready")
		}

		options := *o.gRPCOptions
		options.Model = modelName
		options.ModelFile = modelFile

		log.Debug().Msgf("GRPC: Loading model with options: %+v", options)

		res, err := client.GRPC(o.parallelRequests, ml.wd).LoadModel(o.context, &options)
		if err != nil {
			return "", fmt.Errorf("could not load model: %w", err)
		}
		if !res.Success {
			return "", fmt.Errorf("could not load model (no success): %s", res.Message)
		}

		return client, nil
	}
}

func (ml *ModelLoader) resolveAddress(addr ModelAddress, parallel bool) (grpc.Backend, error) {
	if parallel {
		return addr.GRPC(parallel, ml.wd), nil
	}

	if _, ok := ml.grpcClients[string(addr)]; !ok {
		ml.grpcClients[string(addr)] = addr.GRPC(parallel, ml.wd)
	}
	return ml.grpcClients[string(addr)], nil
}

func (ml *ModelLoader) BackendLoader(opts ...Option) (client grpc.Backend, err error) {
	o := NewOptions(opts...)

	if o.model != "" {
		log.Info().Msgf("Loading model '%s' with backend %s", o.model, o.backendString)
	} else {
		log.Info().Msgf("Loading model with backend %s", o.backendString)
	}

	backend := strings.ToLower(o.backendString)
	if realBackend, exists := Aliases[backend]; exists {
		backend = realBackend
		log.Debug().Msgf("%s is an alias of %s", backend, realBackend)
	}

	if o.singleActiveBackend {
		ml.mu.Lock()
		log.Debug().Msgf("Stopping all backends except '%s'", o.model)
		ml.StopAllExcept(o.model)
		ml.mu.Unlock()
	}

	var backendToConsume string

	switch backend {
	case Gpt4AllLlamaBackend, Gpt4AllMptBackend, Gpt4AllJBackend, Gpt4All:
		o.gRPCOptions.LibrarySearchPath = filepath.Join(o.assetDir, "backend-assets", "gpt4all")
		backendToConsume = Gpt4All
	case PiperBackend:
		o.gRPCOptions.LibrarySearchPath = filepath.Join(o.assetDir, "backend-assets", "espeak-ng-data")
		backendToConsume = PiperBackend
	default:
		backendToConsume = backend
	}

	addr, err := ml.LoadModel(o.model, ml.grpcModel(backendToConsume, o))
	if err != nil {
		return nil, err
	}

	return ml.resolveAddress(addr, o.parallelRequests)
}

func (ml *ModelLoader) GreedyLoader(opts ...Option) (grpc.Backend, error) {
	o := NewOptions(opts...)

	ml.mu.Lock()
	// Return earlier if we have a model already loaded
	// (avoid looping through all the backends)
	if m := ml.CheckIsLoaded(o.model); m != "" {
		log.Debug().Msgf("Model '%s' already loaded", o.model)
		ml.mu.Unlock()

		return ml.resolveAddress(m, o.parallelRequests)
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

	if o.model != "" {
		log.Info().Msgf("Trying to load the model '%s' with all the available backends: %s", o.model, strings.Join(allBackendsToAutoLoad, ", "))
	}

	for _, b := range allBackendsToAutoLoad {
		log.Info().Msgf("[%s] Attempting to load", b)
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
			log.Info().Msgf("[%s] Loads OK", b)
			return model, nil
		} else if modelerr != nil {
			err = multierror.Append(err, modelerr)
			log.Info().Msgf("[%s] Fails: %s", b, modelerr.Error())
		} else if model == nil {
			err = multierror.Append(err, fmt.Errorf("backend returned no usable model"))
			log.Info().Msgf("[%s] Fails: %s", b, "backend returned no usable model")
		}
	}

	return nil, fmt.Errorf("could not load model - all backends returned error: %s", err.Error())
}
