package model

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/utils"

	"github.com/mudler/xlog"
)

// new idea: what if we declare a struct of these here, and use a loop to check?

// TODO: Split ModelLoader and TemplateLoader? Just to keep things more organized. Left together to share a mutex until I look into that. Would split if we separate directories for .bin/.yaml and .tmpl
// ModelUnloadHook is called when a model is about to be unloaded.
// The model name is passed as the argument.
type ModelUnloadHook func(modelName string)

// RemoteModelUnloader handles unloading models from remote backend nodes.
// In distributed mode, this is implemented by the SmartRouter.
// When ShutdownModel is called for a model with no local process,
// RemoteModelUnloader.UnloadRemoteModel is called to tell the remote node to free it.
type RemoteModelUnloader interface {
	UnloadRemoteModel(modelName string) error
}

// ModelRouter is a callback that routes model loading to a remote node
// instead of starting a local process. When set on the ModelLoader,
// grpcModel() will delegate to this function before attempting local loading.
type ModelRouter func(ctx context.Context, backend, modelID, modelName, modelFile string,
	opts *pb.ModelOptions, parallel bool) (*Model, error)

// BackendLoadEvent describes one actual backend load attempt: a backend
// process spawn (or remote-address attach) followed by its LoadModel RPC.
// Cache hits and loads coalesced onto another goroutine's in-flight attempt
// never produce an event, so observers see real loads only. Distributed-mode
// routing is excluded too: there grpcModel runs per inference request and the
// worker node owns the actual load.
type BackendLoadEvent struct {
	ModelID   string
	ModelName string
	// Backend is the alias-resolved backend string (e.g. "parakeet-cpp").
	Backend string
	// BackendURI is the resolved runtime serving the load: the installed
	// backend's launcher path (which names the variant directory) or a
	// remote gRPC address. This is what identifies WHICH build served the
	// model — a stale installed backend is invisible in the model config
	// but obvious here.
	BackendURI string
	Duration   time.Duration
	Err        error
}

type ModelLoader struct {
	ModelPath                string
	mu                       sync.Mutex
	store                    ModelStore
	loading                  map[string]chan struct{} // tracks models currently being loaded
	wd                       *WatchDog
	externalBackends         map[string]string
	lruEvictionMaxRetries    int           // Maximum number of retries when waiting for busy models
	lruEvictionRetryInterval time.Duration // Interval between retries when waiting for busy models
	onUnloadHooks            []ModelUnloadHook
	loadObserver             func(BackendLoadEvent)
	remoteUnloader           RemoteModelUnloader
	modelRouter              ModelRouter // distributed mode: route to remote node
	backendLogs              *BackendLogStore
	backendLoggingEnabled    atomic.Bool
	// stoppingProcs marks backend processes that LocalAI is stopping on
	// purpose (model unload / graceful shutdown), keyed by the
	// *process.Process pointer. The exit-watcher goroutine in startProcess
	// consults it to decide whether an exit is an expected stop or a crash —
	// the exit code can't, since a child killed by our own SIGTERM/SIGKILL
	// reports -1, indistinguishable from a signal-induced crash.
	stoppingProcs sync.Map
	// loadFailures records, per modelID, the cooldown window applied after a
	// failed load so that a client repeatedly polling a broken model does not
	// spawn (and leak) a fresh backend process on every request. Guarded by mu.
	loadFailures            map[string]*loadFailureState
	loadFailureBaseCooldown time.Duration // first cooldown after a failure
	loadFailureMaxCooldown  time.Duration // cap for the exponential backoff
}

// loadFailureState tracks consecutive load failures for a single modelID and
// the instant at which its cooldown window expires.
type loadFailureState struct {
	consecutive   int
	cooldownUntil time.Time
}

// ModelLoadCooldownError is returned when a model load is skipped because a
// recent attempt failed and the per-model cooldown window has not yet elapsed.
// The HTTP layer maps it to 503 with a Retry-After header so a polling client
// backs off instead of triggering a fresh backend start on every request.
type ModelLoadCooldownError struct {
	ModelID    string
	RetryAfter time.Duration
}

func (e *ModelLoadCooldownError) Error() string {
	return fmt.Sprintf("model %q load is in cooldown after a recent failure; retry after %s",
		e.ModelID, e.RetryAfter.Round(time.Second))
}

