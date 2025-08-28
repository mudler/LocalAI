package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/phayes/freeport"
	"github.com/rs/zerolog/log"
)

const (
	LLamaCPP = "llama-cpp"
)

var Aliases map[string]string = map[string]string{
	"go-llama":               LLamaCPP,
	"llama":                  LLamaCPP,
	"embedded-store":         LocalStoreBackend,
	"huggingface-embeddings": TransformersBackend,
	"langchain-huggingface":  LCHuggingFaceBackend,
	"transformers-musicgen":  TransformersBackend,
	"sentencetransformers":   TransformersBackend,
	"mamba":                  TransformersBackend,
	"stablediffusion":        StableDiffusionGGMLBackend,
}

var TypeAlias map[string]string = map[string]string{
	"sentencetransformers":   "SentenceTransformer",
	"huggingface-embeddings": "SentenceTransformer",
	"mamba":                  "Mamba",
	"transformers-musicgen":  "MusicgenForConditionalGeneration",
}

const (
	WhisperBackend             = "whisper"
	StableDiffusionGGMLBackend = "stablediffusion-ggml"
	LCHuggingFaceBackend       = "huggingface"

	TransformersBackend = "transformers"
	LocalStoreBackend   = "local-store"
)

// starts the grpcModelProcess for the backend, and returns a grpc client
// It also loads the model
func (ml *ModelLoader) grpcModel(backend string, o *Options) func(string, string, string) (*Model, error) {
	return func(modelID, modelName, modelFile string) (*Model, error) {

		log.Debug().Msgf("Loading Model %s with gRPC (file: %s) (backend: %s): %+v", modelID, modelFile, backend, *o)

		var client *Model

		getFreeAddress := func() (string, error) {
			port, err := freeport.GetFreePort()
			if err != nil {
				return "", fmt.Errorf("failed allocating free ports: %s", err.Error())
			}
			return fmt.Sprintf("127.0.0.1:%d", port), nil
		}

		// If no specific model path is set for transformers/HF, set it to the model path
		for _, env := range []string{"HF_HOME", "TRANSFORMERS_CACHE", "HUGGINGFACE_HUB_CACHE"} {
			if os.Getenv(env) == "" {
				err := os.Setenv(env, ml.ModelPath)
				if err != nil {
					log.Error().Err(err).Str("name", env).Str("modelPath", ml.ModelPath).Msg("unable to set environment variable to modelPath")
				}
			}
		}

		// Check if the backend is provided as external
		if uri, ok := ml.GetAllExternalBackends(o)[backend]; ok {
			log.Debug().Msgf("Loading external backend: %s", uri)
			// check if uri is a file or a address
			if fi, err := os.Stat(uri); err == nil {
				log.Debug().Msgf("external backend is file: %+v", fi)
				serverAddress, err := getFreeAddress()
				if err != nil {
					return nil, fmt.Errorf("failed allocating free ports: %s", err.Error())
				}
				// Make sure the process is executable
				process, err := ml.startProcess(uri, modelID, serverAddress)
				if err != nil {
					log.Error().Err(err).Str("path", uri).Msg("failed to launch ")
					return nil, err
				}

				log.Debug().Msgf("GRPC Service Started")

				client = NewModel(modelID, serverAddress, process)
			} else {
				log.Debug().Msg("external backend is a uri")
				// address
				client = NewModel(modelID, uri, nil)
			}
		} else {
			log.Error().Msgf("Backend not found: %s", backend)
			return nil, fmt.Errorf("backend not found: %s", backend)
		}

		log.Debug().Msgf("Wait for the service to start up")
		log.Debug().Msgf("Options: %+v", o.gRPCOptions)

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
				log.Error().Err(err).Msg("failed starting/connecting to the gRPC service")
			}
			time.Sleep(time.Duration(o.grpcAttemptsDelay) * time.Second)
		}

		if !ready {
			log.Debug().Msgf("GRPC Service NOT ready")
			if process := client.Process(); process != nil {
				process.Stop()
			}
			return nil, fmt.Errorf("grpc service not ready")
		}

		options := *o.gRPCOptions
		options.Model = modelName
		options.ModelFile = modelFile
		options.ModelPath = ml.ModelPath

		log.Debug().Msgf("GRPC: Loading model with options: %+v", options)

		res, err := client.GRPC(o.parallelRequests, ml.wd).LoadModel(o.context, &options)
		if err != nil {
			if process := client.Process(); process != nil {
				process.Stop()
			}
			return nil, fmt.Errorf("could not load model: %w", err)
		}
		if !res.Success {
			if process := client.Process(); process != nil {
				process.Stop()
			}
			return nil, fmt.Errorf("could not load model (no success): %s", res.Message)
		}

		return client, nil
	}
}

