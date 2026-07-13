package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"github.com/phayes/freeport"
	"google.golang.org/protobuf/proto"
)

const (
	LLamaCPP   = "llama-cpp"
	IKLLamaCPP = "ik-llama-cpp"
)

var Aliases = map[string]string{
	"go-llama":               LLamaCPP,
	"llama":                  LLamaCPP,
	"ik_llama":               IKLLamaCPP,
	"ik-llama":               IKLLamaCPP,
	"embedded-store":         LocalStoreBackend,
	"valkey":                 ValkeyStoreBackend,
	"huggingface-embeddings": TransformersBackend,
	"transformers-musicgen":  TransformersBackend,
	"sentencetransformers":   TransformersBackend,
	"mamba":                  TransformersBackend,
	"stablediffusion":        StableDiffusionGGMLBackend,
}

var TypeAlias = map[string]string{
	"sentencetransformers":   "SentenceTransformer",
	"huggingface-embeddings": "SentenceTransformer",
	"mamba":                  "Mamba",
	"transformers-musicgen":  "MusicgenForConditionalGeneration",
}

const (
	WhisperBackend             = "whisper"
	StableDiffusionGGMLBackend = "stablediffusion-ggml"

	TransformersBackend = "transformers"
	LocalStoreBackend   = "local-store"
	ValkeyStoreBackend  = "valkey-store"
)

// starts the grpcModelProcess for the backend, and returns a grpc client
// It also loads the model
func (ml *ModelLoader) grpcModel(backend string, o *Options) func(string, string, string) (*Model, error) {
	return func(modelID, modelName, modelFile string) (*Model, error) {

		xlog.Debug("Loading Model with gRPC", "modelID", modelID, "file", modelFile, "backend", backend, "options", *o)

		// Distributed mode: delegate to the model router if set. No load
		// event is emitted here: this branch runs per inference request and
		// the actual load happens on the worker node.
		ml.mu.Lock()
		router := ml.modelRouter
		ml.mu.Unlock()
		if router != nil {
			xlog.Info("Routing model to remote node via ModelRouter", "modelID", modelID, "backend", backend)
			return router(o.context, backend, modelID, modelName, modelFile, o.gRPCOptions, o.parallelRequests)
		}

		uri := ml.GetAllExternalBackends(o)[backend]
		start := time.Now()
		m, err := ml.spawnGRPCModel(backend, uri, o, modelID, modelName, modelFile)
		ml.notifyLoadObserver(BackendLoadEvent{
			ModelID:    modelID,
			ModelName:  modelName,
			Backend:    backend,
			BackendURI: uri,
			Duration:   time.Since(start),
			Err:        err,
		})
		return m, err
	}
}

