package localai_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// POST /backends/apply must be idempotent by default: supervising apps call it
// on every boot to ensure a backend exists, and forcing a reinstall there
// re-downloads the whole artifact each time. Reinstall stays available behind
// the explicit force flag.
var _ = Describe("POST /backends/apply force plumbing", func() {
	var (
		app     *echo.Echo
		gs      *galleryop.GalleryService
		tmpDir  string
		received chan galleryop.ManagementOp[gallery.GalleryBackend, any]
	)

	BeforeEach(func() {
		app = echo.New()

		var err error
		tmpDir, err = os.MkdirTemp("", "backends-apply-test-*")
		Expect(err).NotTo(HaveOccurred())

		systemState, err := system.GetSystemState(system.WithBackendPath(tmpDir))
		Expect(err).NotTo(HaveOccurred())
		appConfig := &config.ApplicationConfig{SystemState: systemState}

		// The service is deliberately not started: the test reads the op off
		// the (unbuffered) channel itself.
		gs = galleryop.NewGalleryService(appConfig, model.NewModelLoader(systemState))
		svc := CreateBackendEndpointService(nil, systemState, gs, nil)
		app.POST("/backends/apply", svc.ApplyBackendEndpoint(systemState))

		received = make(chan galleryop.ManagementOp[gallery.GalleryBackend, any], 1)
		go func() {
			op := <-gs.BackendGalleryChannel
			received <- op
		}()
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	apply := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/backends/apply", strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	It("enqueues a non-forced op by default", func() {
		rec := apply(`{"id":"llama-cpp"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var op galleryop.ManagementOp[gallery.GalleryBackend, any]
		Eventually(received).Should(Receive(&op))
		Expect(op.GalleryElementName).To(Equal("llama-cpp"))
		Expect(op.Force).To(BeFalse())
	})

	It("enqueues a forced op when the request sets force", func() {
		rec := apply(`{"id":"llama-cpp","force":true}`)
		Expect(rec.Code).To(Equal(http.StatusOK))

		var op galleryop.ManagementOp[gallery.GalleryBackend, any]
		Eventually(received).Should(Receive(&op))
		Expect(op.GalleryElementName).To(Equal("llama-cpp"))
		Expect(op.Force).To(BeTrue())
	})
})
