package localai_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// InstallBackendOnNodeEndpoint became async to stop blocking the browser on
// the 3-minute NATS reply timeout. These specs lock in the new contract:
// HTTP 202 with a jobID, a ManagementOp enqueued on the gallery channel, and
// an opcache entry keyed by NodeScopedKey so concurrent installs of the same
// backend on different nodes do not stomp each other.
var _ = Describe("InstallBackendOnNodeEndpoint async behavior", func() {
	var (
		e              *echo.Echo
		galleryService *galleryop.GalleryService
		opcache        *galleryop.OpCache
		appCfg         *config.ApplicationConfig
		dispatched     chan galleryop.ManagementOp[gallery.GalleryBackend, any]
		done           chan struct{}
		drainExited    chan struct{}
	)

	BeforeEach(func() {
		e = echo.New()
		appCfg = &config.ApplicationConfig{
			BackendGalleries: []config.Gallery{{Name: "test-gallery", URL: "http://example.com"}},
		}
		galleryService = galleryop.NewGalleryService(appCfg, nil)
		opcache = galleryop.NewOpCache(galleryService)
		// Drain the gallery channel into a buffered side channel so the
		// handler's `go func() { ch <- op }()` send does not block waiting
		// for the real worker (which is not running in this unit test).
		dispatched = make(chan galleryop.ManagementOp[gallery.GalleryBackend, any], 4)
		done = make(chan struct{})
		drainExited = make(chan struct{})
		go func() {
			defer close(drainExited)
			for {
				select {
				case op := <-galleryService.BackendGalleryChannel:
					dispatched <- op
				case <-done:
					return
				}
			}
		}()
	})

	AfterEach(func() {
		// Signal the drain goroutine to exit. We do NOT close
		// BackendGalleryChannel: the handler's dispatch goroutine may still
		// be pending (specs that don't Eventually-Receive), and a send on a
		// closed channel panics. Signalling via `done` lets the drain
		// goroutine return without touching the gallery channel.
		close(done)
		Eventually(drainExited, "2s").Should(BeClosed())
	})

	It("returns 202 with a jobID and dispatches a TargetNodeID-scoped op", func() {
		body := `{"backend": "llama-cpp"}`
		req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-xyz/backends/install", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("node-xyz")

		handler := localai.InstallBackendOnNodeEndpoint(nil, galleryService, opcache, appCfg)
		Expect(handler(c)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusAccepted))

		var resp map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp["jobID"]).To(BeAssignableToTypeOf(""))
		Expect(resp["jobID"].(string)).ToNot(BeEmpty())
		Expect(resp["message"]).To(Equal("backend installation started"))

		Eventually(dispatched, "2s").Should(Receive())
		Expect(opcache.Exists(galleryop.NodeScopedKey("node-xyz", "llama-cpp"))).To(BeTrue())
		Expect(opcache.IsBackendOp(galleryop.NodeScopedKey("node-xyz", "llama-cpp"))).To(BeTrue())
	})

	It("returns 400 when neither backend nor uri is supplied", func() {
		req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-xyz/backends/install", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("node-xyz")

		handler := localai.InstallBackendOnNodeEndpoint(nil, galleryService, opcache, appCfg)
		Expect(handler(c)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("accepts a direct URI install and uses the name as the cache key", func() {
		body := `{"uri": "oci://example.com/custom-backend:v1", "name": "custom"}`
		req := httptest.NewRequest(http.MethodPost, "/api/nodes/node-xyz/backends/install", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("node-xyz")

		handler := localai.InstallBackendOnNodeEndpoint(nil, galleryService, opcache, appCfg)
		Expect(handler(c)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusAccepted))

		Expect(opcache.Exists(galleryop.NodeScopedKey("node-xyz", "custom"))).To(BeTrue())
	})
})
