package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/system"
)

// The Activity page reads finished operations from /api/operations/history.
// The live /api/operations payload must stay exactly as it was: it is polled
// once a second by every open tab.
var _ = Describe("/api/operations/history", func() {
	noopMw := func(next echo.HandlerFunc) echo.HandlerFunc { return next }

	var (
		e       *echo.Echo
		svc     *galleryop.GalleryService
		opcache *galleryop.OpCache
	)

	BeforeEach(func() {
		tmpDir, err := os.MkdirTemp("", "ops-history-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(tmpDir)).To(Succeed())
		})

		// /api/operations resolves a live op's name against the backend gallery,
		// which dereferences SystemState. A bare config would panic there.
		appCfg := &config.ApplicationConfig{
			SystemState: system.NewCapabilityState("default",
				system.WithBackendPath(tmpDir), system.WithBackendSystemPath(tmpDir)),
		}
		svc = galleryop.NewGalleryService(appCfg, nil)
		opcache = galleryop.NewOpCache(svc)
		e = echo.New()
		routes.RegisterUIAPIRoutes(e, nil, nil, appCfg, svc, opcache, &application.Application{}, noopMw)
	})

	finish := func(key, jobID string) {
		opcache.Set(key, jobID)
		svc.UpdateStatus(jobID, &galleryop.OpStatus{Processed: true, Progress: 100, Message: "completed"})
		opcache.DeleteUUID(jobID)
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("returns an empty list before anything has run", func() {
		rec := get("/api/operations/history")
		Expect(rec.Code).To(Equal(http.StatusOK))

		var envelope struct {
			Operations []galleryop.OpRecord `json:"operations"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
		Expect(envelope.Operations).To(BeEmpty())
	})

	It("returns finished operations newest first", func() {
		finish("model-one", "job-1")
		finish("model-two", "job-2")

		rec := get("/api/operations/history")
		Expect(rec.Code).To(Equal(http.StatusOK))

		var envelope struct {
			Operations []galleryop.OpRecord `json:"operations"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
		Expect(envelope.Operations).To(HaveLen(2))
		Expect(envelope.Operations[0].Name).To(Equal("model-two"))
		Expect(envelope.Operations[0].Outcome).To(Equal("completed"))
	})

	It("empties the record on DELETE", func() {
		finish("model-one", "job-1")

		req := httptest.NewRequest(http.MethodDelete, "/api/operations/history", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))

		Expect(opcache.History()).To(BeEmpty())
	})

	It("leaves the live operations payload unchanged", func() {
		// Regression guard: the 1s poll must not grow a history key.
		opcache.Set("model-live", "job-live")
		svc.UpdateStatus("job-live", &galleryop.OpStatus{Progress: 42, Cancellable: true})

		rec := get("/api/operations")
		Expect(rec.Code).To(Equal(http.StatusOK))

		var envelope map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &envelope)).To(Succeed())
		Expect(envelope).To(HaveLen(1))
		Expect(envelope).To(HaveKey("operations"))
	})
})
