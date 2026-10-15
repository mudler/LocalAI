// SPDX-License-Identifier: MIT
package routes_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	"github.com/mudler/LocalAI/core/systemone"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
)

var _ = Describe("registered SystemOne multimodal admission", func() {
	DescribeTable("enforces shared limits without billing errors", func(input string, code int, count int64) {
		root := GinkgoT().TempDir()
		app, err := application.New(config.WithDataPath(root), config.WithDisableLocalAIAssistant(true), config.WithDisableCSRF(true), config.WithSystemState(&system.SystemState{Model: system.Model{ModelsPath: root}, Backend: system.Backend{BackendsPath: root}}))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Shutdown()).To(Succeed()) })
		cfg := config.ModelConfig{Name: "decision", Backend: "llama-cpp", KnownUsecases: config.GetUsecasesFromYAML([]string{"decisions"})}
		cfg.SetDefaults()
		cfg.Model = "fixture.gguf"
		app.ModelConfigLoader().ReplaceModelConfigs([]config.ModelConfig{cfg})
		fixture := &nativeDecisionFixture{body: `{"answers":{"q":{"type":"noul","noul":0.5}},"usage":{"input_tokens":12,"output_tokens":0}}`}
		app.ModelLoader().SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			return model.NewModelWithClient(id, "test://decision", fixture), nil
		})
		e := echo.New()
		routes.RegisterSystemOneRoutes(e, app)
		req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(input))
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
		Entry("valid image-only", imageRequest("{}", []string{routePNG(1, 1)}), 200, int64(1)),
		Entry("image-bearing above text limit", imageRequest(`{"text":"`+strings.Repeat("x", systemone.MaxBodyBytes)+`"}`, []string{routePNG(1, 1)}), 200, int64(1)),
		Entry("valid JPEG", imageRequest("{}", []string{routeJPEG(false)}), 200, int64(1)),
		Entry("truncated JPEG", imageRequest("{}", []string{routeJPEG(true)}), 400, int64(0)),
		Entry("corrupt PNG pixels", imageRequest("{}", []string{corruptRoutePNG()}), 400, int64(0)),
		Entry("truncated PNG", imageRequest("{}", []string{truncatedRoutePNG()}), 400, int64(0)),
		Entry("invalid header", imageRequest("{}", []string{"data:image/png;base64,AA=="}), 400, int64(0)),
		Entry("remote URL", imageRequest("{}", []string{"https://example.org/x.png"}), 400, int64(0)),
		Entry("dimensions", imageRequest("{}", []string{routePNG(4097, 1)}), 413, int64(0)),
		Entry("aggregate pixels", imageRequest("{}", []string{routePNG(3000, 3000), routePNG(3000, 3000)}), 413, int64(0)),
		Entry("count", imageRequest("{}", strings.Split(strings.Repeat(routePNG(1, 1)+" ", 9), " ")[:9]), 413, int64(0)),
		Entry("encoded", imageRequest("{}", []string{strings.Repeat("A", systemone.MaxImageEncodedBytes+1)}), 413, int64(0)),
		Entry("decoded", imageRequest("{}", []string{"data:image/png;base64," + strings.Repeat("A", ((systemone.MaxImageDecodedBytes+3)/3)*4)}), 413, int64(0)),
		Entry("empty images keep text cap", imageRequest(`"`+strings.Repeat("x", systemone.MaxBodyBytes)+`"`, []string{}), 413, int64(0)),
		Entry("escaping is not a second wire cap", imageRequest(`"`+strings.Repeat("<", 20000)+`"`, nil), 200, int64(1)),
		Entry("image body cap", imageRequest("{}", []string{routePNG(1, 1)})+strings.Repeat(" ", systemone.MaxImageBodyBytes), 413, int64(0)),
	)
})

func routePNG(w, h int) string {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		panic(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}
func imageRequest(state string, images []string) string {
	raw, _ := json.Marshal(images)
	return `{"model":"decision","state":` + state + `,"images":` + string(raw) + `,"questions":{"q":{"type":"noul"}}}`
}

func truncatedRoutePNG() string {
	raw, _ := base64.StdEncoding.DecodeString(strings.SplitN(routePNG(8, 8), ",", 2)[1])
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw[:33])
}

func routeJPEG(truncated bool) string {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		panic(err)
	}
	raw := b.Bytes()
	if truncated {
		raw = raw[:len(raw)-10]
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw)
}
func corruptRoutePNG() string {
	raw, _ := base64.StdEncoding.DecodeString(strings.SplitN(routePNG(8, 8), ",", 2)[1])
	at := bytes.Index(raw, []byte("IDAT"))
	raw[at+5] ^= 255
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
}
