package localai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Toggle model endpoint", func() {
	It("always reports the saved revision and pending cleanup count", func() {
		tempDir, err := os.MkdirTemp("", "toggle-model-test-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, tempDir)

		systemState, err := system.GetSystemState(system.WithModelPath(tempDir))
		Expect(err).NotTo(HaveOccurred())
		appConfig := config.NewApplicationConfig(config.WithSystemState(systemState))
		loader := config.NewModelConfigLoader(tempDir)
		Expect(os.WriteFile(filepath.Join(tempDir, "model.yaml"), []byte("name: model\nbackend: llama-cpp\n"), 0o644)).To(Succeed())
		Expect(loader.LoadModelConfigsFromPath(tempDir)).To(Succeed())
		lifecycle := &endpointLifecycleRecorder{}
		app := echo.New()
		app.PUT("/api/models/:name/:action", ToggleStateModelEndpoint(loader, nil, appConfig, lifecycle))

		request := func(action string) map[string]any {
			req := httptest.NewRequest(http.MethodPut, "/api/models/model/"+action, nil).WithContext(context.Background())
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
			var response map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
			return response
		}

		disabled := request("disable")
		Expect(disabled).To(HaveKeyWithValue("config_revision", Not(BeEmpty())))
		Expect(disabled).To(HaveKeyWithValue("pending_cleanup", BeNumerically("==", 0)))

		lifecycle.pendingCleanup = 3
		enabled := request("enable")
		Expect(enabled).To(HaveKeyWithValue("config_revision", Not(BeEmpty())))
		Expect(enabled).To(HaveKeyWithValue("pending_cleanup", BeNumerically("==", 3)))
	})
})