// spawnGRPCModel starts the backend process (or attaches to a remote
// address), waits for it to come up, and issues the LoadModel RPC. Reached
// only for actual loads: LoadModel resolves cache hits and coalesces
// concurrent loads before invoking the grpcModel closure. uri is the
// resolved external-backend runtime (empty when the backend isn't
// registered).
func (ml *ModelLoader) spawnGRPCModel(backend, uri string, o *Options, modelID, modelName, modelFile string) (*Model, error) {
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
	if uri != "" {
		xlog.Debug("Loading external backend", "uri", uri)
		// check if uri is a file or an address
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
	for i := range o.grpcAttempts {
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
		stopLoadProcess(client, modelID)
		return nil, fmt.Errorf("grpc service not ready")
	}

	// Clone before setting the per-load fields: o.gRPCOptions is shared by
	// retried/auto-discovery attempts, and a plain struct copy would copy
	// the protobuf message's internal mutex.
	options := proto.Clone(o.gRPCOptions).(*pb.ModelOptions)
	options.Model = modelName
	options.ModelFile = modelFile
	options.ModelPath = ml.ModelPath

	xlog.Debug("GRPC: Loading model with options", "options", options)

	res, err := client.GRPC(o.parallelRequests, ml.wd).LoadModel(o.context, options)
	if err != nil {
		stopLoadProcess(client, modelID)
		return nil, fmt.Errorf("could not load model: %w", err)
	}
	if !res.Success {
		stopLoadProcess(client, modelID)
		return nil, fmt.Errorf("could not load model (no success): %s", res.Message)
	}

	// Register size for size-aware eviction using the caller-supplied estimate
	// (computed via pkg/vram, which handles multi-file and non-GGUF models).
	if ml.wd != nil && o.modelSizeBytes > 0 {
		ml.wd.RegisterModelSize(modelID, o.modelSizeBytes)
	}

	return client, nil
}

// stopLoadProcess tears down a backend process whose load did not complete.
// The stop error is only logged: the load error is what the caller reports.
func stopLoadProcess(client *Model, modelID string) {
	process := client.Process()
	if process == nil {
		return
	}
	if err := process.Stop(); err != nil {
		xlog.Warn("failed to stop backend process after failed load", "error", err, "modelID", modelID)
	}
}

// parallelSlotsFromOptions returns the effective n_parallel from the backend
// option strings ("parallel:N" / "n_parallel:N"), or "1" when unset — the
// llama.cpp default. Used only for the effective-tuning load log.
func parallelSlotsFromOptions(opts []string) string {
	for _, o := range opts {
		k, v, ok := strings.Cut(o, ":")
		if ok && (k == "parallel" || k == "n_parallel") {
			return strings.TrimSpace(v)
		}
	}
	return "1"
}

func (ml *ModelLoader) backendLoader(opts ...Option) (client grpc.Backend, err error) {
	o := NewOptions(opts...)

	xlog.Info("BackendLoader starting", "modelID", o.modelID, "backend", o.backendString, "model", o.model)

	// Surface the effective performance-relevant runtime options at load (some of
	// these are auto-tuned for the detected hardware). Logged once per load so an
	// admin can see what will actually run and pin or override any value in the
	// model YAML — or set LOCALAI_DISABLE_HARDWARE_DEFAULTS=true to turn the
	// hardware auto-tuning off entirely. Gated on an LLM-ish load (context set) so
	// TTS/audio/other backends stay quiet.
	if opt := o.gRPCOptions; opt != nil && opt.ContextSize > 0 {
		xlog.Info("effective runtime tuning (override in the model YAML; LOCALAI_DISABLE_HARDWARE_DEFAULTS=true disables hardware auto-tuning)",
			"modelID", o.modelID,
			"context", opt.ContextSize,
			"n_batch", opt.NBatch,
			"n_gpu_layers", opt.NGPULayers,
			"parallel", parallelSlotsFromOptions(opt.Options),
			"flash_attention", opt.FlashAttention,
			"f16", opt.F16Memory)
	}

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
		// Defensive cleanup: the model usually wasn't registered yet (LoadModel
		// failed before that), so StopGRPC reporting "model not found" is the
		// expected case, not an error. The outer Failed-to-load log below
		// carries the real reason.
		if stopErr := ml.StopGRPC(only(o.modelID)); stopErr != nil {
			xlog.Debug("cleanup stop after failed load", "error", stopErr, "model", o.modelID)
		}
		xlog.Error("Failed to load model", "modelID", o.modelID, "error", err, "backend", o.backendString)
		return nil, err
	}

	return model.GRPC(o.parallelRequests, ml.wd), nil
}

// retryEnforce repeatedly invokes fn until it returns NeedMore=false or the
// retry budget is exhausted. It sleeps `retryInterval` between attempts and
// logs progress under `label`. Used by both LRU and group-exclusivity
// enforcement so the busy-model wait behaviour is identical.
func retryEnforce(fn func() EnforceLRULimitResult, maxRetries int, retryInterval time.Duration, label string) {
	for attempt := range maxRetries {
		result := fn()
		if !result.NeedMore {
			if result.EvictedCount > 0 {
				xlog.Info("[ModelLoader] "+label+" enforcement complete", "evicted", result.EvictedCount)
			}
			return
		}
		if attempt < maxRetries-1 {
			xlog.Info("[ModelLoader] Waiting for busy models to become idle before eviction",
				"label", label,
				"evicted", result.EvictedCount,
				"attempt", attempt+1,
				"maxRetries", maxRetries,
				"retryIn", retryInterval)
			time.Sleep(retryInterval)
		} else {
			xlog.Warn("[ModelLoader] "+label+" enforcement incomplete after max retries",
				"evicted", result.EvictedCount,
				"reason", "conflicts are still busy or pinned")
		}
	}
}

