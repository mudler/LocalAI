package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Request body limits", func() {
	It("lets the remesh route apply its larger limit without weakening other routes", func() {
		dir := GinkgoT().TempDir()
		models := filepath.Join(dir, "models")
		backends := filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0o750)).To(Succeed())
		Expect(os.Mkdir(backends, 0o750)).To(Succeed())

		state, err := system.GetSystemState(
			system.WithModelPath(models),
			system.WithBackendPath(backends),
		)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		localApp, err := application.New(
			config.WithContext(ctx),
			config.WithSystemState(state),
			config.WithUploadLimitMB(1),
		)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = localApp.Shutdown() }()
		app, err := API(localApp)
		Expect(err).NotTo(HaveOccurred())

		body := new(bytes.Buffer)
		writer := multipart.NewWriter(body)
		Expect(writer.WriteField("model", "missing-model")).To(Succeed())
		part, err := writer.CreateFormFile("mesh", "large.glb")
		Expect(err).NotTo(HaveOccurred())
		_, err = part.Write(bytes.Repeat([]byte{'x'}, 2<<20))
		Expect(err).NotTo(HaveOccurred())
		Expect(writer.Close()).To(Succeed())

		request := httptest.NewRequest(http.MethodPost, "/3d/remesh", body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusNotFound), response.Body.String())

		request = httptest.NewRequest(http.MethodPost, "/3d/generations", bytes.NewReader(make([]byte, 2<<20)))
		request.Header.Set("Content-Type", "application/json")
		response = httptest.NewRecorder()
		app.ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusRequestEntityTooLarge), response.Body.String())
	})
})

// The Files page sends a file as the body of one request. Its limit is the
// workspace file size, unless the operator set --upload-limit.
var _ = Describe("Files upload limit", func() {
	const mb = 1 << 20

	newAPI := func(opts ...config.AppOption) *echo.Echo {
		dir := GinkgoT().TempDir()
		models := filepath.Join(dir, "models")
		backends := filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0o750)).To(Succeed())
		Expect(os.Mkdir(backends, 0o750)).To(Succeed())
		state, err := system.GetSystemState(system.WithModelPath(models), system.WithBackendPath(backends))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		base := []config.AppOption{config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(filepath.Join(dir, "data")),
			config.DisableMetricsEndpoint, config.WithDisableLocalAIAssistant(true), config.WithDisableStats(true)}
		localApp, err := application.New(append(base, opts...)...)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(localApp.Shutdown()).To(Succeed()) })
		app, err := API(localApp)
		Expect(err).NotTo(HaveOccurred())
		return app
	}
	upload := func(app *echo.Echo, name string, size int) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPut, "/api/chats/files/content?path=/"+name, bytes.NewReader(make([]byte, size)))
		request.Header.Set("Content-Type", "application/octet-stream")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		return response
	}
	general := func(app *echo.Echo, size int) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/3d/generations", bytes.NewReader(make([]byte, size)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		return response
	}
	maxFileSize := func(app *echo.Echo) float64 {
		request := httptest.NewRequest(http.MethodGet, "/api/chats/files?path=/", nil)
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		var list map[string]any
		Expect(json.Unmarshal(response.Body.Bytes(), &list)).To(Succeed())
		return list["max_file_size"].(float64)
	}

	It("accepts a file above 15 MB up to the workspace size when the operator set nothing, and keeps the other routes at 15 MB", func() {
		app := newAPI()
		Expect(upload(app, "big.bin", 16*mb).Code).To(Equal(http.StatusOK))
		Expect(upload(app, "huge.bin", 257*mb).Code).To(Equal(http.StatusRequestEntityTooLarge))
		Expect(general(app, 16*mb).Code).To(Equal(http.StatusRequestEntityTooLarge))
		Expect(maxFileSize(app)).To(BeNumerically("==", 256*mb))
	})

	It("refuses uploads above an explicit lower limit and reports it", func() {
		app := newAPI(config.WithUploadLimitMB(2))
		Expect(upload(app, "ok.bin", 1*mb).Code).To(Equal(http.StatusOK))
		Expect(upload(app, "big.bin", 3*mb).Code).To(Equal(http.StatusRequestEntityTooLarge))
		Expect(maxFileSize(app)).To(BeNumerically("==", 2*mb))
	})

	It("accepts uploads up to an explicit higher limit", func() {
		app := newAPI(config.WithUploadLimitMB(32))
		Expect(upload(app, "big.bin", 20*mb).Code).To(Equal(http.StatusOK))
		Expect(upload(app, "huge.bin", 33*mb).Code).To(Equal(http.StatusRequestEntityTooLarge))
		Expect(general(app, 20*mb).Code).NotTo(Equal(http.StatusRequestEntityTooLarge))
		Expect(maxFileSize(app)).To(BeNumerically("==", 32*mb))
	})
})
