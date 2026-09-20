// SPDX-License-Identifier: MIT
package localai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	grpcPkg "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ggrpc "google.golang.org/grpc"
)

type animationEndpointBackend struct {
	grpcPkg.Backend
	metadata []byte
	success  bool
	seen     *pb.Animate3DRequest
}

func (*animationEndpointBackend) HealthCheck(context.Context) (bool, error) { return true, nil }
func (*animationEndpointBackend) IsBusy() bool                              { return false }
func (b *animationEndpointBackend) Animate3D(_ context.Context, request *pb.Animate3DRequest, _ ...ggrpc.CallOption) (*pb.Result, error) {
	b.seen = request
	if err := os.WriteFile(request.Dst, []byte("glTF fixture"), 0o600); err != nil {
		return nil, err
	}
	return &pb.Result{Success: b.success, Metadata: b.metadata, Message: "fixture failure"}, nil
}

const animationTestMetadata = `{"usage":{"input_units":32,"output_units":15000,"accounting_rule":"frame_steps_v1","details":{"output_frames":150,"sampling_steps":100}},"custom":true}`

var _ = Describe("3D animation HTTP output", func() {
	DescribeTable("returns an asset and cleans temporary output on base64 or backend failure", func(format string, success bool, responseMetadata string, statsEnabled bool) {
		reportUsage := responseMetadata == animationTestMetadata
		state := &system.SystemState{}
		loader := model.NewModelLoader(state)
		fixture := &animationEndpointBackend{success: success}
		fixture.metadata = []byte(responseMetadata)
		loader.SetModelRouter(func(_ context.Context, id string, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*model.Model, error) {
			return model.NewModelWithClient(id, "test://animation", fixture), nil
		})
		cfg := &config.ModelConfig{Backend: "kimodocpp", Name: "motion"}
		cfg.SetDefaults()
		cfg.Model = "motion.gguf"
		appConfig := config.NewApplicationConfig(config.WithSystemState(state))
		appConfig.GeneratedContentDir = GinkgoT().TempDir()
		appConfig.EnableTracing = true
		request := &schema.Model3DAnimationRequest{BasicModelRequest: schema.BasicModelRequest{Model: "motion"}, ResponseFormat: format,
			Inputs: map[string]schema.AnimationInput{"prompt": {Type: "text", Data: "Walk forward"}}}
		e := echo.New()
		recorder := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/3d/animate", nil), recorder)
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, request)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
		stats := billing.NewMemoryBackend(10)
		DeferCleanup(func() { Expect(stats.Close()).To(Succeed()) })
		var statsRecorder *billing.Recorder
		if statsEnabled {
			statsRecorder = billing.NewRecorder(stats)
		}
		handler := middleware.UsageMiddleware(statsRecorder, &auth.User{ID: "local"})(Model3DAnimationEndpoint(loader, appConfig))
		err := handler(c)
		records, statsErr := stats.Aggregate(context.Background(), billing.AggregateQuery{UserID: "local", Period: "all"})
		Expect(statsErr).NotTo(HaveOccurred())
		if success && reportUsage && statsEnabled {
			Expect(records).To(HaveLen(1))
			Expect(records[0].RequestCount).To(Equal(int64(1)))
			Expect(records[0].PromptTokens).To(Equal(int64(32)))
			Expect(records[0].CompletionTokens).To(Equal(int64(15000)))
			Expect(records[0].TotalTokens).To(Equal(int64(15032)))
		} else {
			Expect(records).To(BeEmpty())
		}
		Expect(fixture.seen).NotTo(BeNil())
		Expect(fixture.seen.ModelIdentity).To(Equal("motion.gguf"))
		files, readErr := filepath.Glob(filepath.Join(appConfig.GeneratedContentDir, "3d", "*"))
		Expect(readErr).NotTo(HaveOccurred())
		if !success {
			Expect(err).To(HaveOccurred())
			Expect(files).To(BeEmpty())
			Expect(c.Get(middleware.ContextKeyPromptTokens)).To(BeNil())
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Expect(recorder.Code).To(Equal(http.StatusOK))
		var response schema.OpenAIResponse
		Expect(json.Unmarshal(recorder.Body.Bytes(), &response)).To(Succeed())
		Expect(response.Data).To(HaveLen(1))
		Expect(response.Model).To(Equal("motion"))
		var body map[string]json.RawMessage
		Expect(json.Unmarshal(recorder.Body.Bytes(), &body)).To(Succeed())
		Expect(body).NotTo(HaveKey("usage"))
		if reportUsage {
			Expect(response.Metadata).To(MatchJSON(fixture.metadata))
			Expect(c.Get(middleware.ContextKeyCompletionTokens)).To(Equal(int64(15000)))
		} else {
			if responseMetadata == `{"custom":true}` {
				Expect(response.Metadata).To(MatchJSON(responseMetadata))
			} else {
				Expect(response.Metadata).To(BeEmpty())
			}
			Expect(c.Get(middleware.ContextKeyPromptTokens)).To(BeNil())
		}
		if format == "b64_json" {
			Expect(response.Data[0].B64JSON).To(Equal(base64.StdEncoding.EncodeToString([]byte("glTF fixture"))))
			Expect(files).To(BeEmpty())
		} else {
			Expect(response.Data[0].URL).To(ContainSubstring("/generated-3d/animation-"))
			Expect(files).To(HaveLen(1))
		}
	}, Entry("URL", "url", true, animationTestMetadata, true), Entry("base64", "b64_json", true, animationTestMetadata, true), Entry("backend failure", "url", false, animationTestMetadata, true), Entry("no metadata", "url", true, "", true),
		Entry("unrelated metadata", "url", true, `{"custom":true}`, true), Entry("invalid JSON", "url", true, `{`, true), Entry("invalid units", "url", true, `{"usage":{"input_units":1,"output_units":-1}}`, true),
		Entry("statistics disabled", "url", true, animationTestMetadata, false))
})

