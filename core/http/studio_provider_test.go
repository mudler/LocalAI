package http_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The studio loop calls the model through the application's own router, in
// memory. The server must hand its router to the provider once the routes exist.
var _ = Describe("studio provider", func() {
	It("reaches the application's routes through its in-memory transport", func() {
		dir := GinkgoT().TempDir()
		models := filepath.Join(dir, "models")
		backends := filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0o750)).To(Succeed())
		Expect(os.Mkdir(backends, 0o750)).To(Succeed())
		state, err := system.GetSystemState(system.WithModelPath(models), system.WithBackendPath(backends))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		localApp, err := application.New(config.WithContext(ctx), config.WithSystemState(state))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = localApp.Shutdown() }()

		_, err = API(localApp)
		Expect(err).NotTo(HaveOccurred())

		client := localApp.StudioProvider().Transport().Client(nil)
		resp, err := client.Get("http://studio.invalid/healthz")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
	})
})
