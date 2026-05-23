package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

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
})
