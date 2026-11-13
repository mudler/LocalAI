//go:build auth

package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The handshake of a realtime session is where a paired client names its
// model. The check lives in the handler, so a stack without the early
// middleware still refuses a model that is not on the list.
var _ = Describe("the realtime handshake of a client key", func() {
	const hmac = "realtime-handshake"

	It("refuses a model that is not on the list before the upgrade, with the gate alone", func() {
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		owner := &auth.User{ID: "o1", Email: "o@example.test", Provider: auth.ProviderLocal, Subject: "s", Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(db.Create(owner).Error).To(Succeed())
		code, _, err := auth.CreatePairingCode(db, owner.ID, "c", []string{auth.FeatureClientInference}, nil, 0, 0, hmac, auth.WithModels([]string{"allowed"}))
		Expect(err).ToNot(HaveOccurred())
		key, _, err := auth.RedeemPairingCode(db, code, "", hmac)
		Expect(err).ToNot(HaveOccurred())

		cfg := &config.ApplicationConfig{}
		cfg.Auth.Enabled = true
		cfg.Auth.APIKeyHMACSecret = hmac
		stack := func(mw echo.MiddlewareFunc) *echo.Echo {
			e := echo.New()
			e.Use(auth.Middleware(db, cfg))
			e.Use(mw)
			// The refusals come before the application is used.
			e.GET("/v1/realtime", Realtime(nil))
			return e
		}
		get := func(e *echo.Echo, target string) int {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			req.Header.Set("Authorization", "Bearer "+key)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec.Code
		}
		for _, e := range []*echo.Echo{stack(auth.InstallClientModelGate(nil)), stack(auth.RequireClientModels(nil))} {
			Expect(get(e, "/v1/realtime?model=secret")).To(Equal(http.StatusForbidden))
			Expect(get(e, "/v1/realtime")).To(Equal(http.StatusBadRequest), "a client must name its model")
			// A model of the list passes the gate; the plain GET is then not a
			// websocket handshake, which is the next thing to refuse it.
			Expect(get(e, "/v1/realtime?model=allowed")).To(Equal(http.StatusBadRequest))
		}
		// No gate at all: the key is refused rather than let through.
		Expect(get(stack(func(next echo.HandlerFunc) echo.HandlerFunc { return next }), "/v1/realtime?model=allowed")).To(Equal(http.StatusForbidden))
	})
})

var _ = Describe("the realtime session of a client whose owner is demoted", func() {
	It("closes at the next recheck", func() {
		const hmac = "realtime-demote"
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		owner := &auth.User{ID: "o2", Email: "o2@example.test", Provider: auth.ProviderLocal, Subject: "s2", Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(db.Create(owner).Error).To(Succeed())
		code, _, err := auth.CreatePairingCode(db, owner.ID, "c", []string{auth.FeatureClientInference}, nil, 0, 0, hmac, auth.WithModels([]string{"allowed"}))
		Expect(err).ToNot(HaveOccurred())
		_, key, err := auth.RedeemPairingCode(db, code, "", hmac)
		Expect(err).ToNot(HaveOccurred())

		h := newWSHarness(func(context.Context) (bool, error) { return auth.ClientKeyStatus(db, key.ID, owner.ID) }, 10*time.Millisecond)
		c := h.dial()
		// An administrator owner keeps the session for a few rechecks, then
		// another administrator demotes it. A websocket read that timed out is
		// not usable again, so the demotion runs beside the single read.
		go func() {
			defer GinkgoRecover()
			time.Sleep(150 * time.Millisecond)
			Expect(db.Model(&auth.User{}).Where("id = ?", owner.ID).Update("role", auth.RoleUser).Error).To(Succeed())
		}()
		start := time.Now()
		msgs, closed := drain(c, 3*time.Second)
		Expect(closed).To(BeTrue())
		Expect(time.Since(start)).To(BeNumerically(">=", 140*time.Millisecond), "it stood until the demotion")
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0]).To(ContainSubstring("client_key_invalid"))
	})
})

var _ = Describe("the realtime session of a client whose list changes", func() {
	It("closes when the model leaves the list of the key or gains tools", func() {
		const hmac = "realtime-list"
		db, err := auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		owner := &auth.User{ID: "o3", Email: "o3@example.test", Provider: auth.ProviderLocal, Subject: "s3", Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(db.Create(owner).Error).To(Succeed())
		code, _, err := auth.CreatePairingCode(db, owner.ID, "c", []string{auth.FeatureClientInference}, nil, 0, 0, hmac, auth.WithModels([]string{"voice", "other"}))
		Expect(err).ToNot(HaveOccurred())
		_, key, err := auth.RedeemPairingCode(db, code, "", hmac)
		Expect(err).ToNot(HaveOccurred())

		var problem atomic.Value
		problem.Store("")
		check := func(string) error {
			if m := problem.Load().(string); m != "" {
				return errors.New(m)
			}
			return nil
		}
		status := func(ctx context.Context) (bool, error) {
			return auth.ClientKeyStatusFor(ctx, db, key.ID, owner.ID, "voice", check)
		}
		// The change of the list and of the tools check, each on its own session.
		for _, change := range []func(){
			func() {
				_, err := auth.SetClientKeyModels(db, key.ID, owner.ID, []string{"other"}, nil)
				Expect(err).ToNot(HaveOccurred())
			},
			func() { problem.Store("the model has tools attached") },
		} {
			h := newWSHarness(status, 10*time.Millisecond)
			c := h.dial()
			go func() {
				defer GinkgoRecover()
				time.Sleep(100 * time.Millisecond)
				change()
			}()
			msgs, closed := drain(c, 3*time.Second)
			Expect(closed).To(BeTrue())
			Expect(msgs).To(HaveLen(1))
			// Put the key back for the next change.
			_, err := auth.SetClientKeyModels(db, key.ID, owner.ID, []string{"voice", "other"}, nil)
			Expect(err).ToNot(HaveOccurred())
			problem.Store("")
		}
	})
})
