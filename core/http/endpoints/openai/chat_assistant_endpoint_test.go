package openai

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/localaitools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// bareClient is a LocalAIClient with no behaviour: the tools are registered
// from the interface, and these specs never call one.
type bareClient struct{ localaitools.LocalAIClient }

// The OpenAI-compatible chat route gives the admin tools to an administrator
// who asks for them with metadata.localai_assistant, and to nobody else.
var _ = Describe("ChatEndpoint metadata.localai_assistant", func() {
	admin := &auth.User{ID: "a", Role: auth.RoleAdmin}
	user := &auth.User{ID: "u", Role: auth.RoleUser}

	// ask sends the request and returns the handler's error and the tools
	// that reached the backend stage.
	ask := func(cfg *config.ApplicationConfig, who *auth.User, holder *mcpTools.LocalAIAssistantHolder) (error, []string) {
		GinkgoHelper()
		input := &schema.OpenAIRequest{
			PredictionOptions: schema.PredictionOptions{BasicModelRequest: schema.BasicModelRequest{Model: "m"}},
			Messages:          []schema.Message{{Role: "user", Content: "hi", StringContent: "hi"}},
			Metadata:          map[string]string{mcpTools.MetadataKeyLocalAIAssistant: "true"},
		}
		mc := &config.ModelConfig{Name: "m"}
		mc.Compression.Enabled = true
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}")), httptest.NewRecorder())
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, mc)
		if who != nil {
			c.Set("auth_user", who)
		}
		err := ChatEndpoint(nil, nil, nil, cfg, nil, holder, nil, stopCompressor{})(c)
		var names []string
		for _, t := range input.Tools {
			names = append(names, t.Function.Name)
		}
		return err, names
	}
	authOn := func() *config.ApplicationConfig {
		cfg := &config.ApplicationConfig{}
		cfg.Auth.Enabled = true
		return cfg
	}
	holder := func() *mcpTools.LocalAIAssistantHolder {
		h := mcpTools.NewLocalAIAssistantHolder()
		Expect(h.Initialize(GinkgoT().Context(), bareClient{}, localaitools.Options{})).To(Succeed())
		return h
	}
	code := func(err error) int {
		GinkgoHelper()
		httpErr, ok := err.(*echo.HTTPError)
		Expect(ok).To(BeTrue(), "expected echo.HTTPError, got %T: %v", err, err)
		return httpErr.Code
	}

	It("gives an administrator the admin tools and none of the client tools", func() {
		err, names := ask(authOn(), admin, holder())
		Expect(err).To(MatchError(ContainSubstring(errStopAfterTools.Error())))
		Expect(names).To(ContainElement("list_installed_models"))
		for _, t := range localaitools.ClientTools {
			Expect(names).NotTo(ContainElement(t.Tool), "the client tools are not offered on this path")
		}
	})

	It("refuses a non-admin with 403 and offers nothing", func() {
		err, names := ask(authOn(), user, holder())
		Expect(code(err)).To(Equal(http.StatusForbidden))
		Expect(names).To(BeEmpty())
		err, _ = ask(authOn(), nil, holder())
		Expect(code(err)).To(Equal(http.StatusForbidden))
	})

	It("answers 503 to an administrator when the admin tools are switched off in Settings", func() {
		cfg := authOn()
		cfg.DisableChatsLocalAIAdmin = true
		err, names := ask(cfg, admin, holder())
		Expect(code(err)).To(Equal(http.StatusServiceUnavailable))
		Expect(err.(*echo.HTTPError).Message).To(ContainSubstring("switched off"))
		Expect(names).To(BeEmpty())
	})

	It("keeps the 403 for a non-admin when the admin tools are switched off", func() {
		cfg := authOn()
		cfg.DisableChatsLocalAIAdmin = true
		err, _ := ask(cfg, user, holder())
		Expect(code(err)).To(Equal(http.StatusForbidden))
	})
})
