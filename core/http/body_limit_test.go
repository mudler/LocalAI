package http_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

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