// NewModelLoader creates a new ModelLoader instance.
// LRU eviction is now managed through the WatchDog component.
func NewModelLoader(system *system.SystemState) *ModelLoader {
	nml := &ModelLoader{
		ModelPath:                system.Model.ModelsPath,
		store:                    NewInMemoryModelStore(),
		loading:                  make(map[string]chan struct{}),
		externalBackends:         make(map[string]string),
		lruEvictionMaxRetries:    30,              // Default: 30 retries
		lruEvictionRetryInterval: 1 * time.Second, // Default: 1 second
		backendLogs:              NewBackendLogStore(1000),
		loadFailures:             make(map[string]*loadFailureState),
		loadFailureBaseCooldown:  10 * time.Second, // Default: 10s after the first failure
		loadFailureMaxCooldown:   5 * time.Minute,  // Default cap for the backoff
	}

	return nml
}

// SetLoadFailureCooldown configures the per-model load-failure backoff. base is
// the cooldown applied after the first failure; it doubles on each consecutive
// failure up to max. base is authoritative (base <= 0 disables the cooldown
// entirely); max only overrides the current cap when > 0.
func (ml *ModelLoader) SetLoadFailureCooldown(base, max time.Duration) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	if base < 0 {
		base = 0
	}
	ml.loadFailureBaseCooldown = base
	if max > 0 {
		ml.loadFailureMaxCooldown = max
	}
	if ml.loadFailureMaxCooldown < ml.loadFailureBaseCooldown {
		ml.loadFailureMaxCooldown = ml.loadFailureBaseCooldown
	}
}

// cooldownRemaining returns how long the modelID's load cooldown still has to
// run, or 0 if there is none. Callers must hold ml.mu.
func (ml *ModelLoader) cooldownRemaining(modelID string) time.Duration {
	st := ml.loadFailures[modelID]
	if st == nil {
		return 0
	}
	if remaining := time.Until(st.cooldownUntil); remaining > 0 {
		return remaining
	}
	return 0
}

// recordLoadFailure grows the modelID's consecutive-failure count and arms the
// next cooldown window using exponential backoff capped at loadFailureMaxCooldown.
func (ml *ModelLoader) recordLoadFailure(modelID string) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	if ml.loadFailureBaseCooldown <= 0 {
		return // cooldown disabled
	}
	st := ml.loadFailures[modelID]
	if st == nil {
		st = &loadFailureState{}
		ml.loadFailures[modelID] = st
	}
	st.consecutive++
	// base * 2^(consecutive-1), clamped. Cap the shift to avoid overflowing
	// the Duration; anything past the cap collapses to loadFailureMaxCooldown.
	shift := st.consecutive - 1
	if shift > 20 {
		shift = 20
	}
	backoff := ml.loadFailureBaseCooldown * (1 << shift)
	if backoff <= 0 || backoff > ml.loadFailureMaxCooldown {
		backoff = ml.loadFailureMaxCooldown
	}
	st.cooldownUntil = time.Now().Add(backoff)
}

// clearLoadFailure resets the modelID's failure state after a successful load.
// Callers must hold ml.mu.
func (ml *ModelLoader) clearLoadFailure(modelID string) {
	delete(ml.loadFailures, modelID)
}

// GetLoadingCount returns the number of models currently being loaded
func (ml *ModelLoader) GetLoadingCount() int {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	return len(ml.loading)
}

// OnModelUnload registers a hook that is called when a model is unloaded.
func (ml *ModelLoader) OnModelUnload(hook ModelUnloadHook) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.onUnloadHooks = append(ml.onUnloadHooks, hook)
}

func (ml *ModelLoader) SetWatchDog(wd *WatchDog) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.wd = wd
}

// SetLoadObserver registers a callback fired after every actual backend load
// attempt, successful or not. See BackendLoadEvent for what counts as one.
func (ml *ModelLoader) SetLoadObserver(obs func(BackendLoadEvent)) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.loadObserver = obs
}

func (ml *ModelLoader) notifyLoadObserver(ev BackendLoadEvent) {
	ml.mu.Lock()
	obs := ml.loadObserver
	ml.mu.Unlock()
	if obs != nil {
		obs(ev)
	}
}

// SetRemoteUnloader sets the handler for unloading models on remote nodes.
// In distributed mode, this should be set to the SmartRouter adapter.
func (ml *ModelLoader) SetRemoteUnloader(u RemoteModelUnloader) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.remoteUnloader = u
}

