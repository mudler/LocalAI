package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/privacy"
	"github.com/mudler/LocalAI/pkg/system"
)

// The incognito switch of the chat page, through the real server: the marker
// header, the policy, the chat that is kept out of the list.
var _ = Describe("incognito chats in the server", func() {
	var (
		localApp *application.Application
		app      http.Handler
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0o750)).To(Succeed())
		Expect(os.Mkdir(backends, 0o750)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(models, "m.yaml"), []byte("name: m\nbackend: llama-cpp\nparameters:\n  model: m.gguf\n"), 0o600)).To(Succeed())
		state, err := system.GetSystemState(system.WithModelPath(models), system.WithBackendPath(backends))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		localApp, err = application.New(config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(filepath.Join(dir, "data")))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = localApp.Shutdown() })
		e, err := API(localApp)
		Expect(err).NotTo(HaveOccurred())
		app = e
	})

	send := func(method, path, body, marker string) *httptest.ResponseRecorder {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		if marker != "" {
			req.Header.Set(privacy.Header, marker)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	It("starts an incognito chat that is never listed and says the policy", func() {
		normal := send(http.MethodPost, "/api/chats", `{"model":"m"}`, "")
		Expect(normal.Code).To(Equal(http.StatusCreated), normal.Body.String())
		Expect(normal.Body.String()).NotTo(ContainSubstring(`"incognito"`))

		rec := send(http.MethodPost, "/api/chats", `{"model":"m"}`, "zero-retention")
		Expect(rec.Code).To(Equal(http.StatusCreated), rec.Body.String())
		var chat struct {
			ID        string `json:"id"`
			Incognito string `json:"incognito"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &chat)).To(Succeed())
		Expect(chat.Incognito).To(Equal("zero-retention"))
		Expect(rec.Header().Get(privacy.Header)).To(Equal("zero-retention"))

		list := send(http.MethodGet, "/api/chats", "", "")
		Expect(list.Code).To(Equal(http.StatusOK), list.Body.String())
		Expect(list.Body.String()).NotTo(ContainSubstring(chat.ID))
		Expect(list.Body.String()).To(ContainSubstring(`"incognito":{"policy":"allow","max_level":"zero-retention"}`))

		// The chat keeps its level without the marker: the page cannot lose it.
		got := send(http.MethodGet, "/api/chats/"+chat.ID, "", "")
		Expect(got.Code).To(Equal(http.StatusOK), got.Body.String())
		Expect(got.Body.String()).To(ContainSubstring(`"incognito":"zero-retention"`))

		Expect(send(http.MethodDelete, "/api/chats/"+chat.ID, "", "zero-retention").Code).To(Equal(http.StatusOK))
		Expect(send(http.MethodGet, "/api/chats/"+chat.ID, "", "").Code).To(Equal(http.StatusNotFound))
	})

	It("refuses the marker with 403 when the policy forbids incognito", func() {
		localApp.ApplicationConfig().Incognito = "forbid"
		rec := send(http.MethodPost, "/api/chats", `{"model":"m"}`, "memory-off")
		Expect(rec.Code).To(Equal(http.StatusForbidden), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("incognito"))
		list := send(http.MethodGet, "/api/chats", "", "")
		Expect(list.Body.String()).To(ContainSubstring(`"policy":"forbid"`))
	})

	It("refuses a level above the server's maximum", func() {
		localApp.ApplicationConfig().IncognitoMaxLevelName = "memory-off"
		Expect(send(http.MethodPost, "/api/chats", `{"model":"m"}`, "zero-retention").Code).To(Equal(http.StatusForbidden))
		Expect(send(http.MethodPost, "/api/chats", `{"model":"m"}`, "memory-off").Code).To(Equal(http.StatusCreated))
	})
})
