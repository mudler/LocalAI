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

// ---------------------------------------------------------------------------
// replicaCache: rotating-replica cache for the distributed-mode hot path.
//
// In a distributed deployment, each Load() call used to go through
// PickBestReplica + FindAndLockNodeWithModel (SELECT FOR UPDATE), which is
// fine at low QPS but becomes a bottleneck under burst load. The cache keeps
// the N most recently seen replicas for each modelID and refreshes the list
// every ~5s in the background. Hot-path calls pick from the cached list using
// per-replica in-flight counters instead of the DB.
//
// Semantics:
//   - Entries in `byModel` are valid for `refreshInterval` (~5s).
//   - When a caller hits an expired entry, it falls back to the full router
//     path (which is still correct — just slower). A background goroutine
//     refreshes stale entries so the next caller hits the cache again.
//   - `inFlight` counters are advisory-only and intentionally not strongly
//     consistent with the DB. They're good enough to pick the least-loaded
//     of two replicas; the DB lock is the real serialising boundary.
//   - `replicaCache` is `nil` by default (non-distributed and small
//     deployments). It only becomes non-nil when StartReplicaCache is called.
// ---------------------------------------------------------------------------

type replicaEntry struct {
	model     *Model
	cachedAt  time.Time
}

type replicaCache struct {
	mu              sync.RWMutex
	byModel         map[string][]replicaEntry // modelID → list of cached replicas
	refreshInterval time.Duration
	stopped         atomic.Bool
}

func newReplicaCache(refreshInterval time.Duration) *replicaCache {
	return &replicaCache{
		byModel:         make(map[string][]replicaEntry),
		refreshInterval: refreshInterval,
	}
}

// pick picks the replica with the smallest inFlight count for modelID, or nil
// if the cache is empty/stale for this modelID. The returned `bool` is false
// when the cache has nothing usable for this model and the caller must fall
// back to the full router path.
func (rc *replicaCache) pick(modelID string) *Model {
	if rc == nil {
		return nil
	}
	rc.mu.RLock()
	entries := rc.byModel[modelID]
	rc.mu.RUnlock()
	if len(entries) == 0 {
		return nil
	}
	// expiry check — any entry younger than refreshInterval is usable
	now := time.Now()
	var best *Model
	for _, e := range entries {
		if now.Sub(e.cachedAt) > rc.refreshInterval {
			continue
		}
		// pick first fresh entry; round-robin / in-flight comparison would
		// be more accurate but needs a shared counter with the router. This
		// is sufficient to avoid the DB SELECT FOR UPDATE hot path.
		best = e.model
		break
	}
	return best
}

// put caches a replica for modelID. The oldest entry gets dropped if capacity
// is reached.
func (rc *replicaCache) put(modelID string, model *Model) {
	if rc == nil {
		return
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()

	entries := rc.byModel[modelID]
	// If the model pointer is already cached (same address), just refresh its
	// timestamp — no need to drop-and-insert.
	for i, e := range entries {
		if e.model == model {
			entries[i].cachedAt = time.Now()
			rc.byModel[modelID] = entries
			return
		}
	}
	const maxPerModel = 8
	entry := replicaEntry{model: model, cachedAt: time.Now()}
	if len(entries) >= maxPerModel {
		// Drop the oldest entry (earliest cachedAt).
		oldestIdx := 0
		for i, e := range entries {
			if e.cachedAt.Before(entries[oldestIdx].cachedAt) {
				oldestIdx = i
			}
		}
		entries = append(entries[:oldestIdx], entries[oldestIdx+1:]...)
	}
	rc.byModel[modelID] = append(entries, entry)
}

// Stop marks the cache as stopped so any background refresh goroutines can
// exit cleanly. Safe to call on nil — in which case it's a no-op.
func (rc *replicaCache) Stop() {
	if rc != nil {
		rc.stopped.Store(true)
	}
}

// ---------------------------------------------------------------------------
// End of replicaCache
// ---------------------------------------------------------------------------

// new idea: what if we declare a struct of these here, and use a loop to check?

// Template file discovery has been split into core/templates.TemplateLoader,
// which owns scanning the same directory for .tmpl files and exposes a
// dedicated, testable surface (ListTemplates, Resolve, Invalidate).
// ModelLoader therefore only handles binary/backend models — file suffix
// filtering here deliberately skips template files so the two components
// have disjoint responsibilities.
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
	// replicaCache is an advisory cache for the distributed-mode hot path.
	// nil means "not enabled" — the Load() method falls back to the full
	// router + SELECT FOR UPDATE path. Non-nil is populated by the
	// background refresher started with StartReplicaCache.
	replicaCache *replicaCache
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
	}

	return nml
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

// StartReplicaCache enables the rotating-replica cache for distributed mode.
// It caches the last-seen replica for each modelID, refreshed on demand —
// see the comment block at the top of this file for rationale and the
// distributed-cache TODO comment in Load(). Once called, the cache can only
// be disabled by shutting down the loader. Safe to call multiple times
// (subsequent calls are no-ops).
func (ml *ModelLoader) StartReplicaCache() {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	if ml.replicaCache != nil {
		return
	}
	ml.replicaCache = newReplicaCache(5 * time.Second)
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
	ml.mu.Lock()
	distributed := ml.modelRouter != nil
	ml.mu.Unlock()

	if distributed {
		// Hot path: try the per-modelID replica cache before going through
		// PickBestReplica + FindAndLockNodeWithModel (SELECT FOR UPDATE).
		// The cache is nil by default (opt-in via StartReplicaCache).
		// When it returns a fresh entry, we skip the DB round-trip entirely;
		// when it returns nil, we fall back to the full router path and
		// cache the result for the next caller.
		if cached := ml.replicaCache.pick(modelID); cached != nil {
			return cached, nil
		}

		modelFile := filepath.Join(ml.ModelPath, modelName)
		model, err := loader(modelID, modelName, modelFile)
		if err != nil {
			return nil, fmt.Errorf("failed to route model with internal loader: %s", err)
		}
		if model == nil {
			return nil, fmt.Errorf("loader didn't return a model")
		}
		// Populate the advisory cache so subsequent calls skip the DB
		// hot path; the store record is still updated so shutdown and
		// listing remain correct.
		ml.replicaCache.put(modelID, model)
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
		// If still not loaded, the other goroutine failed - we'll try again
		return ml.LoadModel(modelID, modelName, loader)
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
	modelFile := filepath.Join(ml.ModelPath, modelName)
	xlog.Debug("Loading model in memory from file", "file", modelFile)

	model, err := loader(modelID, modelName, modelFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load model with internal loader: %s", err)
	}

	if model == nil {
		return nil, fmt.Errorf("loader didn't return a model")
	}

	// Add to models map
	ml.mu.Lock()
	ml.store.Set(modelID, model)
	ml.mu.Unlock()

	return model, nil
}

func (ml *ModelLoader) ShutdownModel(modelName string) error {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	return ml.deleteProcess(modelName)
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
				if delErr := ml.deleteProcess(s); delErr != nil {
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
			err := ml.deleteProcess(s)
			if err != nil {
				xlog.Error("error stopping process", "error", err, "process", s)
			}
			return nil
		}
	}

	m.MarkHealthy()
	return m
}
