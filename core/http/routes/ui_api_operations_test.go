package routes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"unsafe"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/system"
)

// parkedModelManager parks inside the operation the worker is running so a
// spec can read /api/operations while that operation is genuinely in flight.
// Without it the terminal status lands before the request is served and the
// running phase is unobservable.
type parkedModelManager struct {
	entered chan string
	release chan struct{}
}

func (m *parkedModelManager) InstallModel(_ context.Context, op *galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig], _ galleryop.ProgressCallback) error {
	m.entered <- op.GalleryElementName
	<-m.release
	return nil
}

func (m *parkedModelManager) DeleteModel(name string) error {
	m.entered <- name
	<-m.release
	return nil
}

func applicationWithDistributedServices(registry *nodes.NodeRegistry, router *nodes.SmartRouter) *application.Application {
	app := &application.Application{}
	field := reflect.ValueOf(app).Elem().FieldByName("distributed")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(&application.DistributedServices{
		Registry: registry,
		Router:   router,
	}))
	return app
}

var _ = Describe("/api/operations with durable staging jobs", func() {
	noopMw := func(next echo.HandlerFunc) echo.HandlerFunc { return next }

	serveOperations := func(app *application.Application) []map[string]any {
		GinkgoHelper()
		appCfg := &config.ApplicationConfig{}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)
		e := echo.New()
		routes.RegisterUIAPIRoutes(e, nil, nil, appCfg, galleryService, opcache, app, noopMw)

		req := httptest.NewRequest(http.MethodGet, "/api/operations", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var envelope struct {
			Operations []map[string]any `json:"operations"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
		return envelope.Operations
	}

	operationByID := func(operations []map[string]any, id string) map[string]any {
		GinkgoHelper()
		for _, operation := range operations {
			if operation["id"] == id {
				return operation
			}
		}
		return nil
	}

	It("includes database-only staging jobs and excludes other load phases", func() {
		db := testutil.SetupTestDB()
		registry, err := nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		router := nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{})

		_, _, err = registry.ClaimLoadJob(context.Background(), "durable-model", "replica-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.UpdateLoadJob(context.Background(), "durable-model", nodes.LoadJobUpdate{
			State: nodes.LoadJobStateStaging, NodeID: "node-1", NodeName: "durable-node",
			BytesSent: 25, TotalBytes: 100, FileIndex: 1, TotalFiles: 1,
		})).To(Succeed())
		_, _, err = registry.ClaimLoadJob(context.Background(), "loading-model", "replica-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.UpdateLoadJob(context.Background(), "loading-model", nodes.LoadJobUpdate{
			State: nodes.LoadJobStateLoading,
		})).To(Succeed())

		operations := serveOperations(applicationWithDistributedServices(registry, router))
		found := operationByID(operations, "staging:durable-model")
		Expect(found).ToNot(BeNil())
		Expect(found).To(SatisfyAll(
			HaveKeyWithValue("name", "durable-model"),
			HaveKeyWithValue("nodeID", "node-1"),
			HaveKeyWithValue("nodeName", "durable-node"),
			HaveKeyWithValue("phase", nodes.LoadJobStateStaging),
			HaveKeyWithValue("progress", float64(25)),
			HaveKeyWithValue("currentBytes", float64(25)),
			HaveKeyWithValue("totalBytes", float64(100)),
		))
		Expect(operationByID(operations, "staging:loading-model")).To(BeNil())
	})

	It("overlays a matching tracker snapshot without duplicating the durable job", func() {
		db := testutil.SetupTestDB()
		registry, err := nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		router := nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{})
		_, _, err = registry.ClaimLoadJob(context.Background(), "overlay-model", "replica-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(registry.UpdateLoadJob(context.Background(), "overlay-model", nodes.LoadJobUpdate{
			State: nodes.LoadJobStateStaging, NodeName: "durable-node", BytesSent: 10, TotalBytes: 100,
		})).To(Succeed())
		router.StagingTracker().Start("overlay-model", "fresh-node", 1)
		router.StagingTracker().UpdateFile("overlay-model", "weights.gguf", 1, 70, 100, "1 MB/s")

		operations := serveOperations(applicationWithDistributedServices(registry, router))
		matches := []map[string]any{}
		for _, operation := range operations {
			if operation["id"] == "staging:overlay-model" {
				matches = append(matches, operation)
			}
		}
		Expect(matches).To(HaveLen(1))
		Expect(matches[0]).To(SatisfyAll(
			HaveKeyWithValue("nodeName", "fresh-node"),
			HaveKeyWithValue("progress", float64(70)),
			HaveKeyWithValue("currentBytes", float64(70)),
			HaveKeyWithValue("totalBytes", float64(100)),
			HaveKeyWithValue("message", ContainSubstring("weights.gguf")),
		))
	})

	It("retains tracker-only operations when the database read fails", func() {
		db := testutil.SetupTestDB()
		registry, err := nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		router := nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{})
		router.StagingTracker().Start("tracker-only", "local-node", 1)
		router.StagingTracker().UpdateFile("tracker-only", "model.gguf", 1, 40, 100, "")

		sqlDB, err := db.DB()
		Expect(err).ToNot(HaveOccurred())
		Expect(sqlDB.Close()).To(Succeed())

		operations := serveOperations(applicationWithDistributedServices(registry, router))
		found := operationByID(operations, "staging:tracker-only")
		Expect(found).ToNot(BeNil())
		Expect(found).To(SatisfyAll(
			HaveKeyWithValue("progress", float64(40)),
			HaveKeyWithValue("currentBytes", float64(40)),
			HaveKeyWithValue("totalBytes", float64(100)),
		))
	})
})

// These specs guard the contract between the opcache (which stores
// node-scoped backend installs under a "node:<nodeID>:<backend>" key) and the
// /api/operations response surface the React UI polls. Without nodeID
// extraction the panel would show the raw prefixed key and have no way to
// label which worker an install is targeting.
var _ = Describe("/api/operations with node-scoped backend ops", func() {
	// We pass a zero-value *application.Application because the handler's
	// distributed-services branch guards on a nil check on the returned
	// *DistributedServices, which is nil for a fresh Application{}.
	noopMw := func(next echo.HandlerFunc) echo.HandlerFunc { return next }

	// operationByJobID serves GET /api/operations and returns the single
	// operation carrying jobID, or nil if the endpoint did not list it.
	operationByJobID := func(appCfg *config.ApplicationConfig, svc *galleryop.GalleryService, opcache *galleryop.OpCache, jobID string) map[string]any {
		GinkgoHelper()
		e := echo.New()
		routes.RegisterUIAPIRoutes(e, nil, nil, appCfg, svc, opcache, &application.Application{}, noopMw)
		req := httptest.NewRequest(http.MethodGet, "/api/operations", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var envelope struct {
			Operations []map[string]any `json:"operations"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
		for _, op := range envelope.Operations {
			if op["jobID"] == jobID {
				return op
			}
		}
		return nil
	}

	It("emits nodeID and the un-prefixed backend name for keys built by NodeScopedKey", func() {
		appCfg := &config.ApplicationConfig{}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)

		key := galleryop.NodeScopedKey("worker-7", "llama-cpp")
		opcache.SetBackend(key, "job-uuid-123")

		e := echo.New()
		routes.RegisterUIAPIRoutes(e, nil, nil, appCfg, galleryService, opcache, &application.Application{}, noopMw)

		req := httptest.NewRequest(http.MethodGet, "/api/operations", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))

		// The handler wraps operations in {"operations": [...]}.
		var envelope struct {
			Operations []map[string]any `json:"operations"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())

		var found map[string]any
		for _, op := range envelope.Operations {
			if op["jobID"] == "job-uuid-123" {
				found = op
				break
			}
		}
		Expect(found).ToNot(BeNil(), "node-scoped op should appear in /api/operations")
		Expect(found["nodeID"]).To(Equal("worker-7"))
		Expect(found["name"]).To(Equal("llama-cpp"))
		Expect(found["isBackend"]).To(Equal(true))
	})

	It("surfaces per-node OpStatus entries on /api/operations", func() {
		appCfg := &config.ApplicationConfig{}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)

		jobID := "test-op-nodes-1"
		// Register a backend op so the handler treats this as a backend
		// install (no need to consult the gallery during the test).
		opcache.SetBackend("vllm", jobID)

		// Populate per-node entries via the P4.2 helper. The helper also
		// allocates an OpStatus under jobID, which the handler will read.
		galleryService.UpdateNodeProgress(jobID, "node-b", galleryop.NodeProgress{
			NodeID: "node-b", NodeName: "worker-b", Status: galleryop.NodeStatusRunningOnWorker,
		})
		galleryService.UpdateNodeProgress(jobID, "node-a", galleryop.NodeProgress{
			NodeID: "node-a", NodeName: "worker-a", Status: galleryop.NodeStatusDownloading, Percentage: 30, FileName: "vllm.tar",
		})

		e := echo.New()
		routes.RegisterUIAPIRoutes(e, nil, nil, appCfg, galleryService, opcache, &application.Application{}, noopMw)

		req := httptest.NewRequest(http.MethodGet, "/api/operations", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var envelope struct {
			Operations []map[string]any `json:"operations"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())

		var found map[string]any
		for _, op := range envelope.Operations {
			if op["jobID"] == jobID {
				found = op
				break
			}
		}
		Expect(found).ToNot(BeNil(), "operation should appear in /api/operations")
		nodes, ok := found["nodes"].([]any)
		Expect(ok).To(BeTrue(), "operation should have a nodes array")
		Expect(nodes).To(HaveLen(2))

		// Stable sort by node_name: "worker-a" comes before "worker-b"
		// even though UpdateNodeProgress was called in reverse order.
		first := nodes[0].(map[string]any)
		Expect(first["node_name"]).To(Equal("worker-a"))
		Expect(first["status"]).To(Equal("downloading"))
		Expect(first["file_name"]).To(Equal("vllm.tar"))
		Expect(first["percentage"]).To(Equal(30.0))

		second := nodes[1].(map[string]any)
		Expect(second["node_name"]).To(Equal("worker-b"))
		Expect(second["status"]).To(Equal("running_on_worker"))
	})

	It("does not emit nodeID for non-node-scoped backend ops", func() {
		appCfg := &config.ApplicationConfig{}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)

		// Legacy/global install path: bare backend name as the opcache key.
		opcache.SetBackend("llama-cpp", "job-uuid-456")

		e := echo.New()
		routes.RegisterUIAPIRoutes(e, nil, nil, appCfg, galleryService, opcache, &application.Application{}, noopMw)

		req := httptest.NewRequest(http.MethodGet, "/api/operations", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
		var envelope struct {
			Operations []map[string]any `json:"operations"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())

		var found map[string]any
		for _, op := range envelope.Operations {
			if op["jobID"] == "job-uuid-456" {
				found = op
				break
			}
		}
		Expect(found).ToNot(BeNil())
		// Critical: bare ops must NOT gain a misleading empty nodeID field.
		Expect(found).ToNot(HaveKey("nodeID"), "non-node-scoped ops must NOT carry a nodeID field")
		Expect(found["name"]).To(Equal("llama-cpp"))
	})

	It("reports an admitted operation the worker has not started as queued", func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		appCfg := &config.ApplicationConfig{SystemState: state}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)

		// Nothing consumes ModelGalleryChannel here, which is exactly the state
		// of an op admitted while the serial worker is mid-install. The op is
		// admitted the way the install handlers admit it, so the spec fails if
		// the queued signal and the admission path ever disagree again.
		jobID := "job-queued-op"
		opcache.Set("localai@qwen3-4b", jobID)
		galleryService.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 jobID,
			GalleryElementName: "localai@qwen3-4b",
		})
		Eventually(func() *galleryop.OpStatus {
			return galleryService.GetStatus(jobID)
		}, "2s", "10ms").ShouldNot(BeNil())

		e := echo.New()
		routes.RegisterUIAPIRoutes(e, nil, nil, appCfg, galleryService, opcache, &application.Application{}, noopMw)
		req := httptest.NewRequest(http.MethodGet, "/api/operations", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var envelope struct {
			Operations []map[string]any `json:"operations"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
		var found map[string]any
		for _, op := range envelope.Operations {
			if op["jobID"] == jobID {
				found = op
				break
			}
		}
		Expect(found).ToNot(BeNil(), "an admitted op must be listed while it waits")
		Expect(found["isQueued"]).To(BeTrue(), "an op the worker has not started must report as queued")
		// Cancelling is what the queued state exists for: an op waiting behind
		// a long install is the one a user most wants to call off.
		Expect(found["cancellable"]).To(BeTrue())
	})

	It("offers cancel on a queued removal", func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		appCfg := &config.ApplicationConfig{SystemState: state}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)

		// Nothing consumes the channel: the removal is admitted and waits, which
		// is the one window in a removal's life where calling it off both works
		// and leaves nothing behind.
		jobID := "job-queued-removal"
		opcache.Set("localai@qwen3-4b", jobID)
		galleryService.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 jobID,
			GalleryElementName: "localai@qwen3-4b",
			Delete:             true,
		})
		Eventually(func() *galleryop.OpStatus {
			return galleryService.GetStatus(jobID)
		}, "2s", "10ms").ShouldNot(BeNil())

		found := operationByJobID(appCfg, galleryService, opcache, jobID)
		Expect(found).ToNot(BeNil(), "an admitted removal must be listed while it waits")
		Expect(found["isQueued"]).To(BeTrue())
		Expect(found["isDeletion"]).To(BeTrue())
		Expect(found["cancellable"]).To(BeTrue(),
			"hiding Cancel here strands the removal behind whatever is installing")
	})

	It("does not offer cancel once a removal is running", func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		appCfg := &config.ApplicationConfig{SystemState: state}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)

		// A real worker running a real handler: the entry status is the thing
		// under test, so it must come from the handler and not from the spec.
		// The manager parks inside the removal to hold the running phase open.
		manager := &parkedModelManager{entered: make(chan string, 1), release: make(chan struct{})}
		galleryService.SetModelManager(manager)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		DeferCleanup(func() { close(manager.release) })
		Expect(galleryService.Start(ctx, nil, nil)).To(Succeed())

		jobID := "job-running-removal"
		opcache.Set("localai@qwen3-4b", jobID)
		galleryService.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 jobID,
			GalleryElementName: "localai@qwen3-4b",
			Delete:             true,
		})
		Eventually(manager.entered, "5s").Should(Receive(Equal("localai@qwen3-4b")))

		found := operationByJobID(appCfg, galleryService, opcache, jobID)
		Expect(found).ToNot(BeNil())
		Expect(found["isQueued"]).To(BeFalse())
		Expect(found["isDeletion"]).To(BeTrue())
		Expect(found["cancellable"]).To(BeFalse(),
			"a Cancel button on a running removal cannot be honoured: DeleteModel takes no context")
	})

	It("does not emit isCancelled, which no live operation can ever be", func() {
		// CancelOperation marks the status processed and the cancel handler
		// deletes the op, so a cancelled operation is gone from this endpoint
		// by the next poll. A field that is structurally always false is a
		// state the UI would branch on and never reach.
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		appCfg := &config.ApplicationConfig{SystemState: state}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)
		jobID := "job-no-cancel-field"
		opcache.Set("qwen-asr", jobID)
		galleryService.UpdateStatus(jobID, &galleryop.OpStatus{Progress: 10, Cancellable: true})

		e := echo.New()
		routes.RegisterUIAPIRoutes(e, nil, nil, appCfg, galleryService, opcache, &application.Application{}, noopMw)
		req := httptest.NewRequest(http.MethodGet, "/api/operations", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var envelope struct {
			Operations []map[string]any `json:"operations"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
		Expect(envelope.Operations).To(HaveLen(1))
		Expect(envelope.Operations[0]).ToNot(HaveKey("isCancelled"))
	})

	It("pauses through the resume-safe operation callback", func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		appCfg := &config.ApplicationConfig{SystemState: state}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)
		opcache.Set("localai@gemma", "job-pause")

		var cancelled, paused bool
		galleryService.StoreCancellationActions(
			"job-pause",
			func() { cancelled = true },
			func() { paused = true },
		)

		e := echo.New()
		routes.RegisterUIAPIRoutes(e, nil, nil, appCfg, galleryService, opcache, &application.Application{}, noopMw)
		req := httptest.NewRequest(http.MethodPost, "/api/operations/job-pause/pause", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(paused).To(BeTrue())
		Expect(cancelled).To(BeFalse())
		Expect(opcache.Get("localai@gemma")).To(BeEmpty())
	})

	It("reports a running removal as a deletion", func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		appCfg := &config.ApplicationConfig{SystemState: state}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)

		// Admitted the way the delete handlers admit it, so the spec fails if
		// admission and this endpoint ever disagree about what a removal is.
		jobID := "job-delete-running"
		opcache.Set("localai@qwen3-4b", jobID)
		galleryService.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 jobID,
			GalleryElementName: "localai@qwen3-4b",
			Delete:             true,
		})
		Eventually(func() *galleryop.OpStatus {
			return galleryService.GetStatus(jobID)
		}, "2s", "10ms").ShouldNot(BeNil())

		// The worker's first write is a fresh OpStatus that says nothing about
		// the kind of job. Only the queued seed ever knew.
		galleryService.UpdateStatus(jobID, &galleryop.OpStatus{Message: "processing model: localai@qwen3-4b", Cancellable: true})

		found := operationByJobID(appCfg, galleryService, opcache, jobID)
		Expect(found).ToNot(BeNil(), "a running removal must be listed")
		Expect(found["isQueued"]).To(BeFalse())
		Expect(found["isDeletion"]).To(BeTrue(), "a running removal must report as a removal, not an install")
		Expect(found["taskType"]).To(Equal("deletion"))
	})

	It("still reports a failed removal as a deletion", func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		appCfg := &config.ApplicationConfig{SystemState: state}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)

		jobID := "job-delete-failed"
		opcache.Set("localai@qwen3-4b", jobID)
		galleryService.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 jobID,
			GalleryElementName: "localai@qwen3-4b",
			Delete:             true,
		})
		Eventually(func() *galleryop.OpStatus {
			return galleryService.GetStatus(jobID)
		}, "2s", "10ms").ShouldNot(BeNil())

		galleryService.UpdateStatus(jobID, &galleryop.OpStatus{Message: "processing model: localai@qwen3-4b", Cancellable: true})
		galleryService.UpdateStatus(jobID, &galleryop.OpStatus{
			Error: errors.New("permission denied"), Processed: true, Message: "error: permission denied",
		})

		found := operationByJobID(appCfg, galleryService, opcache, jobID)
		// This is the one that matters: the UI offers Retry on any failure that
		// is not a removal, and Retry installs. A failed removal that reports
		// isDeletion=false gets a Retry button that re-downloads the model.
		Expect(found).ToNot(BeNil(), "a failed removal stays listed until it is dismissed")
		Expect(found["isDeletion"]).To(BeTrue(), "a failed removal must not be offered a Retry that reinstalls")
		Expect(found["taskType"]).To(Equal("deletion"))
	})

	It("surfaces managed model artifact phase and byte counters", func() {
		state, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).NotTo(HaveOccurred())
		appCfg := &config.ApplicationConfig{SystemState: state}
		galleryService := galleryop.NewGalleryService(appCfg, nil)
		opcache := galleryop.NewOpCache(galleryService)
		jobID := "test-op-artifact-progress"
		opcache.Set("qwen-asr", jobID)
		galleryService.UpdateStatus(jobID, &galleryop.OpStatus{
			Phase: "downloading", CurrentBytes: 1024, TotalBytes: 4096,
			Progress: 22.5, Message: "Downloading model file: weights.safetensors", Cancellable: true,
		})

		e := echo.New()
		routes.RegisterUIAPIRoutes(e, nil, nil, appCfg, galleryService, opcache, &application.Application{}, noopMw)
		req := httptest.NewRequest(http.MethodGet, "/api/operations", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var envelope struct {
			Operations []map[string]any `json:"operations"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
		var found map[string]any
		for _, op := range envelope.Operations {
			if op["jobID"] == jobID {
				found = op
				break
			}
		}
		Expect(found).ToNot(BeNil())
		Expect(found["phase"]).To(Equal("downloading"))
		Expect(found["currentBytes"]).To(Equal(float64(1024)))
		Expect(found["totalBytes"]).To(Equal(float64(4096)))
	})
})
