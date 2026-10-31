package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The chats API is part of the real server: its routes exist and a
// conversation can only be started with a model the instance knows.
var _ = Describe("chats routes in the server", func() {
	It("serves the conversation API and refuses an unknown model", func() {
		dir := GinkgoT().TempDir()
		models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0o750)).To(Succeed())
		Expect(os.Mkdir(backends, 0o750)).To(Succeed())
		state, err := system.GetSystemState(system.WithModelPath(models), system.WithBackendPath(backends))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		localApp, err := application.New(config.WithContext(ctx), config.WithSystemState(state))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = localApp.Shutdown() }()
		app, err := API(localApp)
		Expect(err).NotTo(HaveOccurred())

		list := httptest.NewRecorder()
		app.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/chats", nil))
		Expect(list.Code).To(Equal(http.StatusOK), list.Body.String())
		Expect(list.Body.String()).To(ContainSubstring(`"conversations":[]`))

		create := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/chats", strings.NewReader(`{"model":"no-such-model"}`))
		req.Header.Set("Content-Type", "application/json")
		app.ServeHTTP(create, req)
		Expect(create.Code).To(Equal(http.StatusBadRequest), create.Body.String())
	})
})
