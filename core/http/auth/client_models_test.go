//go:build auth

package auth_test

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("The models of a client key", func() {
	const secret = "test-secret"
	var (
		db    *gorm.DB
		owner *auth.User
	)
	inference := []string{auth.FeatureClientInference}

	BeforeEach(func() {
		db = testDB()
		owner = createTestUser(db, "owner@example.com", auth.RoleAdmin, auth.ProviderLocal)
	})

	Describe("the list", func() {
		It("is required with client_inference, refused without it, and checked for shape", func() {
			_, _, err := auth.CreatePairingCode(db, owner.ID, "c", inference, nil, 0, 0, secret)
			Expect(err).To(MatchError(auth.ErrClientModels))
			_, _, err = auth.CreatePairingCode(db, owner.ID, "c", inference, nil, 0, 0, secret, auth.WithModels([]string{}))
			Expect(err).To(MatchError(auth.ErrClientModels))
			_, _, err = auth.CreatePairingCode(db, owner.ID, "c", []string{auth.FeatureClientSelf}, nil, 0, 0, secret, auth.WithModels([]string{"m"}))
			Expect(err).To(MatchError(auth.ErrClientModels))
			for _, bad := range []string{"", " m", "m ", "a\nb", strings.Repeat("m", auth.MaxClientModelName+1)} {
				_, _, err = auth.CreatePairingCode(db, owner.ID, "c", inference, nil, 0, 0, secret, auth.WithModels([]string{bad}))
				Expect(err).To(MatchError(auth.ErrInvalidClientInput), bad)
			}
			var many []string
			for i := range auth.MaxClientModels + 1 {
				many = append(many, strings.Repeat("m", i+1))
			}
			_, _, err = auth.CreatePairingCode(db, owner.ID, "c", inference, nil, 0, 0, secret, auth.WithModels(many))
			Expect(err).To(MatchError(auth.ErrInvalidClientInput))
		})

		It("is copied to the key, sorted without duplicates, and survives the store", func() {
			code, rec, err := auth.CreatePairingCode(db, owner.ID, "c", inference, nil, 0, 0, secret, auth.WithModels([]string{"zeta", "alpha", "alpha", "org/model:q4"}))
			Expect(err).ToNot(HaveOccurred())
			Expect(rec.Models).To(Equal(auth.ModelList{"alpha", "org/model:q4", "zeta"}))
			_, key, err := auth.RedeemPairingCode(db, code, "", secret)
			Expect(err).ToNot(HaveOccurred())
			var stored auth.UserAPIKey
			Expect(db.First(&stored, "id = ?", key.ID).Error).To(Succeed())
			Expect(stored.Models).To(Equal(auth.ModelList{"alpha", "org/model:q4", "zeta"}))
			Expect(stored.AllowsModel("alpha")).To(BeTrue())
			Expect(stored.AllowsModel("beta")).To(BeFalse())
			Expect(stored.AllowsModel("")).To(BeFalse())
		})

		It("is checked at creation and at change with the check of the models", func() {
			check := func(m string) error {
				if m == "tools" {
					return errors.New("the model has tools attached")
				}
				return nil
			}
			_, _, err := auth.CreatePairingCode(db, owner.ID, "c", inference, nil, 0, 0, secret,
				auth.WithModels([]string{"ok", "tools"}), auth.WithModelCheck(check))
			Expect(err).To(MatchError(auth.ErrInvalidClientInput))
			Expect(err.Error()).To(ContainSubstring("tools"))

			code, _, err := auth.CreatePairingCode(db, owner.ID, "c", inference, nil, 0, 0, secret, auth.WithModels([]string{"ok"}), auth.WithModelCheck(check))
			Expect(err).ToNot(HaveOccurred())
			_, key, err := auth.RedeemPairingCode(db, code, "", secret)
			Expect(err).ToNot(HaveOccurred())

			_, err = auth.SetClientKeyModels(db, key.ID, owner.ID, []string{"tools"}, check)
			Expect(err).To(MatchError(auth.ErrInvalidClientInput))
			_, err = auth.SetClientKeyModels(db, key.ID, owner.ID, []string{}, check)
			Expect(err).To(MatchError(auth.ErrClientModels), "a key with client_inference cannot end with no model")
			list, err := auth.SetClientKeyModels(db, key.ID, owner.ID, []string{"b", "a"}, check)
			Expect(err).ToNot(HaveOccurred())
			Expect(list).To(Equal(auth.ModelList{"a", "b"}))
			other := createTestUser(db, "o2@example.com", auth.RoleAdmin, auth.ProviderLocal)
			_, err = auth.SetClientKeyModels(db, key.ID, other.ID, []string{"a"}, check)
			Expect(err).To(HaveOccurred())

			// A key without client_inference takes no list.
			code, _, _ = auth.CreatePairingCode(db, owner.ID, "s", nil, nil, 0, 0, secret)
			_, selfKey, err := auth.RedeemPairingCode(db, code, "", secret)
			Expect(err).ToNot(HaveOccurred())
			_, err = auth.SetClientKeyModels(db, selfKey.ID, owner.ID, []string{"a"}, check)
			Expect(err).To(MatchError(auth.ErrClientModels))
			Expect(selfKey.AllowsModel("a")).To(BeFalse())
		})
	})

	Describe("the middleware", func() {
		var (
			e       *echo.Echo
			key     string
			seen    []byte
			checked []string
			refuse  map[string]error
		)

		BeforeEach(func() {
			cfg := &config.ApplicationConfig{}
			cfg.Auth.Enabled = true
			cfg.Auth.APIKeyHMACSecret = secret
			refuse = map[string]error{}
			checked = nil
			e = echo.New()
			e.Use(auth.Middleware(db, cfg))
			e.Use(auth.RequireRouteFeature(db))
			e.Use(auth.RequireClientModels(func(m string) error {
				checked = append(checked, m)
				return refuse[m]
			}))
			h := func(c echo.Context) error {
				seen, _ = io.ReadAll(c.Request().Body)
				return c.String(http.StatusOK, "ok")
			}
			e.POST("/v1/chat/completions", h)
			e.POST("/tts", h)
			e.POST("/v1/audio/transcriptions", h)
			e.GET("/v1/realtime", h)
			e.GET("/v1/models", h)
			e.POST("/v1/embeddings", h)

			code, _, err := auth.CreatePairingCode(db, owner.ID, "c", inference, nil, 0, 0, secret, auth.WithModels([]string{"allowed", "other-allowed"}))
			Expect(err).ToNot(HaveOccurred())
			plain, _, err := auth.RedeemPairingCode(db, code, "", secret)
			Expect(err).ToNot(HaveOccurred())
			key = plain
		})

		send := func(method, target, contentType, token string, body []byte) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, target, bytes.NewReader(body))
			if contentType != "" {
				req.Header.Set("Content-Type", contentType)
			}
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec
		}
		js := "application/json"

		It("lets a model of the list through and hands the body to the handler unchanged", func() {
			body := []byte(`{"model":"allowed","messages":[{"role":"user","content":"hi"}]}`)
			rec := send("POST", "/v1/chat/completions", js, key, body)
			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(seen).To(Equal(body))
			Expect(checked).To(Equal([]string{"allowed"}))
		})

		It("refuses a model that is not on the list, and a request that names none", func() {
			Expect(send("POST", "/v1/chat/completions", js, key, []byte(`{"model":"secret"}`)).Code).To(Equal(http.StatusForbidden))
			Expect(send("POST", "/v1/chat/completions", js, key, []byte(`{"messages":[]}`)).Code).To(Equal(http.StatusBadRequest))
			Expect(send("POST", "/v1/chat/completions", js, key, []byte(`{"model":""}`)).Code).To(Equal(http.StatusBadRequest))
			Expect(send("POST", "/v1/chat/completions", js, key, []byte(`{"model":null}`)).Code).To(Equal(http.StatusBadRequest))
			Expect(send("POST", "/v1/chat/completions", js, key, []byte(`{"model":["allowed"]}`)).Code).To(Equal(http.StatusBadRequest))
			Expect(send("POST", "/v1/chat/completions", js, key, nil).Code).To(Equal(http.StatusBadRequest))
			Expect(send("POST", "/v1/chat/completions", js, key, []byte(`not json`)).Code).To(Equal(http.StatusBadRequest))
			Expect(send("GET", "/v1/realtime", "", key, nil).Code).To(Equal(http.StatusBadRequest))
			Expect(checked).To(BeEmpty(), "a refused name never reaches the model check")
		})

		It("reads the model from the query, a form field and a multipart field", func() {
			Expect(send("GET", "/v1/realtime?model=allowed", "", key, nil).Code).To(Equal(http.StatusOK))
			Expect(send("GET", "/v1/realtime?model=secret", "", key, nil).Code).To(Equal(http.StatusForbidden))
			Expect(send("POST", "/tts", "application/x-www-form-urlencoded", key, []byte("model=allowed&input=hi")).Code).To(Equal(http.StatusOK))
			Expect(send("POST", "/tts", "application/x-www-form-urlencoded", key, []byte("model=secret")).Code).To(Equal(http.StatusForbidden))

			multi := func(model string) (string, []byte) {
				var buf bytes.Buffer
				w := multipart.NewWriter(&buf)
				if model != "" {
					Expect(w.WriteField("model", model)).To(Succeed())
				}
				part, err := w.CreateFormFile("file", "a.wav")
				Expect(err).ToNot(HaveOccurred())
				_, _ = part.Write([]byte("RIFF"))
				Expect(w.Close()).To(Succeed())
				return w.FormDataContentType(), buf.Bytes()
			}
			ct, body := multi("allowed")
			Expect(send("POST", "/v1/audio/transcriptions", ct, key, body).Code).To(Equal(http.StatusOK))
			ct, body = multi("secret")
			Expect(send("POST", "/v1/audio/transcriptions", ct, key, body).Code).To(Equal(http.StatusForbidden))
			ct, body = multi("")
			Expect(send("POST", "/v1/audio/transcriptions", ct, key, body).Code).To(Equal(http.StatusBadRequest))
		})

		It("checks every name the request carries, so a second one cannot hide behind the first", func() {
			// The query says allowed and the body says another model.
			Expect(send("POST", "/v1/chat/completions?model=allowed", js, key, []byte(`{"model":"secret"}`)).Code).To(Equal(http.StatusForbidden))
			Expect(send("POST", "/v1/chat/completions?model=secret", js, key, []byte(`{"model":"allowed"}`)).Code).To(Equal(http.StatusForbidden))
			Expect(send("POST", "/v1/chat/completions?model=allowed", js, key, []byte(`{"model":"other-allowed"}`)).Code).To(Equal(http.StatusOK))
			// A body that a binder could read under another content type is
			// refused: the binder also reads XML, which nothing here parses.
			Expect(send("POST", "/v1/chat/completions?model=allowed", "application/json-patch+json", key, []byte(`{"model":"secret"}`)).Code).To(Equal(http.StatusForbidden), "the binder reads it as JSON too")
			Expect(send("POST", "/v1/chat/completions?model=allowed", "", key, []byte(`{"model":"secret"}`)).Code).To(Equal(http.StatusUnsupportedMediaType))
			Expect(send("POST", "/v1/chat/completions?model=allowed", "application/xml", key, []byte(`<r><Model>secret</Model></r>`)).Code).To(Equal(http.StatusUnsupportedMediaType))
			// The shapes the binder reads in another way than a strict parser.
			Expect(send("POST", "/v1/chat/completions?model=allowed", js, key, []byte(`{"model":"secret"} trailing`)).Code).To(Equal(http.StatusBadRequest))
			Expect(send("POST", "/v1/chat/completions?model=allowed", js, key, []byte(`{"model":"allowed"}{}`)).Code).To(Equal(http.StatusBadRequest))
			Expect(send("POST", "/v1/chat/completions", js, key, []byte(`{"model":"allowed","Model":"allowed"}`)).Code).To(Equal(http.StatusBadRequest))
			Expect(send("POST", "/v1/chat/completions?model=allowed&model=allowed", js, key, []byte(`{}`)).Code).To(Equal(http.StatusBadRequest))
			Expect(send("POST", "/tts", "application/x-www-form-urlencoded", key, []byte("model=allowed&MODEL=secret")).Code).To(Equal(http.StatusBadRequest))
		})

		It("asks the check again on every request, so a model that gained tools is refused", func() {
			body := []byte(`{"model":"allowed"}`)
			Expect(send("POST", "/v1/chat/completions", js, key, body).Code).To(Equal(http.StatusOK))
			refuse["allowed"] = config.ErrClientModelTools
			rec := send("POST", "/v1/chat/completions", js, key, body)
			Expect(rec.Code).To(Equal(http.StatusForbidden))
			Expect(rec.Body.String()).To(ContainSubstring("tools"))
		})

		It("lets the model list through without a name, and does not touch the other routes", func() {
			Expect(send("GET", "/v1/models", "", key, nil).Code).To(Equal(http.StatusOK))
			Expect(send("POST", "/v1/embeddings", js, key, []byte(`{"model":"allowed"}`)).Code).To(Equal(http.StatusForbidden))
		})

		It("does not change a user key: no model is required of it", func() {
			user := createTestUser(db, "u@example.com", auth.RoleUser, auth.ProviderLocal)
			plain, _, err := auth.CreateAPIKey(db, user.ID, "k", auth.RoleUser, secret, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(send("POST", "/v1/chat/completions", js, plain, []byte(`{"messages":[]}`)).Code).To(Equal(http.StatusOK))
			Expect(checked).To(BeEmpty())
		})

		It("reads the list from the store on each request, so a change applies at once", func() {
			var k auth.UserAPIKey
			Expect(db.Where("kind = ?", auth.KeyKindClient).First(&k).Error).To(Succeed())
			_, err := auth.SetClientKeyModels(db, k.ID, owner.ID, []string{"fresh"}, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(send("POST", "/v1/chat/completions", js, key, []byte(`{"model":"allowed"}`)).Code).To(Equal(http.StatusForbidden))
			Expect(send("POST", "/v1/chat/completions", js, key, []byte(`{"model":"fresh"}`)).Code).To(Equal(http.StatusOK))
		})
	})
})
