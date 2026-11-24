package localai

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// The non-OpenAI endpoints (depth, detection, face_*, voice_*, images, video,
// 3d) return mapBackendError's echo 501 for a backend without the method, not
// the raw gRPC status. The failover retry must still see a capability gap.
var _ = Describe("mapBackendError under a failover chain", func() {
	It("spills a backend's Unimplemented to the next target without tripping it", func() {
		dir := GinkgoT().TempDir()
		write := func(name, body string) {
			Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600)).To(Succeed())
		}
		write("a", "name: a\nbackend: fake-a\n")
		write("b", "name: b\nbackend: fake-b\n")
		write("chain", "name: chain\nfailover:\n  targets:\n    - model: a\n    - model: b\n")

		ss := &system.SystemState{Model: system.Model{ModelsPath: dir}}
		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = ss
		mcl := config.NewModelConfigLoader(dir)
		Expect(mcl.LoadModelConfigsFromPath(dir)).To(Succeed())
		re := middleware.NewRequestExtractor(mcl, model.NewModelLoader(ss), appConfig)
		fm := failover.New(mcl)
		fm.Sync()
		re.SetFailoverManager(fm)

		var calls []string
		handler := func(c echo.Context) error {
			cfg := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
			calls = append(calls, cfg.Name)
			if cfg.Name == "a" {
				return mapBackendError(grpcstatus.Error(codes.Unimplemented, "unimplemented: Detect"))
			}
			return c.JSON(http.StatusOK, map[string]string{"served": cfg.Name})
		}
		app := echo.New()
		app.POST("/v1/detection", handler,
			re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.DetectionRequest) }))

		req := httptest.NewRequest(http.MethodPost, "/v1/detection", strings.NewReader(`{"model":"chain","image":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring(`"served":"b"`))
		Expect(calls).To(Equal([]string{"a", "b"}))
		st, _ := fm.ChainStatus("chain")
		Expect(st.Targets[0].State).To(Equal(failover.StateHealthy))
	})
})
