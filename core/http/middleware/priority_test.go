package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/priority"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The class an API request is served with comes from service_tier, clamped by
// the caller's role, and never from a context value a network caller could
// set.
var _ = Describe("service_tier and the request priority class", func() {
	var (
		modelDir  string
		appConfig *config.ApplicationConfig
		re        *RequestExtractor
	)

	admin := &auth.User{ID: "admin", Role: auth.RoleAdmin, Status: auth.StatusActive}
	user := &auth.User{ID: "user", Role: auth.RoleUser, Status: auth.StatusActive}

	type result struct {
		Class int    `json:"class"`
		Tier  string `json:"tier"`
	}

	// serve runs a request through SetModelAndConfig and the extractor for
	// path, as u (nil for no user), and returns the class the model call gets
	// and the tier kept on the request.
	serve := func(path, body string, u *auth.User, ctx context.Context) (int, result) {
		e := echo.New()
		var handler echo.HandlerFunc
		var newReq func() schema.LocalAIRequest
		switch path {
		case "/v1/responses":
			newReq = func() schema.LocalAIRequest { return new(schema.OpenResponsesRequest) }
			handler = func(c echo.Context) error {
				if err := re.SetOpenResponsesRequest(c); err != nil {
					return err
				}
				in := c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenResponsesRequest)
				return c.JSON(http.StatusOK, result{Class: int(priority.Of(in.Context)), Tier: in.ServiceTier})
			}
		default:
			newReq = func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }
			handler = func(c echo.Context) error {
				if err := re.SetOpenAIRequest(c); err != nil {
					return err
				}
				in := c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
				return c.JSON(http.StatusOK, result{Class: int(priority.Of(in.Context)), Tier: in.ServiceTier})
			}
		}
		setUser := func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if u != nil {
					c.Set("auth_user", u)
				}
				return next(c)
			}
		}
		e.POST(path, handler, setUser, re.SetModelAndConfig(newReq))

		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if ctx != nil {
			req = req.WithContext(ctx)
		}
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		var out result
		if rec.Code == http.StatusOK {
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		}
		return rec.Code, out
	}

	chat := func(tier string) string {
		if tier == "" {
			return `{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`
		}
		return `{"model":"test-model","service_tier":"` + tier + `","messages":[{"role":"user","content":"hi"}]}`
	}

	BeforeEach(func() {
		var err error
		modelDir, err = os.MkdirTemp("", "localai-priority-models-*")
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(modelDir, "test-model.yaml"),
			[]byte("name: test-model\nbackend: llama-cpp\nknown_usecases:\n    - chat\n"), 0o600)).To(Succeed())

		ss := &system.SystemState{Model: system.Model{ModelsPath: modelDir}}
		appConfig = config.NewApplicationConfig()
		appConfig.SystemState = ss
		// Accounts on: a request without a user may not raise.
		appConfig.Auth.Enabled = true
		re = NewRequestExtractor(config.NewModelConfigLoader(modelDir), model.NewModelLoader(ss), appConfig)
	})

	AfterEach(func() { Expect(os.RemoveAll(modelDir)).To(Succeed()) })

	DescribeTable("chat completions",
		func(tier string, u *auth.User, wantClass int, wantTier string) {
			code, got := serve("/v1/chat/completions", chat(tier), u, nil)
			Expect(code).To(Equal(http.StatusOK))
			Expect(got.Class).To(Equal(wantClass))
			Expect(got.Tier).To(Equal(wantTier))
		},
		Entry("no tier is default and stays unset", "", user, 0, ""),
		Entry("auto is default", "auto", user, 0, "default"),
		Entry("a user may lower with flex", "flex", user, -1, "flex"),
		Entry("a user cannot raise", "priority", user, 0, "default"),
		Entry("an admin may raise", "priority", admin, 1, "priority"),
		Entry("an admin may lower", "flex", admin, -1, "flex"),
	)

	It("applies service_tier to text completions", func() {
		code, got := serve("/v1/completions", `{"model":"test-model","prompt":"hi","service_tier":"flex"}`, user, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(got.Class).To(Equal(-1))
	})

	It("rejects an unknown tier", func() {
		code, _ := serve("/v1/chat/completions", chat("urgent"), admin, nil)
		Expect(code).To(Equal(http.StatusBadRequest))
	})

	It("does not let a request without a user raise when accounts are on", func() {
		_, got := serve("/v1/chat/completions", chat("priority"), nil, nil)
		Expect(got.Class).To(Equal(0))
	})

	It("lets any caller raise on a server with no authentication", func() {
		appConfig.Auth.Enabled = false
		_, got := serve("/v1/chat/completions", chat("priority"), nil, nil)
		Expect(got.Class).To(Equal(1))
	})

	It("does not let a caller with an API key raise when no account is admin", func() {
		appConfig.Auth.Enabled = false
		appConfig.ApiKeys = []string{"k"}
		_, got := serve("/v1/chat/completions", chat("priority"), nil, nil)
		Expect(got.Class).To(Equal(0))
	})

	It("keeps a context class on a server with no authentication", func() {
		appConfig.Auth.Enabled = false
		ctx := priority.WithClass(context.Background(), priority.Batch)
		_, got := serve("/v1/chat/completions", chat(""), nil, ctx)
		Expect(got.Class).To(Equal(-1))
	})

	It("ignores a class in the context of a request that is not the internal user's", func() {
		ctx := priority.WithClass(context.Background(), priority.Interactive)
		_, got := serve("/v1/chat/completions", chat(""), user, ctx)
		Expect(got.Class).To(Equal(0))
	})

	It("keeps the class the internal user's context carries", func() {
		ctx := priority.WithClass(auth.WithInternalUser(context.Background(), user), priority.Batch)
		_, got := serve("/v1/chat/completions", chat("priority"), user, ctx)
		Expect(got.Class).To(Equal(-1))

		ctx = priority.WithClass(auth.WithInternalUser(context.Background(), user), priority.Interactive)
		_, got = serve("/v1/chat/completions", chat(""), user, ctx)
		Expect(got.Class).To(Equal(1))
	})

	DescribeTable("responses",
		func(tier string, u *auth.User, wantClass int, wantTier string) {
			body := `{"model":"test-model","input":"hi"}`
			if tier != "" {
				body = `{"model":"test-model","input":"hi","service_tier":"` + tier + `"}`
			}
			code, got := serve("/v1/responses", body, u, nil)
			Expect(code).To(Equal(http.StatusOK))
			Expect(got.Class).To(Equal(wantClass))
			Expect(got.Tier).To(Equal(wantTier))
		},
		Entry("no tier echoes default", "", user, 0, "default"),
		Entry("a user may lower with flex", "flex", user, -1, "flex"),
		Entry("a user cannot raise", "priority", user, 0, "default"),
		Entry("an admin may raise", "priority", admin, 1, "priority"),
	)
})
