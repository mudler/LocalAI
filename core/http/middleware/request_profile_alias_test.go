package middleware_test

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A profile alias resolves to its target's backend process but serves the
// request with the alias's own system prompt and tool policy. These specs drive
// the production middleware chain of /v1/chat/completions.
var _ = Describe("Profile alias through the chat middleware", func() {
	var (
		app            *echo.Echo
		modelDir       string
		capturedConfig *config.ModelConfig
		capturedInput  *schema.OpenAIRequest
	)

	BeforeEach(func() {
		var err error
		modelDir, err = os.MkdirTemp("", "localai-profile-alias-*")
		Expect(err).ToNot(HaveOccurred())

		Expect(os.WriteFile(filepath.Join(modelDir, "base.yaml"),
			[]byte("name: base\nbackend: llama-cpp\nsystem_prompt: base prompt\n"), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelDir, "researcher.yaml"),
			[]byte("name: researcher\nalias: base\nprofile:\n  system_prompt: persona prompt\n  client_tools: drop\n"), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(modelDir, "plain.yaml"),
			[]byte("name: plain\nalias: base\nsystem_prompt: ignored\nclient_tools: drop\n"), 0644)).To(Succeed())

		ss := &system.SystemState{Model: system.Model{ModelsPath: modelDir}}
		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = ss
		mcl := config.NewModelConfigLoader(modelDir)
		Expect(mcl.LoadModelConfigsFromPath(modelDir)).To(Succeed())
		re := NewRequestExtractor(mcl, model.NewModelLoader(ss), appConfig)

		capturedConfig, capturedInput = nil, nil
		app = echo.New()
		app.POST("/v1/chat/completions",
			func(c echo.Context) error {
				capturedConfig, _ = c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
				capturedInput, _ = c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
				return c.String(http.StatusOK, "ok")
			},
			re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
			func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c echo.Context) error {
					if err := re.SetOpenAIRequest(c); err != nil {
						return err
					}
					return next(c)
				}
			},
		)
	})

	AfterEach(func() {
		_ = os.RemoveAll(modelDir)
	})

	withTools := func(model string) string {
		return `{"model":"` + model + `",` +
			`"messages":[{"role":"user","content":"hi"}],` +
			`"tools":[{"type":"function","function":{"name":"client_tool"}}],` +
			`"tool_choice":{"type":"function","function":{"name":"client_tool"}}}`
	}

	It("serves the target process with the alias's prompt and drops client tools", func() {
		rec := postJSON(app, "/v1/chat/completions", withTools("researcher"))

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(capturedConfig).ToNot(BeNil())
		Expect(capturedConfig.ModelID()).To(Equal("base"))
		Expect(capturedConfig.SystemPrompt).To(Equal("persona prompt"))
		Expect(capturedInput.Tools).To(BeEmpty())
		Expect(capturedInput.Functions).To(BeEmpty())
		// The client's forced tool_choice must not survive either.
		Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeFalse())
		// The response still echoes the alias name.
		Expect(capturedInput.Model).To(Equal("researcher"))
	})

	It("leaves an alias without profile block exactly as before, even with top-level fields", func() {
		rec := postJSON(app, "/v1/chat/completions", withTools("plain"))

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(capturedConfig.ModelID()).To(Equal("base"))
		Expect(capturedConfig.SystemPrompt).To(Equal("base prompt"))
		Expect(capturedInput.Functions).To(HaveLen(1))
		Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeTrue())
		Expect(capturedConfig.FunctionToCall()).To(Equal("client_tool"))
	})
})

var _ = Describe("MergeOpenResponsesConfig with client_tools", func() {
	request := func() *schema.OpenResponsesRequest {
		return &schema.OpenResponsesRequest{
			Tools:      []schema.ORFunctionTool{{Type: "function", Name: "client_tool"}},
			ToolChoice: "required",
		}
	}

	It("drops client tools and tool_choice when the config says drop", func() {
		cfg := &config.ModelConfig{ClientTools: config.ClientToolsDrop}
		req := request()
		Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

		Expect(req.Tools).To(BeEmpty())
		Expect(req.ToolChoice).To(BeNil())
	})

	It("keeps client tools by default", func() {
		cfg := &config.ModelConfig{}
		req := request()
		Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

		Expect(req.Tools).To(HaveLen(1))
		Expect(req.ToolChoice).To(Equal("required"))
	})
})