// enforceLRULimit enforces the LRU limit before loading a new model.
// This is called before loading a model to ensure we don't exceed the limit.
// It accounts for models that are currently being loaded by other goroutines.
// If models are busy and can't be evicted, it will wait and retry until space is available.
func (ml *ModelLoader) enforceLRULimit() {
	if ml.wd == nil {
		return
	}

	pendingLoads := ml.GetLoadingCount()

	ml.mu.Lock()
	maxRetries := ml.lruEvictionMaxRetries
	retryInterval := ml.lruEvictionRetryInterval
	ml.mu.Unlock()

	retryEnforce(func() EnforceLRULimitResult {
		return ml.wd.EnforceLRULimit(pendingLoads)
	}, maxRetries, retryInterval, "LRU")
}

// enforceGroupExclusivity evicts every loaded model that shares a concurrency
// group with modelID before loading proceeds. Reuses the LRU retry settings so
// busy conflicts wait for the same window as a busy LRU eviction.
func (ml *ModelLoader) enforceGroupExclusivity(modelID string) {
	if ml.wd == nil {
		return
	}

	ml.mu.Lock()
	maxRetries := ml.lruEvictionMaxRetries
	retryInterval := ml.lruEvictionRetryInterval
	ml.mu.Unlock()

	retryEnforce(func() EnforceLRULimitResult {
		return ml.wd.EnforceGroupExclusivity(modelID)
	}, maxRetries, retryInterval, "group-exclusivity")
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

	ml.mu.Lock()
	distributed := ml.modelRouter != nil
	ml.mu.Unlock()

	// In distributed mode, SmartRouter must run per inference request so
	// PickBestReplica (core/services/nodes/replicapicker.go) picks the
	// least-loaded replica each time. Bypass the local cache and the local
	// LRU / concurrency-group watchdog enforcement: both are scoped to the
	// in-process Model store, which in distributed mode only holds stubs for
	// remote replicas. SmartRouter handles cluster-wide eviction
	// (evictLRUAndFreeNode) and concurrency-group anti-affinity
	// (narrowByGroupAntiAffinity) at the scheduler layer.
	//
	// TODO(distributed-cache): see LoadModel for the rotating-replica-cache
	// integration point that would let hot paths skip the per-request DB
	// round-trip without giving up the shared PickBestReplica policy.
	if distributed {
		client, err := ml.backendLoader(opts...)
		if err != nil {
			return nil, err
		}
		if m := ml.CheckIsLoaded(o.modelID); m != nil && m.Process() == nil {
			client = newConnectionEvictingClient(client, o.modelID, func() {
				if err := ml.ShutdownModel(o.modelID); err != nil {
					xlog.Warn("Failed to shut down remote model after connection error", "model", o.modelID, "error", err)
				}
			})
		}
		return client, nil
	}

	// Return earlier if we have a model already loaded
	// (avoid looping through all the backends)
	if m := ml.CheckIsLoaded(o.modelID); m != nil {
		xlog.Debug("Model already loaded", "model", o.modelID)
		// Update last used time for LRU tracking
		ml.updateModelLastUsed(m)
		client := m.GRPC(o.parallelRequests, ml.wd)
		// Wrap remote models so connection errors during inference trigger eviction
		if m.Process() == nil {
			client = newConnectionEvictingClient(client, o.modelID, func() {
				ml.ShutdownModel(o.modelID)
			})
		}
		return client, nil
	}

	// Evict any loaded model that shares a concurrency group with the
	// requested one before applying the global LRU cap — group eviction may
	// already make room, and otherwise LRU might evict an unrelated model
	// only for the group check to immediately evict another.
	ml.enforceGroupExclusivity(o.modelID)

	// Enforce LRU limit before loading a new model
	ml.enforceLRULimit()

	// if a backend is defined, return the loader directly
	if o.backendString != "" {
		client, err := ml.backendLoader(opts...)
		if err != nil {
			return nil, err
		}
		// Wrap remote models so connection errors during inference trigger eviction
		if m := ml.CheckIsLoaded(o.modelID); m != nil && m.Process() == nil {
			client = newConnectionEvictingClient(client, o.modelID, func() {
				ml.ShutdownModel(o.modelID)
			})
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
			// Wrap remote models so connection errors during inference trigger eviction
			if m := ml.CheckIsLoaded(o.modelID); m != nil && m.Process() == nil {
				model = newConnectionEvictingClient(model, o.modelID, func() {
					ml.ShutdownModel(o.modelID)
				})
			}
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