// SetModelRouter sets the distributed model router callback.
// When set, grpcModel() will delegate to this function before attempting local loading.
func (ml *ModelLoader) SetModelRouter(r ModelRouter) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.modelRouter = r
}

// SetModelStore replaces the default in-memory model store.
// In distributed mode this is called with a DistributedModelStore.
func (ml *ModelLoader) SetModelStore(s ModelStore) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.store = s
}

func (ml *ModelLoader) GetWatchDog() *WatchDog {
	return ml.wd
}

func (ml *ModelLoader) BackendLogs() *BackendLogStore {
	return ml.backendLogs
}

func (ml *ModelLoader) SetBackendLoggingEnabled(enabled bool) {
	ml.backendLoggingEnabled.Store(enabled)
}

func (ml *ModelLoader) BackendLoggingEnabled() bool {
	return ml.backendLoggingEnabled.Load()
}

// SetLRUEvictionRetrySettings updates the LRU eviction retry settings
func (ml *ModelLoader) SetLRUEvictionRetrySettings(maxRetries int, retryInterval time.Duration) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.lruEvictionMaxRetries = maxRetries
	ml.lruEvictionRetryInterval = retryInterval
}

func (ml *ModelLoader) ExistsInModelPath(s string) bool {
	return utils.ExistsInPath(ml.ModelPath, s)
}

func (ml *ModelLoader) SetExternalBackend(name, uri string) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	ml.externalBackends[name] = uri
}

func (ml *ModelLoader) DeleteExternalBackend(name string) {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	delete(ml.externalBackends, name)
}

func (ml *ModelLoader) GetExternalBackend(name string) string {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	return ml.externalBackends[name]
}

func (ml *ModelLoader) GetAllExternalBackends(o *Options) map[string]string {
	backends := make(map[string]string)
	maps.Copy(backends, ml.externalBackends)
	if o != nil {
		maps.Copy(backends, o.externalBackends)
	}
	return backends
}

var knownFilesToSkip []string = []string{
	"MODEL_CARD",
	"README",
	"README.md",
}

var knownModelsNameSuffixToSkip []string = []string{
	".tmpl",
	".keep",
	".yaml",
	".yml",
	".json",
	".txt",
	".pt",
	".onnx",
	".md",
	".ds_store",
	".",
	".safetensors",
	".bin",
	".gguf",
	".ggml",
	".ckpt",
	".zip",
	".tag",
	".bak",
	".partial",
	".tar.gz",
}

// HasKnownModelFileExtension reports whether name ends in a file extension that
// LocalAI recognizes as a model weight or asset file (e.g. ".gguf",
// ".safetensors", ".json"). It is used to tell a concrete file path such as
// "local/model.gguf" apart from a HuggingFace-style repository ID like
// "org/repo": only the former carries a recognized suffix. A version-style
// suffix such as the ".0" in "stabilityai/stable-diffusion-xl-base-1.0" is not
// in the list, so such repo IDs are correctly treated as non-files.
func HasKnownModelFileExtension(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range knownModelsNameSuffixToSkip {
		// "." is a guard entry consumed by ListFilesInModelPath, not a real
		// extension; skip it so it doesn't match every dotted name.
		if suffix == "." {
			continue
		}
		if strings.HasSuffix(lower, strings.ToLower(suffix)) {
			return true
		}
	}
	return false
}

const retryTimeout = time.Duration(2 * time.Minute)

func (ml *ModelLoader) ListFilesInModelPath() ([]string, error) {
	files, err := os.ReadDir(ml.ModelPath)
	if err != nil {
		return []string{}, err
	}

	models := []string{}
FILE:
	for _, file := range files {

		for _, skip := range knownFilesToSkip {
			if strings.EqualFold(file.Name(), skip) {
				continue FILE
			}
		}

		// Skip templates, YAML, .keep, .json, .DS_Store, and other non-model files.
		// Use case-insensitive matching so e.g. CACHEDIR.TAG is caught by ".tag".
		lowerName := strings.ToLower(file.Name())
		for _, skip := range knownModelsNameSuffixToSkip {
			if strings.HasSuffix(lowerName, skip) {
				continue FILE
			}
		}
		// Skip backup files created by LocalAI or huggingface_hub (e.g. model.yaml.bak-pre-gpumem072).
		if strings.Contains(lowerName, ".bak") {
			continue FILE
		}

		// Skip directories
		if file.IsDir() {
			continue
		}

		models = append(models, file.Name())
	}

	return models, nil
}