func (ml *ModelLoader) backendLoader(opts ...Option) (client grpc.Backend, err error) {
	o := NewOptions(opts...)

	log.Info().Str("modelID", o.modelID).Str("backend", o.backendString).Str("o.model", o.model).Msg("BackendLoader starting")

	backend := strings.ToLower(o.backendString)
	if realBackend, exists := Aliases[backend]; exists {
		typeAlias, exists := TypeAlias[backend]
		if exists {
			log.Debug().Msgf("'%s' is a type alias of '%s' (%s)", backend, realBackend, typeAlias)
			o.gRPCOptions.Type = typeAlias
		} else {
			log.Debug().Msgf("'%s' is an alias of '%s'", backend, realBackend)
		}

		backend = realBackend
	}

	model, err := ml.LoadModel(o.modelID, o.model, ml.grpcModel(backend, o))
	if err != nil {
		log.Error().Str("modelID", o.modelID).Err(err).Msgf("Failed to load model %s with backend %s", o.modelID, o.backendString)
		return nil, err
	}

	return model.GRPC(o.parallelRequests, ml.wd), nil
}

func (ml *ModelLoader) stopActiveBackends(modelID string, singleActiveBackend bool) {
	if !singleActiveBackend {
		return
	}

	// If we can have only one backend active, kill all the others (except external backends)

	// Stop all backends except the one we are going to load
	log.Debug().Msgf("Stopping all backends except '%s'", modelID)
	err := ml.StopGRPC(allExcept(modelID))
	if err != nil {
		log.Error().Err(err).Str("keptModel", modelID).Msg("error while shutting down all backends except for the keptModel - greedyloader continuing")
	}
}

func (ml *ModelLoader) Close() {
	if !ml.singletonMode {
		return
	}
	ml.singletonLock.Unlock()
}

func (ml *ModelLoader) lockBackend() {
	if !ml.singletonMode {
		return
	}
	ml.singletonLock.Lock()
}

func (ml *ModelLoader) Load(opts ...Option) (grpc.Backend, error) {
	ml.lockBackend() // grab the singleton lock if needed

	o := NewOptions(opts...)

	// Return earlier if we have a model already loaded
	// (avoid looping through all the backends)
	if m := ml.CheckIsLoaded(o.modelID); m != nil {
		log.Debug().Msgf("Model '%s' already loaded", o.modelID)

		return m.GRPC(o.parallelRequests, ml.wd), nil
	}

	ml.stopActiveBackends(o.modelID, ml.singletonMode)

	// if a backend is defined, return the loader directly
	if o.backendString != "" {
		return ml.backendLoader(opts...)
	}

	// Otherwise scan for backends in the asset directory
	var err error

	// get backends embedded in the binary
	autoLoadBackends := []string{}

	// append externalBackends supplied by the user via the CLI
	for b := range ml.GetAllExternalBackends(o) {
		autoLoadBackends = append(autoLoadBackends, b)
	}

	if len(autoLoadBackends) == 0 {
		log.Error().Msg("No backends found")
		return nil, fmt.Errorf("no backends found")
	}

	log.Debug().Msgf("Loading from the following backends (in order): %+v", autoLoadBackends)

	log.Info().Msgf("Trying to load the model '%s' with the backend '%s'", o.modelID, autoLoadBackends)

	for _, key := range autoLoadBackends {
		log.Info().Msgf("[%s] Attempting to load", key)
		options := append(opts, []Option{
			WithBackendString(key),
		}...)

		model, modelerr := ml.backendLoader(options...)
		if modelerr == nil && model != nil {
			log.Info().Msgf("[%s] Loads OK", key)
			return model, nil
		} else if modelerr != nil {
			err = errors.Join(err, fmt.Errorf("[%s]: %w", key, modelerr))
			log.Info().Msgf("[%s] Fails: %s", key, modelerr.Error())
		} else if model == nil {
			err = errors.Join(err, fmt.Errorf("backend %s returned no usable model", key))
			log.Info().Msgf("[%s] Fails: %s", key, "backend returned no usable model")
		}
	}

	ml.Close() // make sure to release the lock in case of failure

	return nil, fmt.Errorf("could not load model - all backends returned error: %s", err.Error())
}
