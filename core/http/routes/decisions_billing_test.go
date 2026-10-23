// SPDX-License-Identifier: MIT
package routes_test

import (
	"context"
	"encoding/json"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
	"strings"
)

var _ = Describe("registered Decisions billing", func() {
	DescribeTable("serves the adapter without auth and records validated usage once", func(body string, code int, count, tokens int64) {
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
		e.Use(auth.Middleware(nil, app.ApplicationConfig()))
		e.Use(auth.RequireRouteFeature(nil))
		routes.RegisterSystemOneRoutes(e, app)
		req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(`{"model":"decision","input":"x","questions":[{"type":"predicate","name":"refund","instructions":"yes?"}]}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		Expect(w.Code).To(Equal(code), w.Body.String())
		if code == http.StatusOK {
			var response schema.DecisionsResponse
			Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
			Expect(response.Answers).To(HaveLen(1))
			Expect(response.Answers[0].Type).To(Equal("predicate"))
			Expect(*response.Answers[0].Name).To(Equal("refund"))
			Expect(*response.Answers[0].Probability).To(Equal(0.5))
			Expect(response.Usage.InputTokens).To(Equal(int(tokens)))
		}
		buckets, err := app.StatsRecorder().Aggregate(context.Background(), billing.AggregateQuery{})
		Expect(err).NotTo(HaveOccurred())
		var actual, prompt int64
		for _, b := range buckets {
			actual += b.RequestCount
			prompt += b.PromptTokens
			Expect(b.CompletionTokens).To(Equal(int64(0)))
		}
		Expect(actual).To(Equal(count))
		Expect(prompt).To(Equal(tokens))
	},
		Entry("positive input", `{"model":"decision","answers":{"q000000":{"type":"noul","noul":0.5}},"usage":{"input_tokens":12,"output_tokens":0}}`, 200, int64(1), int64(12)),
		Entry("explicit zero usage", `{"model":"decision","answers":{"q000000":{"type":"noul","noul":0.5}},"usage":{"input_tokens":0,"output_tokens":0}}`, 200, int64(1), int64(0)),
		Entry("missing usage", `{"model":"decision","answers":{"q000000":{"type":"noul","noul":0.5}}}`, 500, int64(0), int64(0)),
		Entry("invalid adapter answer", `{"model":"decision","answers":{"q000000":{"type":"noul","noul":2}},"usage":{"input_tokens":12,"output_tokens":0}}`, 500, int64(0), int64(0)),
	)
})