func (ml *ModelLoader) ListLoadedModels() []*Model {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	models := []*Model{}
	ml.store.Range(func(_ string, m *Model) bool {
		models = append(models, m)
		return true
	})

	return models
}

func (ml *ModelLoader) LoadModel(modelID, modelName string, loader func(string, string, string) (*Model, error)) (*Model, error) {
	return ml.LoadModelWithFile(modelID, modelName, modelName, loader)
}

func (ml *ModelLoader) LoadModelWithFile(modelID, modelName, modelFileName string, loader func(string, string, string) (*Model, error)) (*Model, error) {
	if modelFileName == "" {
		modelFileName = modelName
	}
	return ml.loadModel(modelID, modelName, modelFileName, loader, true)
}

// loadModel is the implementation behind LoadModelWithFile. checkCooldown gates fresh,
// independent load triggers behind the per-model failure cooldown; it is set to
// false for the coalesced retry of an in-flight burst (a follower whose leader
// just failed), which is not a new trigger and should still get its one retry.
func (ml *ModelLoader) loadModel(modelID, modelName, modelFileName string, loader func(string, string, string) (*Model, error), checkCooldown bool) (*Model, error) {
	ml.mu.Lock()
	distributed := ml.modelRouter != nil
	ml.mu.Unlock()

	if distributed {
		// Distributed mode: SmartRouter must run per inference request so
		// PickBestReplica (core/services/nodes/replicapicker.go) picks the
		// least-loaded replica each time. The cached *Model returned from a
		// previous call holds a client wrapper bound to one (nodeID,
		// replicaIndex), so reusing it pins every subsequent request to the
		// node that won the very first pick — defeating per-replica load
		// balancing. Bypass the cache and the loading-coalesce map; the
		// router does its own coalescing for first-time loads (advisory DB
		// lock + singleflight on backend.install RPC), so concurrent first
		// requests still produce a single worker-side install.
		//
		// TODO(distributed-cache): if profiling shows the per-request
		// FindAndLockNodeWithModel SELECT FOR UPDATE becomes a hot path
		// under burst load, replace this branch with a per-modelID cache
		// that holds a *list* of replicas (refreshed every ~5s in
		// background) and picks per call via PickBestReplica against
		// locally-tracked in-flight counters. Same policy, no DB round-trip
		// per inference. Trade-off: cross-frontend in-flight visibility
		// becomes eventually consistent, acceptable for 1-3 frontend
		// deployments.
		modelFile := filepath.Join(ml.ModelPath, modelFileName)
		model, err := loader(modelID, modelName, modelFile)
		if err != nil {
			return nil, fmt.Errorf("failed to route model with internal loader: %s", err)
		}
		if model == nil {
			return nil, fmt.Errorf("loader didn't return a model")
		}
		// Record the latest mapping so DistributedModelStore.Range, shutdown,
		// and listing endpoints see a representative entry. The DB is the
		// source of truth for cluster-wide state; the local store is just a
		// stub for in-process callers.
		ml.mu.Lock()
		ml.store.Set(modelID, model)
		ml.mu.Unlock()
		return model, nil
	}

	ml.mu.Lock()

	// Check if we already have a loaded model
	if model := ml.checkIsLoaded(modelID); model != nil {
		ml.mu.Unlock()
		return model, nil
	}

	// If a recent load attempt for this model failed, short-circuit fresh load
	// triggers until the cooldown elapses. This stops a client that keeps
	// polling a broken model from spawning (and leaking) a new backend process
	// on every request. The coalesced follower-retry below passes
	// checkCooldown=false so an in-flight burst still gets its one retry.
	if checkCooldown {
		if retryAfter := ml.cooldownRemaining(modelID); retryAfter > 0 {
			ml.mu.Unlock()
			xlog.Debug("Model load in cooldown after a recent failure", "modelID", modelID, "retryAfter", retryAfter)
			return nil, &ModelLoadCooldownError{ModelID: modelID, RetryAfter: retryAfter}
		}
	}

	// Check if another goroutine is already loading this model
	if loadingChan, isLoading := ml.loading[modelID]; isLoading {
		ml.mu.Unlock()
		// Wait for the other goroutine to finish loading
		xlog.Debug("Waiting for model to be loaded by another request", "modelID", modelID)
		<-loadingChan
		// Now check if the model is loaded
		ml.mu.Lock()
		model := ml.checkIsLoaded(modelID)
		ml.mu.Unlock()
		if model != nil {
			return model, nil
		}
		// If still not loaded, the other goroutine failed. Retry once as part of
		// this burst, bypassing the cooldown gate (we are not a new trigger).
		return ml.loadModel(modelID, modelName, modelFileName, loader, false)
	}

	// Mark this model as loading (create a channel that will be closed when done)
	loadingChan := make(chan struct{})
	ml.loading[modelID] = loadingChan
	ml.mu.Unlock()

	// Ensure we clean up the loading state when done
	defer func() {
		ml.mu.Lock()
		delete(ml.loading, modelID)
		close(loadingChan)
		ml.mu.Unlock()
	}()

	// Load the model (this can take a long time, no lock held)
	modelFile := filepath.Join(ml.ModelPath, modelFileName)
	xlog.Debug("Loading model in memory from file", "file", modelFile)

	model, err := loader(modelID, modelName, modelFile)
	if err != nil {
		ml.recordLoadFailure(modelID)
		return nil, fmt.Errorf("failed to load model with internal loader: %s", err)
	}

	if model == nil {
		ml.recordLoadFailure(modelID)
		return nil, fmt.Errorf("loader didn't return a model")
	}

	// Add to models map and reset any prior failure cooldown for this model.
	ml.mu.Lock()
	ml.clearLoadFailure(modelID)
	ml.store.Set(modelID, model)
	ml.mu.Unlock()

	return model, nil
}

