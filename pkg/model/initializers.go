package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
	"github.com/phayes/freeport"
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

		xlog.Debug("Loading Model with gRPC", "modelID", modelID, "file", modelFile, "backend", backend, "options", *o)

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
					xlog.Error("unable to set environment variable to modelPath", "error", err, "name", env, "modelPath", ml.ModelPath)
				}
			}
		}

		// Check if the backend is provided as external
		if uri, ok := ml.GetAllExternalBackends(o)[backend]; ok {
			xlog.Debug("Loading external backend", "uri", uri)
			// check if uri is a file or a address
			if fi, err := os.Stat(uri); err == nil {
				xlog.Debug("external backend is file", "file", fi)
				serverAddress, err := getFreeAddress()
				if err != nil {
					return nil, fmt.Errorf("failed allocating free ports: %s", err.Error())
				}
				// Make sure the process is executable
				process, err := ml.startProcess(uri, modelID, serverAddress)
				if err != nil {
					xlog.Error("failed to launch", "error", err, "path", uri)
					return nil, err
				}

				xlog.Debug("GRPC Service Started")

				client = NewModel(modelID, serverAddress, process)
			} else {
				xlog.Debug("external backend is a uri")
				// address
				client = NewModel(modelID, uri, nil)
			}
		} else {
			xlog.Error("Backend not found", "backend", backend)
			return nil, fmt.Errorf("backend not found: %s", backend)
		}

		xlog.Debug("Wait for the service to start up")
		xlog.Debug("Options", "options", o.gRPCOptions)

		// Wait for the service to start up
		ready := false
		for i := 0; i < o.grpcAttempts; i++ {
			alive, err := client.GRPC(o.parallelRequests, ml.wd).HealthCheck(context.Background())
			if alive {
				xlog.Debug("GRPC Service Ready")
				ready = true
				break
			}
			if err != nil && i == o.grpcAttempts-1 {
				xlog.Error("failed starting/connecting to the gRPC service", "error", err)
			}
			time.Sleep(time.Duration(o.grpcAttemptsDelay) * time.Second)
		}

		if !ready {
			xlog.Debug("GRPC Service NOT ready")
			if process := client.Process(); process != nil {
				process.Stop()
			}
			return nil, fmt.Errorf("grpc service not ready")
		}

		options := *o.gRPCOptions
		options.Model = modelName
		options.ModelFile = modelFile
		options.ModelPath = ml.ModelPath

		xlog.Debug("GRPC: Loading model with options", "options", options)

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

	xlog.Info("BackendLoader starting", "modelID", o.modelID, "backend", o.backendString, "model", o.model)

	backend := strings.ToLower(o.backendString)
	if realBackend, exists := Aliases[backend]; exists {
		typeAlias, exists := TypeAlias[backend]
		if exists {
			xlog.Debug("alias is a type alias", "alias", backend, "realBackend", realBackend, "type", typeAlias)
			o.gRPCOptions.Type = typeAlias
		} else {
			xlog.Debug("alias", "alias", backend, "realBackend", realBackend)
		}

		backend = realBackend
	}

	model, err := ml.LoadModel(o.modelID, o.model, ml.grpcModel(backend, o))
	if err != nil {
		if stopErr := ml.StopGRPC(only(o.modelID)); stopErr != nil {
			xlog.Error("error stopping model", "error", stopErr, "model", o.modelID)
		}
		xlog.Error("Failed to load model", "modelID", o.modelID, "error", err, "backend", o.backendString)
		return nil, err
	}

	return model.GRPC(o.parallelRequests, ml.wd), nil
}

// enforceLRULimit enforces the LRU limit before loading a new model.
// This is called before loading a model to ensure we don't exceed the limit.
// It accounts for models that are currently being loaded by other goroutines.
// If models are busy and can't be evicted, it will wait and retry until space is available.
func (ml *ModelLoader) enforceLRULimit() {
	if ml.wd == nil {
		return
	}

	// Get the count of models currently being loaded to account for concurrent requests
	pendingLoads := ml.GetLoadingCount()

	// Get retry settings from ModelLoader
	ml.mu.Lock()
	maxRetries := ml.lruEvictionMaxRetries
	retryInterval := ml.lruEvictionRetryInterval
	ml.mu.Unlock()

	for attempt := 0; attempt < maxRetries; attempt++ {
		result := ml.wd.EnforceLRULimit(pendingLoads)

		if !result.NeedMore {
			// Successfully evicted enough models (or no eviction needed)
			if result.EvictedCount > 0 {
				xlog.Info("[ModelLoader] LRU enforcement complete", "evicted", result.EvictedCount)
			}
			return
		}

		// Need more evictions but models are busy - wait and retry
		if attempt < maxRetries-1 {
			xlog.Info("[ModelLoader] Waiting for busy models to become idle before eviction",
				"evicted", result.EvictedCount,
				"attempt", attempt+1,
				"maxRetries", maxRetries,
				"retryIn", retryInterval)
			time.Sleep(retryInterval)
		} else {
			// Last attempt - log warning but proceed (might fail to load, but at least we tried)
			xlog.Warn("[ModelLoader] LRU enforcement incomplete after max retries",
				"evicted", result.EvictedCount,
				"reason", "models are still busy with active API calls")
		}
	}
}

// updateModelLastUsed updates the last used time for a model (for LRU tracking)
func (ml *ModelLoader) updateModelLastUsed(m *Model) {
	if ml.wd == nil || m == nil {
		return
	}
	ml.wd.UpdateLastUsed(m.address)
}

func (ml *ModelLoader) Load(opts ...Option) (grpc.Backend, error) {
	o := NewOptions(opts...)

	// Return earlier if we have a model already loaded
	// (avoid looping through all the backends)
	if m := ml.CheckIsLoaded(o.modelID); m != nil {
		xlog.Debug("Model already loaded", "model", o.modelID)
		// Update last used time for LRU tracking
		ml.updateModelLastUsed(m)
		return m.GRPC(o.parallelRequests, ml.wd), nil
	}

	// Enforce LRU limit before loading a new model
	ml.enforceLRULimit()

	// if a backend is defined, return the loader directly
	if o.backendString != "" {
		client, err := ml.backendLoader(opts...)
		if err != nil {
			return nil, err
		}
		return client, nil
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
		xlog.Error("No backends found")
		return nil, fmt.Errorf("no backends found")
	}

	xlog.Debug("Loading from the following backends (in order)", "backends", autoLoadBackends)

	xlog.Info("Trying to load the model", "modelID", o.modelID, "backends", autoLoadBackends)

	for _, key := range autoLoadBackends {
		xlog.Info("Attempting to load", "backend", key)
		options := append(opts, []Option{
			WithBackendString(key),
		}...)

		model, modelerr := ml.backendLoader(options...)
		if modelerr == nil && model != nil {
			xlog.Info("Loads OK", "backend", key)
			return model, nil
		} else if modelerr != nil {
			err = errors.Join(err, fmt.Errorf("[%s]: %w", key, modelerr))
			xlog.Info("Fails", "backend", key, "error", modelerr.Error())
		} else if model == nil {
			err = errors.Join(err, fmt.Errorf("backend %s returned no usable model", key))
			xlog.Info("Fails", "backend", key, "error", "backend returned no usable model")
		}
	}

	return nil, fmt.Errorf("could not load model - all backends returned error: %s", err.Error())
}
