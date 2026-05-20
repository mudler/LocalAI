package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newRequestApp creates a minimal Echo app with SetModelAndConfig middleware.
func newRequestApp(re *RequestExtractor) *echo.Echo {
	e := echo.New()
	e.POST("/v1/chat/completions",
		func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		},
		re.SetModelAndConfig(func() schema.LocalAIRequest {
			return new(schema.OpenAIRequest)
		}),
	)
	return e
}

func postJSON(e *echo.Echo, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

var _ = Describe("SetModelAndConfig middleware", func() {
	var (
		app      *echo.Echo
		modelDir string
	)

	BeforeEach(func() {
		var err error
		modelDir, err = os.MkdirTemp("", "localai-test-models-*")
		Expect(err).ToNot(HaveOccurred())

		ss := &system.SystemState{
			Model: system.Model{ModelsPath: modelDir},
		}
		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = ss

		mcl := config.NewModelConfigLoader(modelDir)
		ml := model.NewModelLoader(ss)

		re := NewRequestExtractor(mcl, ml, appConfig)
		app = newRequestApp(re)
	})

	AfterEach(func() {
		os.RemoveAll(modelDir)
	})

	Context("when the model does not exist", func() {
		It("returns 404 with a helpful error message", func() {
			rec := postJSON(app, "/v1/chat/completions",
				`{"model":"nonexistent-model","messages":[{"role":"user","content":"hi"}]}`)

			Expect(rec.Code).To(Equal(http.StatusNotFound))

			var resp schema.ErrorResponse
			Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
			Expect(resp.Error).ToNot(BeNil())
			Expect(resp.Error.Message).To(ContainSubstring("nonexistent-model"))
			Expect(resp.Error.Message).To(ContainSubstring("not found"))
			Expect(resp.Error.Type).To(Equal("invalid_request_error"))
		})
	})

	Context("when the model exists as a config file", func() {
		BeforeEach(func() {
			cfgContent := []byte("name: test-model\nbackend: llama-cpp\n")
			err := os.WriteFile(filepath.Join(modelDir, "test-model.yaml"), cfgContent, 0644)
			Expect(err).ToNot(HaveOccurred())
		})

		It("passes through to the handler", func() {
			rec := postJSON(app, "/v1/chat/completions",
				`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`)

			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Context("when the model exists as a pre-loaded config", func() {
		var mcl *config.ModelConfigLoader

		BeforeEach(func() {
			// Simulate a model installed via gallery: config is loaded in memory
			// (not just a YAML file on disk). Recreate the app with the pre-loaded config.
			ss := &system.SystemState{
				Model: system.Model{ModelsPath: modelDir},
			}
			appConfig := config.NewApplicationConfig()
			appConfig.SystemState = ss

			mcl = config.NewModelConfigLoader(modelDir)
			// Pre-load a config as if installed via gallery
			cfgContent := []byte("name: gallery-model\nbackend: llama-cpp\nmodel: gallery-model\n")
			err := os.WriteFile(filepath.Join(modelDir, "gallery-model.yaml"), cfgContent, 0644)
			Expect(err).ToNot(HaveOccurred())
			Expect(mcl.ReadModelConfig(filepath.Join(modelDir, "gallery-model.yaml"))).To(Succeed())

			ml := model.NewModelLoader(ss)
			re := NewRequestExtractor(mcl, ml, appConfig)
			app = newRequestApp(re)
		})

		It("passes through to the handler", func() {
			rec := postJSON(app, "/v1/chat/completions",
				`{"model":"gallery-model","messages":[{"role":"user","content":"hi"}]}`)

			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Context("when the model name contains a slash (HuggingFace ID)", func() {
		It("skips the existence check and passes through", func() {
			rec := postJSON(app, "/v1/chat/completions",
				`{"model":"stabilityai/stable-diffusion-xl-base-1.0","messages":[{"role":"user","content":"hi"}]}`)

			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Context("when no model is specified", func() {
		It("passes through without checking", func() {
			rec := postJSON(app, "/v1/chat/completions",
				`{"messages":[{"role":"user","content":"hi"}]}`)

			// No model name → middleware doesn't reject, handler runs
			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})
})

// ---------------------------------------------------------------------------
// MergeOpenResponsesConfig — tool_choice parsing
// ---------------------------------------------------------------------------
//
// The OpenAI chat/completions spec nests the function name under "function":
//     {"type":"function", "function":{"name":"my_function"}}
// The legacy Anthropic-compat shape puts it at the top level:
//     {"type":"function", "name":"my_function"}
// Both need to reach SetFunctionCallNameString (not SetFunctionCallString,
// which is the mode field "none"/"auto"/"required").
//
// These specs assert both shapes populate the specific-function name and that
// downstream predicates (ShouldCallSpecificFunction, FunctionToCall) return
// the expected values so grammar-based forcing actually engages.
var _ = Describe("MergeOpenResponsesConfig tool_choice parsing", func() {
	var cfg *config.ModelConfig

	BeforeEach(func() {
		cfg = &config.ModelConfig{}
	})

	Context("string tool_choice", func() {
		It("sets mode to required for tool_choice=\"required\"", func() {
			req := &schema.OpenResponsesRequest{ToolChoice: "required"}
			Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

			// "required" is a mode, not a specific function.
			Expect(cfg.ShouldCallSpecificFunction()).To(BeFalse())
			// ShouldUseFunctions must be true so tools are sent to the model.
			Expect(cfg.ShouldUseFunctions()).To(BeTrue())
		})

		It("leaves config untouched for tool_choice=\"auto\"", func() {
			req := &schema.OpenResponsesRequest{ToolChoice: "auto"}
			Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

			Expect(cfg.ShouldCallSpecificFunction()).To(BeFalse())
			Expect(cfg.FunctionToCall()).To(Equal(""))
		})

		It("leaves config untouched for tool_choice=\"none\"", func() {
			req := &schema.OpenResponsesRequest{ToolChoice: "none"}
			Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

			Expect(cfg.ShouldCallSpecificFunction()).To(BeFalse())
			Expect(cfg.FunctionToCall()).To(Equal(""))
		})
	})

	Context("specific-function tool_choice (OpenAI spec shape)", func() {
		It("parses {type:function, function:{name:...}} and sets the specific-function name", func() {
			req := &schema.OpenResponsesRequest{
				ToolChoice: map[string]any{
					"type":     "function",
					"function": map[string]any{"name": "get_weather"},
				},
			}
			Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

			// This is the key invariant the fix restores: a correctly-formed
			// OpenAI tool_choice must result in ShouldCallSpecificFunction()=true.
			Expect(cfg.ShouldCallSpecificFunction()).To(BeTrue())
			Expect(cfg.FunctionToCall()).To(Equal("get_weather"))
		})

		It("prefers the nested function.name over a stray top-level name", func() {
			// Defense-in-depth: both shapes present, OpenAI spec wins.
			req := &schema.OpenResponsesRequest{
				ToolChoice: map[string]any{
					"type":     "function",
					"function": map[string]any{"name": "correct_name"},
					"name":     "legacy_name",
				},
			}
			Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

			Expect(cfg.FunctionToCall()).To(Equal("correct_name"))
		})
	})

	Context("specific-function tool_choice (legacy Anthropic-compat shape)", func() {
		It("parses {type:function, name:...} and sets the specific-function name", func() {
			req := &schema.OpenResponsesRequest{
				ToolChoice: map[string]any{
					"type": "function",
					"name": "get_weather",
				},
			}
			Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

			Expect(cfg.ShouldCallSpecificFunction()).To(BeTrue())
			Expect(cfg.FunctionToCall()).To(Equal("get_weather"))
		})
	})

	Context("malformed tool_choice", func() {
		It("is a no-op when type is missing", func() {
			req := &schema.OpenResponsesRequest{
				ToolChoice: map[string]any{
					"function": map[string]any{"name": "get_weather"},
				},
			}
			Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

			Expect(cfg.ShouldCallSpecificFunction()).To(BeFalse())
		})

		It("is a no-op when type is not \"function\"", func() {
			req := &schema.OpenResponsesRequest{
				ToolChoice: map[string]any{
					"type":     "object",
					"function": map[string]any{"name": "get_weather"},
				},
			}
			Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

			Expect(cfg.ShouldCallSpecificFunction()).To(BeFalse())
		})

		It("is a no-op when name is missing from both shapes", func() {
			req := &schema.OpenResponsesRequest{
				ToolChoice: map[string]any{
					"type":     "function",
					"function": map[string]any{},
				},
			}
			Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

			Expect(cfg.ShouldCallSpecificFunction()).To(BeFalse())
			Expect(cfg.FunctionToCall()).To(Equal(""))
		})

		It("is a no-op when name is empty string", func() {
			req := &schema.OpenResponsesRequest{
				ToolChoice: map[string]any{
					"type":     "function",
					"function": map[string]any{"name": ""},
				},
			}
			Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

			Expect(cfg.ShouldCallSpecificFunction()).To(BeFalse())
		})
	})

	Context("nil tool_choice", func() {
		It("is a no-op", func() {
			req := &schema.OpenResponsesRequest{ToolChoice: nil}
			Expect(MergeOpenResponsesConfig(cfg, req)).To(Succeed())

			Expect(cfg.ShouldCallSpecificFunction()).To(BeFalse())
			Expect(cfg.FunctionToCall()).To(Equal(""))
		})
	})
})

// ---------------------------------------------------------------------------
// SetModelAndConfig + SetOpenAIRequest - /v1/chat/completions tool_choice parsing
// ---------------------------------------------------------------------------
//
// Parallel to the MergeOpenResponsesConfig specs above, but for the chat
// completions path. The parsing block lives in mergeOpenAIRequestAndModelConfig
// (called from SetOpenAIRequest), so these tests drive the full middleware
// chain the way the production /v1/chat/completions route does.
//
// What we assert per shape:
//   - "required"                                  -> ShouldUseFunctions=true,  no specific name
//   - "none"                                      -> ShouldUseFunctions=false (tools disabled)
//   - "auto"                                      -> ShouldUseFunctions=true,  no specific name
//   - {type:function, function:{name:"X"}} (spec) -> ShouldCallSpecificFunction=true, FunctionToCall="X"
//   - {type:function, name:"X"}        (legacy)   -> ShouldCallSpecificFunction=true, FunctionToCall="X"
//   - nested+flat both present                    -> nested wins
//   - malformed (no type / no name)               -> no-op
var _ = Describe("SetModelAndConfig tool_choice parsing (chat completions)", func() {
	var (
		app            *echo.Echo
		modelDir       string
		capturedConfig *config.ModelConfig
	)

	BeforeEach(func() {
		var err error
		modelDir, err = os.MkdirTemp("", "localai-test-models-*")
		Expect(err).ToNot(HaveOccurred())

		cfgContent := []byte("name: test-model\nbackend: llama-cpp\n")
		Expect(os.WriteFile(filepath.Join(modelDir, "test-model.yaml"), cfgContent, 0644)).To(Succeed())

		ss := &system.SystemState{
			Model: system.Model{ModelsPath: modelDir},
		}
		appConfig := config.NewApplicationConfig()
		appConfig.SystemState = ss

		mcl := config.NewModelConfigLoader(modelDir)
		ml := model.NewModelLoader(ss)
		re := NewRequestExtractor(mcl, ml, appConfig)

		capturedConfig = nil
		app = echo.New()
		app.POST("/v1/chat/completions",
			func(c echo.Context) error {
				if cfg, ok := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig); ok {
					capturedConfig = cfg
				}
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

	// chatReq wraps a tool_choice JSON fragment in a minimal valid chat-completions
	// payload. The tools array is non-empty so downstream code paths that gate on
	// len(input.Functions) see something to work with.
	chatReq := func(toolChoiceJSON string) string {
		return `{"model":"test-model",` +
			`"messages":[{"role":"user","content":"hi"}],` +
			`"tools":[{"type":"function","function":{"name":"get_weather"}}],` +
			`"tool_choice":` + toolChoiceJSON + `}`
	}

	Context("string tool_choice", func() {
		It("engages mode for tool_choice=\"required\"", func() {
			rec := postJSON(app, "/v1/chat/completions", chatReq(`"required"`))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeFalse())
			Expect(capturedConfig.ShouldUseFunctions()).To(BeTrue())
		})

		It("disables tools for tool_choice=\"none\"", func() {
			// Before #9559 this was a silent no-op (json.Unmarshal of "none"
			// into functions.Tool failed); now "none" is honored per OpenAI spec.
			rec := postJSON(app, "/v1/chat/completions", chatReq(`"none"`))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.ShouldUseFunctions()).To(BeFalse())
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeFalse())
		})

		It("leaves config untouched for tool_choice=\"auto\"", func() {
			rec := postJSON(app, "/v1/chat/completions", chatReq(`"auto"`))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeFalse())
			// "auto" is the default: tools available, model decides.
			Expect(capturedConfig.ShouldUseFunctions()).To(BeTrue())
			Expect(capturedConfig.FunctionToCall()).To(Equal(""))
		})
	})

	Context("specific-function tool_choice (OpenAI spec shape)", func() {
		It("parses {type:function, function:{name:...}} and forces the named function", func() {
			rec := postJSON(app, "/v1/chat/completions",
				chatReq(`{"type":"function","function":{"name":"get_weather"}}`))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			// Key invariant: a correctly-formed OpenAI tool_choice must engage
			// grammar-based forcing via SetFunctionCallNameString.
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeTrue())
			Expect(capturedConfig.FunctionToCall()).To(Equal("get_weather"))
		})

		It("prefers the nested function.name over a stray top-level name", func() {
			rec := postJSON(app, "/v1/chat/completions",
				chatReq(`{"type":"function","function":{"name":"correct_name"},"name":"legacy_name"}`))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.FunctionToCall()).To(Equal("correct_name"))
		})
	})

	Context("specific-function tool_choice (legacy Anthropic-compat shape)", func() {
		It("parses {type:function, name:...} and forces the named function", func() {
			rec := postJSON(app, "/v1/chat/completions",
				chatReq(`{"type":"function","name":"get_weather"}`))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeTrue())
			Expect(capturedConfig.FunctionToCall()).To(Equal("get_weather"))
		})
	})

	// Some non-spec clients send the object form serialized as a JSON string.
	// The pre-#9559 code accepted that by accident; this Context locks in
	// continued tolerance so those clients do not silently regress.
	Context("double-encoded tool_choice (JSON string of an object, non-spec)", func() {
		It("parses a serialized OpenAI-spec nested object", func() {
			// tool_choice value is itself a JSON-encoded string containing the
			// object form. Use json.Marshal of the inner blob so the escapes
			// are correct regardless of the test reader.
			inner := `{"type":"function","function":{"name":"get_weather"}}`
			encoded, err := json.Marshal(inner)
			Expect(err).ToNot(HaveOccurred())
			rec := postJSON(app, "/v1/chat/completions", chatReq(string(encoded)))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeTrue())
			Expect(capturedConfig.FunctionToCall()).To(Equal("get_weather"))
		})

		It("parses a serialized legacy/Anthropic flat object", func() {
			inner := `{"type":"function","name":"get_weather"}`
			encoded, err := json.Marshal(inner)
			Expect(err).ToNot(HaveOccurred())
			rec := postJSON(app, "/v1/chat/completions", chatReq(string(encoded)))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeTrue())
			Expect(capturedConfig.FunctionToCall()).To(Equal("get_weather"))
		})

		It("falls back to mode-string handling when the JSON string parses but has no usable name", func() {
			// A JSON-string that decodes to a map without a function name
			// should not engage specific-function forcing. We expect it to
			// fall through to the mode-string path; the resulting mode is
			// the raw blob (nonsense), but ShouldCallSpecificFunction stays
			// false - the invariant that matters.
			inner := `{"type":"function"}`
			encoded, err := json.Marshal(inner)
			Expect(err).ToNot(HaveOccurred())
			rec := postJSON(app, "/v1/chat/completions", chatReq(string(encoded)))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeFalse())
		})
	})

	Context("malformed tool_choice", func() {
		It("is a no-op when type is missing", func() {
			rec := postJSON(app, "/v1/chat/completions",
				chatReq(`{"function":{"name":"get_weather"}}`))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeFalse())
		})

		It("is a no-op when type is not \"function\"", func() {
			rec := postJSON(app, "/v1/chat/completions",
				chatReq(`{"type":"object","function":{"name":"get_weather"}}`))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeFalse())
		})

		It("is a no-op when name is missing from both shapes", func() {
			rec := postJSON(app, "/v1/chat/completions",
				chatReq(`{"type":"function","function":{}}`))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeFalse())
			Expect(capturedConfig.FunctionToCall()).To(Equal(""))
		})

		It("is a no-op when name is empty string", func() {
			rec := postJSON(app, "/v1/chat/completions",
				chatReq(`{"type":"function","function":{"name":""}}`))

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeFalse())
		})
	})

	Context("nil tool_choice", func() {
		It("is a no-op", func() {
			rec := postJSON(app, "/v1/chat/completions",
				`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`)

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(capturedConfig).ToNot(BeNil())
			Expect(capturedConfig.ShouldCallSpecificFunction()).To(BeFalse())
			Expect(capturedConfig.FunctionToCall()).To(Equal(""))
		})
	})
})