func (ml *ModelLoader) ShutdownModel(modelName string) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	return ml.deleteProcess(modelName, false)
}

// ShutdownModelForce stops a backend without waiting for an in-flight gRPC
// call to finish first. It is used by the watchdog's busy-killer, which only
// fires once a backend has been stuck on a call past the busy timeout — the
// graceful ShutdownModel would block forever on that stuck call (while
// holding ml.mu), preventing every other model load. See deleteProcess.
func (ml *ModelLoader) ShutdownModelForce(modelName string) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	return ml.deleteProcess(modelName, true)
}

func (ml *ModelLoader) CheckIsLoaded(s string) *Model {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	return ml.checkIsLoaded(s)
}

func (ml *ModelLoader) checkIsLoaded(s string) *Model {
	m, ok := ml.store.Get(s)
	if !ok {
		return nil
	}

	xlog.Debug("Model already loaded in memory", "model", s)

	// Skip the gRPC health check if the model was recently verified.
	// This avoids serializing concurrent requests behind ml.mu while each
	// one does a network round-trip (especially costly in distributed mode).
	if m.IsRecentlyHealthy() {
		xlog.Debug("Model health check cached, skipping gRPC probe", "model", s)
		return m
	}

	client := m.GRPC(false, ml.wd)

	xlog.Debug("Checking model availability", "model", s)
	cTimeout, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	alive, err := client.HealthCheck(cTimeout)
	if !alive {
		xlog.Warn("GRPC Model not responding", "error", err)
		xlog.Warn("Deleting the process in order to recreate it")
		process := m.Process()
		if process == nil {
			// Remote/distributed model — no local process to check.
			// Only evict on definitive connection errors (node is down).
			// Timeouts may mean the node is busy, so keep the model cached.
			if isConnectionError(err) {
				xlog.Warn("Remote model unreachable (connection error), removing from cache", "model", s, "error", err)
				if delErr := ml.deleteProcess(s, false); delErr != nil {
					xlog.Error("error cleaning up remote model", "error", delErr, "model", s)
				}
				return nil
			}
			xlog.Warn("Remote model health check failed (possible timeout), keeping cached", "model", s, "error", err)
			return m
		}
		if !process.IsAlive() {
			xlog.Debug("GRPC Process is not responding", "model", s)
			// stop and delete the process, this forces to re-load the model and re-create again the service
			err := ml.deleteProcess(s, false)
			if err != nil {
				xlog.Error("error stopping process", "error", err, "process", s)
			}
			return nil
		}
	}

	m.MarkHealthy()
	return m
}
