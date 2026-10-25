package routes_test

import (
	"context"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	grpcpkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
	"net/http"
	"net/http/httptest"
	"strings"
)

type nativeDecisionFixture struct {
	grpcpkg.Backend
	body string
}

func (*nativeDecisionFixture) HealthCheck(context.Context) (bool, error) { return true, nil }
func (*nativeDecisionFixture) IsBusy() bool                              { return false }
func (*nativeDecisionFixture) Free(context.Context) error                { return nil }
func (b *nativeDecisionFixture) Score(context.Context, *pb.ScoreRequest, ...ggrpc.CallOption) (*pb.ScoreResponse, error) {
	return &pb.ScoreResponse{ResponseJson: b.body}, nil
}

var _ = Describe("registered SystemOne billing", func() {
	DescribeTable("records validated native responses exactly once", func(body string, code int, count int64) {
		root := GinkgoT().TempDir()
		app, err := application.New(config.WithDataPath(root), config.WithDisableLocalAIAssistant(true), config.WithDisableCSRF(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		cfg := config.ModelConfig{Name: "decision", Backend: "llama-cpp", KnownUsecases: config.GetUsecasesFromYAML([]string{"decisions"})}
		cfg.SetDefaults()
		cfg.Model = "fixture.gguf"
		app.ModelConfigLoader().ReplaceModelConfigs([]config.ModelConfig{cfg})
		fixture := &nativeDecisionFixture{body: body}
		app.ModelLoader().SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			return model.NewModelWithClient(id, "test://decision", fixture), nil
		})
		e := echo.New()
		routes.RegisterSystemOneRoutes(e, app)
		req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{"model":"decision","state":"x","questions":{"q":{"type":"noul","instructions":"yes?"}}}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(code), w.Body.String())
		buckets, err := app.StatsRecorder().Aggregate(context.Background(), billing.AggregateQuery{})
		Expect(err).NotTo(HaveOccurred())
		var actual int64
		for _, b := range buckets {
			actual += b.RequestCount
			Expect(b.CompletionTokens).To(Equal(int64(0)))
		}
		Expect(actual).To(Equal(count))
	},
		Entry("positive input", `{"answers":{"q":{"type":"noul","noul":0.5}},"usage":{"input_tokens":12,"output_tokens":0}}`, 200, int64(1)),
		Entry("explicit zeros", `{"answers":{"q":{"type":"noul","noul":0.5}},"usage":{"input_tokens":0,"output_tokens":0}}`, 200, int64(1)),
		Entry("absent usage", `{"answers":{"q":{"type":"noul","noul":0.5}}}`, 200, int64(0)),
		Entry("negative", `{"answers":{"q":{"type":"noul","noul":0.5}},"usage":{"input_tokens":-1,"output_tokens":0}}`, 500, int64(0)),
		Entry("incomplete", `{"answers":{"q":{"type":"noul","noul":0.5}},"usage":{"input_tokens":12}}`, 500, int64(0)),
		Entry("null", "null", 500, int64(0)),
		Entry("oversized valid JSON", `{"answers":{"q":{"type":"noul","noul":0.5}},"usage":{"input_tokens":12,"output_tokens":0},"padding":"`+strings.Repeat("x", 64<<10)+`"}`, 500, int64(0)),
		Entry("empty", `{}`, 500, int64(0)),
		Entry("missing answers", `{"usage":{"input_tokens":12,"output_tokens":0}}`, 500, int64(0)),
	)
})
