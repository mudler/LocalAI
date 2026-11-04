// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostgrants"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs use real accounts: the credential type decides.
var _ = Describe("host access routes with real sessions and API keys", func() {
	It("lets an administrator's session write and refuses the same administrator's API key", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).NotTo(HaveOccurred())
		admin := auth.User{ID: "admin", Role: auth.RoleAdmin, Status: auth.StatusActive}
		plain := auth.User{ID: "plain", Role: auth.RoleUser, Status: auth.StatusActive}
		Expect(db.Create(&admin).Error).To(Succeed())
		Expect(db.Create(&plain).Error).To(Succeed())
		adminKey, _, err := auth.CreateAPIKey(db, admin.ID, "test", auth.RoleAdmin, "secret", nil)
		Expect(err).NotTo(HaveOccurred())
		adminSession, err := auth.CreateSession(db, admin.ID, "secret")
		Expect(err).NotTo(HaveOccurred())
		plainSession, err := auth.CreateSession(db, plain.ID, "secret")
		Expect(err).NotTo(HaveOccurred())

		store, err := hostgrants.Open(filepath.Join(GinkgoT().TempDir(), "hostgrants.json"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		auditPath := filepath.Join(GinkgoT().TempDir(), "audit.jsonl")
		audit, err := hostgrants.OpenAudit(auditPath)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(audit.Close)
		svc, err := gateway.NewHostGrantsOwner(gateway.HostGrantsConfig{Store: store, Manager: &hgManager{}, Audit: audit,
			Admin: func(ctx context.Context, a contract.Actor) error { return auth.ValidateAddonActor(ctx, db, a, true) }})
		Expect(err).NotTo(HaveOccurred())
		gw := &gateway.Gateway{HostGrantsOwner: svc, Revalidate: func(ctx context.Context, a contract.Actor) error { return auth.ValidateAddonActor(ctx, db, a, false) }}

		e := echo.New()
		cfg := &config.ApplicationConfig{}
		cfg.Auth.APIKeyHMACSecret = "secret"
		e.Use(auth.Middleware(db, cfg))
		g := e.Group("/api/addons", BrowserProtection())
		g.GET("/host-grants", GetHostGrants(gw, db))
		g.PUT("/host-grants/:addonId", EditHostGrants(gw, db))

		put := func(mutate func(*http.Request)) int {
			r := httptest.NewRequest("PUT", "/api/addons/host-grants/sandbox", strings.NewReader(`{"operationId":"op1","revision":0,"ops":["egress.check"]}`))
			r.Host = "192.0.2.10:8080"
			r.Header.Set("Content-Type", "application/json")
			mutate(r)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			return w.Code
		}
		tab := func(session string) func(*http.Request) {
			return func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "session", Value: session})
				r.Header.Set("Sec-Fetch-Site", "same-origin")
				r.Header.Set("Sec-Fetch-Dest", "empty")
				r.Header.Set("Sec-Fetch-Mode", "cors")
				r.Header.Set(ConfirmHeader, "sandbox")
			}
		}
		// The session of an administrator passes every gate.
		Expect(put(tab(adminSession))).To(Equal(200))
		Expect(put(func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+adminKey)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			r.Header.Set(ConfirmHeader, "sandbox")
		})).To(Equal(403), "an administrator API key")
		Expect(put(func(r *http.Request) {
			tab(adminSession)(r)
			r.Header.Set("Authorization", "Bearer "+adminKey)
		})).To(Equal(403), "a session cookie with a key header")
		Expect(put(func(r *http.Request) {
			tab(adminSession)(r)
			r.Header.Del(ConfirmHeader)
		})).To(Equal(403), "no confirm header")
		Expect(put(tab(plainSession))).To(BeElementOf(401, 403), "a session of a user who is not an administrator")

		read := func(mutate func(*http.Request)) int {
			r := httptest.NewRequest("GET", "/api/addons/host-grants", nil)
			r.Host = "192.0.2.10:8080"
			mutate(r)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			return w.Code
		}
		Expect(read(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+adminKey) })).To(Equal(200), "an administrator key may read")
		Expect(read(func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session", Value: plainSession}) })).To(BeElementOf(401, 403))

		// The refusals of the callers that are not a person are in the audit
		// file, with the kind of credential and no secret.
		raw, err := os.ReadFile(auditPath)
		Expect(err).NotTo(HaveOccurred())
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		refusedByKey := 0
		for _, l := range lines {
			if strings.Contains(l, `"event":"refused"`) && strings.Contains(l, `"reason":"not_human_surface"`) && strings.Contains(l, `"credential":"key"`) {
				refusedByKey++
			}
		}
		Expect(refusedByKey).To(BeNumerically(">=", 1), string(raw))
		Expect(string(raw)).NotTo(ContainSubstring(adminKey))
		Expect(string(raw)).NotTo(ContainSubstring("secret"))
	})
})