var _ = Describe("3D animation validation", func() {
	var request *schema.Model3DAnimationRequest
	var cfg *config.ModelConfig
	BeforeEach(func() {
		cfg = &config.ModelConfig{Backend: "kimodocpp"}
		request = &schema.Model3DAnimationRequest{Inputs: map[string]schema.AnimationInput{"prompt": {Type: "text", Data: "Walk forward"}}}
	})
	It("accepts text input without an image", func() {
		Expect(validateAnimationRequest(request, cfg)).To(Succeed())
	})
	It("rejects malformed or oversized prompts before model loading", func() {
		for _, text := range []string{strings.Repeat("x", 4097), "walk\x00run", "\xff", "  "} {
			request.Inputs["prompt"] = schema.AnimationInput{Type: "text", Data: text}
			Expect(validateAnimationRequest(request, cfg)).NotTo(Succeed())
		}
	})
	It("rejects unsupported models and input combinations", func() {
		Expect(validateAnimationRequest(request, &config.ModelConfig{Backend: "trellis2cpp"})).NotTo(Succeed())
		request.Inputs["mesh"] = schema.AnimationInput{Type: "mesh", Data: "asset.glb"}
		Expect(validateAnimationRequest(request, cfg)).NotTo(Succeed())
	})
	It("validates parameters and response format before loading weights", func() {
		for _, params := range []map[string]string{{"texture_steps": "12"}, {"steps": "NaN"}, {"frames": "151"}, {"seed": "-1"}} {
			request.Params = params
			Expect(validateAnimationRequest(request, cfg)).NotTo(Succeed())
		}
		request.Params = nil
		request.ResponseFormat = "obj"
		Expect(validateAnimationRequest(request, cfg)).NotTo(Succeed())
	})
	It("matches future compound media inputs without assuming text conditioning", func() {
		requirements := []schema.ThreeDInput{{Name: "source", Type: "mesh", Required: true}, {Name: "motion", Type: "video", Required: true}}
		inputs := map[string]schema.AnimationInput{"source": {Type: "mesh", Data: "mesh.glb"}, "motion": {Type: "video", Data: "motion.mp4"}}
		Expect(animationInputsMatch(inputs, requirements)).To(BeTrue())
		delete(inputs, "motion")
		Expect(animationInputsMatch(inputs, requirements)).To(BeFalse())
	})
})
