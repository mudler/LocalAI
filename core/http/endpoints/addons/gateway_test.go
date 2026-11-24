// SPDX-License-Identifier: MIT
package addons

import (
	"bytes"
	"context"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestGateway(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Addon HTTP") }

var _ = Describe("addon routes", func() {
	It("rejects anonymous and forged forwarding authority through real authentication", func() {
		e := echo.New()
		g := &gateway.Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }}
		e.Use(auth.Middleware(nil, &config.ApplicationConfig{}))
		e.POST("/api/addons/dispatch", Dispatch(g, nil))
		r := httptest.NewRequest(http.MethodPost, "/api/addons/dispatch", bytes.NewBufferString(`{}`))
		r.Header.Set("X-Forwarded-Actor", "admin")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		Expect(w.Code).To(Equal(401))
	})
})

type readClient struct{ contract.Runtime }

func (readClient) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{Payload: []byte(`"served"`)}, nil
}

type readRuntime struct{}

func (readRuntime) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return addonruntime.Instance{Generation: 1, Incarnation: "current", Client: readClient{}}, nil
}

var _ = Describe("local-owner dispatch end to end", func() {
	It("serves the synthetic owner through the real chain, denies sharing and rejects named callers", func() {
		store, err := policy.OpenStore(filepath.Join(GinkgoT().TempDir(), "policy"), policy.ModeLocalOwner)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = store.Close() })
		revalidate := auth.AddonRevalidate(nil, auth.AddonModeLocalOwner)
		p := policy.New(store, policy.Options{Revalidate: revalidate, CreateAuthority: func(context.Context, contract.Actor, string) error { return nil }})
		owner := contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: "local-owner"}
		key, err := p.Register(context.Background(), owner, "sample")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Commit(context.Background(), owner, key)).To(Succeed())
		provider := p.Gateway()
		g := &gateway.Gateway{Runtime: readRuntime{}, Revalidate: revalidate, Admission: provider, Resolver: provider}

		serve := func(mode string) int {
			e := echo.New()
			e.Use(auth.Middleware(nil, &config.ApplicationConfig{}))
			var opts []Option
			if mode != "default" {
				opts = append(opts, WithOwnership(mode))
			}
			e.POST("/api/addons/dispatch", Dispatch(g, nil, opts...), BrowserProtection())
			body := `{"addonId":"sample","resourceId":"` + key.ResourceID + `","operationId":"op","generation":1,"action":"read","arguments":{}}`
			r := httptest.NewRequest(http.MethodPost, "/api/addons/dispatch", bytes.NewBufferString(body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			return w.Code
		}
		Expect(serve(auth.AddonModeLocalOwner)).To(Equal(200))
		Expect(serve(auth.AddonModeNamedUser)).To(Equal(401))
		// Without the option the handler is named-user and never infers local-owner.
		Expect(serve("default")).To(Equal(401))
		Expect(serve("")).To(Equal(401))
		// Sharing is off for the local owner.
		Expect(p.SetGrant(context.Background(), owner, key, "bob", policy.RoleViewer)).NotTo(Succeed())
		Expect(p.Revoke(context.Background(), owner, key, "bob")).NotTo(Succeed())
	})
})
