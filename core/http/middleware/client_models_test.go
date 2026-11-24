//go:build auth

// SPDX-License-Identifier: MIT

package middleware_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// These specs run the real binder of the handlers (SetModelAndConfig) behind
// the real global stack, not a stub handler: the first version of the model
// list check read the request in another way than the binder did, and a stub
// handler hid it.
var _ = Describe("The model list of a client key against the real handlers", func() {
	const hmac = "client-models-secret"
	var (
		db       *gorm.DB
		key      string
		owner    *auth.User
		withMw   *echo.Echo // global stack as in the application
		gateOnly *echo.Echo // the gate only: the handler decides alone
		noGate   *echo.Echo // no model middleware at all
	)

	served := func(c echo.Context) error {
		cfg, _ := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		name := ""
		if cfg != nil {
			name = cfg.Name
		}
		return c.String(http.StatusOK, "served="+name)
	}

	BeforeEach(func() {
		var err error
		db, err = auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		sqlDB, err := db.DB()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(sqlDB.Close)
		owner = &auth.User{ID: "owner-1", Email: "o@example.test", Name: "o", Provider: auth.ProviderLocal, Subject: "s1", Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(db.Create(owner).Error).To(Succeed())

		dir := GinkgoT().TempDir()
		ac := config.NewApplicationConfig()
		ac.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
		ac.Auth.Enabled = true
		ac.Auth.APIKeyHMACSecret = hmac
		bcl := config.NewModelConfigLoader(dir)
		bcl.ReplaceModelConfigs([]config.ModelConfig{
			{Name: "allowed", Backend: "test"},
			{Name: "secret", Backend: "test"},
		})
		re := NewRequestExtractor(bcl, model.NewModelLoader(ac.SystemState), ac)

		code, _, err := auth.CreatePairingCode(db, owner.ID, "c", []string{auth.FeatureClientInference, auth.FeatureClientSelf}, nil, 0, 0, hmac,
			auth.WithModels([]string{"allowed"}), auth.WithModelCheck(bcl.ClientModelProblem))
		Expect(err).ToNot(HaveOccurred())
		key, _, err = auth.RedeemPairingCode(db, code, "", hmac)
		Expect(err).ToNot(HaveOccurred())

		build := func(early bool) *echo.Echo {
			e := echo.New()
			e.Pre(echomw.RemoveTrailingSlash())
			e.Use(auth.Middleware(db, ac))
			e.Use(auth.RequireRouteFeature(db))
			if early {
				e.Use(auth.RequireClientModels(bcl.ClientModelProblem))
			} else {
				// Only the gate, which reads no part of the request: the
				// check in the handler has to decide alone.
				e.Use(auth.InstallClientModelGate(bcl.ClientModelProblem))
			}
			openai := func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }
			tts := func() schema.LocalAIRequest { return new(schema.TTSRequest) }
			e.POST("/v1/chat/completions", served, re.SetModelAndConfigWithDefault(openai, config.BuildUsecaseFilterFn(config.FLAG_CHAT)))
			e.POST("/v1/audio/transcriptions", served, re.SetModelAndConfig(openai))
			e.POST("/v1/audio/classification", served, re.SetModelAndConfig(openai))
			e.POST("/v1/audio/speech", served, re.SetModelAndConfig(tts))
			e.POST("/tts", served, re.SetModelAndConfig(tts))
			return e
		}
		withMw = build(true)
		gateOnly = build(false)
		noGate = echo.New()
		noGate.Use(auth.Middleware(db, ac))
		noGate.Use(auth.RequireRouteFeature(db))
		noGate.POST("/v1/chat/completions", served, re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
	})

	do := func(e *echo.Echo, method, target, ctype string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, bytes.NewReader(body))
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}
	multi := func(fields ...[2]string) (string, []byte) {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		for _, f := range fields {
			Expect(w.WriteField(f[0], f[1])).To(Succeed())
		}
		fw, err := w.CreateFormFile("file", "a.wav")
		Expect(err).ToNot(HaveOccurred())
		_, _ = fw.Write([]byte("RIFF"))
		Expect(w.Close()).To(Succeed())
		return w.FormDataContentType(), b.Bytes()
	}

	msgs := `,"messages":[{"role":"user","content":"hi"}]`
	js := "application/json"

	It("serves a model of the list on every route", func() {
		for _, e := range []*echo.Echo{withMw, gateOnly} {
			Expect(do(e, "POST", "/v1/chat/completions", js, []byte(`{"model":"allowed"`+msgs+`}`)).Body.String()).To(Equal("served=allowed"))
			Expect(do(e, "POST", "/v1/chat/completions?model=allowed", js, []byte(`{"model":"allowed"`+msgs+`}`)).Body.String()).To(Equal("served=allowed"))
			Expect(do(e, "POST", "/tts", js, []byte(`{"model":"allowed","input":"x"}`)).Body.String()).To(Equal("served=allowed"))
			Expect(do(e, "POST", "/v1/audio/speech", js, []byte(`{"model":"allowed","input":"x"}`)).Body.String()).To(Equal("served=allowed"))
			ct, body := multi([2]string{"model", "allowed"})
			Expect(do(e, "POST", "/v1/audio/transcriptions", ct, body).Body.String()).To(Equal("served=allowed"))
			Expect(do(e, "POST", "/v1/audio/classification", ct, body).Body.String()).To(Equal("served=allowed"))
			Expect(do(e, "POST", "/v1/chat/completions", "application/x-www-form-urlencoded", []byte(url.Values{"model": {"allowed"}}.Encode())).Body.String()).To(Equal("served=allowed"))
		}
	})

	// The seven requests of the review, and the routes it did not send. None may
	// reach the model "secret", with the early middleware and without it.
	type attack struct {
		name, method, target, ctype string
		body                        func() (string, []byte)
	}
	static := func(ct, b string) func() (string, []byte) {
		return func() (string, []byte) { return ct, []byte(b) }
	}
	mp := func(fields ...[2]string) func() (string, []byte) {
		return func() (string, []byte) { return multi(fields...) }
	}
	attacks := []attack{
		{"JSON with trailing data", "POST", "/v1/chat/completions?model=allowed", "", static(js, `{"model":"secret"`+msgs+`} trailing`)},
		{"two JSON values", "POST", "/v1/chat/completions?model=allowed", "", static(js, `{"model":"secret"`+msgs+`}{}`)},
		{"XML body", "POST", "/v1/chat/completions?model=allowed", "", static("application/xml", `<r><Model>secret</Model></r>`)},
		{"text/xml body", "POST", "/v1/chat/completions?model=allowed", "", static("text/xml", `<r><Model>secret</Model></r>`)},
		{"multipart MODEL on chat", "POST", "/v1/chat/completions?model=allowed", "", mp([2]string{"MODEL", "secret"})},
		{"multipart MODEL on transcriptions", "POST", "/v1/audio/transcriptions?model=allowed", "", mp([2]string{"MODEL", "secret"})},
		{"tts JSON with trailing data", "POST", "/tts?model=allowed", "", static(js, `{"model":"secret","input":"hi"} x`)},
		{"speech JSON with trailing data", "POST", "/v1/audio/speech?model=allowed", "", static(js, `{"model":"secret","input":"hi"} x`)},
		{"classification multipart MODEL", "POST", "/v1/audio/classification?model=allowed", "", mp([2]string{"MODEL", "secret"})},
		{"classification XML", "POST", "/v1/audio/classification?model=allowed", "", static("application/xml", `<r><Model>secret</Model></r>`)},
		{"transcriptions XML", "POST", "/v1/audio/transcriptions?model=allowed", "", static("application/xml", `<r><Model>secret</Model></r>`)},
		{"multipart model and Model", "POST", "/v1/audio/transcriptions", "", mp([2]string{"Model", "secret"}, [2]string{"model", "allowed"})},
		{"JSON model twice", "POST", "/v1/chat/completions", "", static(js, `{"model":"allowed","Model":"secret"`+msgs+`}`)},
		{"body model against query", "POST", "/v1/chat/completions?model=allowed", "", static(js, `{"model":"secret"`+msgs+`}`)},
		{"model twice in the query", "POST", "/v1/chat/completions?model=allowed&model=secret", "", static(js, `{"messages":[]}`)},
		{"form MODEL", "POST", "/v1/chat/completions?model=allowed", "", static("application/x-www-form-urlencoded", url.Values{"MODEL": {"secret"}}.Encode())},
		{"body of an unknown type", "POST", "/v1/chat/completions?model=allowed", "", static("application/octet-stream", `{"model":"secret"}`)},
		{"body without a content type", "POST", "/v1/chat/completions?model=allowed", "", static("", `{"model":"secret"}`)},
	}
	for _, a := range attacks {
		It("never reaches the model that is not on the list: "+a.name, func() {
			for _, e := range []*echo.Echo{withMw, gateOnly, noGate} {
				ct, body := a.body()
				rec := do(e, a.method, a.target, ct, body)
				Expect(rec.Body.String()).ToNot(ContainSubstring("served=secret"), "status %d", rec.Code)
				if e == withMw {
					Expect(rec.Code).To(BeNumerically(">=", 400), rec.Body.String())
				}
			}
		})
	}

	It("stops a client key when its owner is no longer an administrator", func() {
		body := []byte(`{"model":"allowed"` + msgs + `}`)
		Expect(do(withMw, "POST", "/v1/chat/completions", js, body).Code).To(Equal(http.StatusOK))
		// Another administrator demotes the owner and turns chat off.
		Expect(db.Model(&auth.User{}).Where("id = ?", owner.ID).Update("role", auth.RoleUser).Error).To(Succeed())
		Expect(auth.UpdateUserPermissions(db, owner.ID, auth.PermissionMap{auth.FeatureChat: false})).To(Succeed())
		for _, e := range []*echo.Echo{withMw, gateOnly} {
			rec := do(e, "POST", "/v1/chat/completions", js, body)
			Expect(rec.Code).To(Equal(http.StatusUnauthorized), rec.Body.String())
			Expect(rec.Body.String()).ToNot(ContainSubstring("served="))
		}
	})

	It("serves a request with no body that names its model in the query", func() {
		for _, e := range []*echo.Echo{withMw, gateOnly} {
			Expect(do(e, "POST", "/v1/chat/completions?model=allowed", js, nil).Body.String()).To(Equal("served=allowed"))
		}
	})

	It("refuses a client key on a route that has no model gate, so an unprotected route stays closed", func() {
		rec := do(noGate, "POST", "/v1/chat/completions", js, []byte(`{"model":"allowed"`+msgs+`}`))
		Expect(rec.Code).To(Equal(http.StatusForbidden))
	})

	It("lets the handler decide alone when the early middleware reads nothing", func() {
		// The binder reads up to the first JSON value and ignores the rest, and
		// reads XML: the model it produced is the one that is checked.
		Expect(do(gateOnly, "POST", "/v1/chat/completions?model=allowed", js, []byte(`{"model":"secret"`+msgs+`} trailing`)).Code).To(Equal(http.StatusForbidden))
		Expect(do(gateOnly, "POST", "/v1/chat/completions?model=allowed", "application/xml", []byte(`<r><Model>secret</Model></r>`)).Code).To(Equal(http.StatusForbidden))
		Expect(do(gateOnly, "POST", "/v1/chat/completions", js, []byte(`{"messages":[]}`)).Code).To(Equal(http.StatusBadRequest), "a client must name its model")
	})

	It("checks the name before and after the :latest suffix is stripped", func() {
		rec := do(gateOnly, "POST", "/v1/chat/completions", js, []byte(`{"model":"allowed:latest"`+msgs+`}`))
		Expect(rec.Code).To(Equal(http.StatusForbidden), "a name that is not on the list as written is refused")
	})

	Describe("a router", func() {
		It("is refused for a client key when a candidate or the fallback has tools, at creation, at change and on a request", func() {
			dir := GinkgoT().TempDir()
			ac := config.NewApplicationConfig()
			ac.SystemState = &system.SystemState{Model: system.Model{ModelsPath: dir}}
			ac.Auth.Enabled = true
			ac.Auth.APIKeyHMACSecret = hmac
			bcl := config.NewModelConfigLoader(dir)
			router := func(fallback string) config.ModelConfig {
				return config.ModelConfig{Name: "smart", Backend: "test", Router: config.RouterConfig{
					ClassifierModel: "plain",
					Policies:        []config.RouterPolicy{{Label: "code", Description: "code"}},
					Candidates:      []config.RouterCandidate{{Model: "plain", Labels: []string{"code"}}},
					Fallback:        fallback,
				}}
			}
			tooled := config.ModelConfig{Name: "tooled", Backend: "test", MCP: config.MCPConfig{Auto: true}}
			plain := config.ModelConfig{Name: "plain", Backend: "test"}
			bcl.ReplaceModelConfigs([]config.ModelConfig{plain, tooled, router("tooled")})

			inf := []string{auth.FeatureClientInference}
			_, _, err := auth.CreatePairingCode(db, owner.ID, "c", inf, nil, 0, 0, hmac,
				auth.WithModels([]string{"smart"}), auth.WithModelCheck(bcl.ClientModelProblem))
			Expect(err).To(HaveOccurred(), "creation")

			// The same router with a plain fallback is accepted.
			bcl.ReplaceModelConfigs([]config.ModelConfig{plain, tooled, router("plain")})
			code, rec, err := auth.CreatePairingCode(db, owner.ID, "c", inf, nil, 0, 0, hmac,
				auth.WithModels([]string{"smart"}), auth.WithModelCheck(bcl.ClientModelProblem))
			Expect(err).ToNot(HaveOccurred())
			plainKey, _, err := auth.RedeemPairingCode(db, code, "", hmac)
			Expect(err).ToNot(HaveOccurred())
			var stored auth.UserAPIKey
			Expect(db.Where("kind = ?", auth.KeyKindClient).Order("created_at desc").First(&stored).Error).To(Succeed())
			_ = rec

			re := NewRequestExtractor(bcl, model.NewModelLoader(ac.SystemState), ac)
			e := echo.New()
			e.Use(auth.Middleware(db, ac))
			e.Use(auth.RequireRouteFeature(db))
			e.Use(auth.RequireClientModels(bcl.ClientModelProblem))
			e.POST("/v1/chat/completions", served, re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }))
			send := func() int {
				req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"smart","messages":[]}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+plainKey)
				r := httptest.NewRecorder()
				e.ServeHTTP(r, req)
				return r.Code
			}
			Expect(send()).To(Equal(http.StatusOK))

			// Change: the owner puts a model with tools on the list.
			bcl.ReplaceModelConfigs([]config.ModelConfig{plain, tooled, router("tooled")})
			_, err = auth.SetClientKeyModels(db, stored.ID, owner.ID, []string{"smart"}, bcl.ClientModelProblem)
			Expect(err).To(HaveOccurred(), "change")
			// Request: the fallback gained tools after the key was made.
			Expect(send()).To(Equal(http.StatusForbidden), "request")
		})
	})
})
