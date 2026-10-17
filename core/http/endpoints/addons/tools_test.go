// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/httpclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type toolAdmission struct{}

func (toolAdmission) Admit(ctx context.Context, _ contract.Actor, _ gateway.ResourceKey, _ string, run func(context.Context) error) error {
	return run(ctx)
}

type toolResolver struct{}

func (toolResolver) Resolve(context.Context, contract.Actor, contract.Request) (gateway.ResourceKey, error) {
	return gateway.ResourceKey{AddonID: "notes", ResourceID: "res"}, nil
}
func (toolResolver) Output(context.Context, contract.Actor, contract.Request, string) (contract.OutputDescription, error) {
	return contract.OutputDescription{}, gateway.ErrForbidden
}

type toolDeclared struct {
	tools map[string][]string
	fail  atomic.Pointer[error]
}

func (t *toolDeclared) DeclaredTools(_ context.Context, id string, gen uint64) ([]string, error) {
	if e := t.fail.Load(); e != nil {
		return nil, *e
	}
	if l, ok := t.tools[id]; ok && gen == 1 {
		return l, nil
	}
	return nil, gateway.ErrForbidden
}

type toolClient struct {
	contract.Runtime
	reads atomic.Int32
}

func (c *toolClient) Read(context.Context, contract.Envelope) (contract.Result, error) {
	c.reads.Add(1)
	return contract.Result{Payload: []byte(`{"ok":true}`)}, nil
}

type toolRuntime struct{ client contract.Runtime }

func (t toolRuntime) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return addonruntime.Instance{Generation: 1, Incarnation: "current", Client: t.client}, nil
}

var _ = Describe("server-side tool declarations over HTTP", Label("tools"), func() {
	It("gives the same 403 on the owner and on a forwarding frontend and never contacts the add-on", func() {
		db, e := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(e).NotTo(HaveOccurred())
		u := auth.User{ID: "admin", Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(db.Create(&u).Error).To(Succeed())
		token, _, e := auth.CreateAPIKey(db, u.ID, "test", auth.RoleAdmin, "secret", nil)
		Expect(e).NotTo(HaveOccurred())
		certs, roots := certificates()
		pins := map[string][32]byte{}
		keys := map[string]ed25519.PublicKey{}
		private := map[string]ed25519.PrivateKey{}
		for _, id := range []string{"owner", "front-a"} {
			pins[id] = sha256.Sum256(certs[id].Certificate[0])
			pub, priv, e := ed25519.GenerateKey(rand.Reader)
			Expect(e).NotTo(HaveOccurred())
			keys[id], private[id] = pub, priv
		}
		revalidate := func(ctx context.Context, a contract.Actor) error { return auth.ValidateAddonActor(ctx, db, a, false) }
		client := &toolClient{}
		owner := httptest.NewUnstartedServer(nil)
		cs := map[string]*gateway.Carrier{}
		for _, id := range []string{"owner", "front-a"} {
			c, e := gateway.NewCarrier(gateway.CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + owner.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
			Expect(e).NotTo(HaveOccurred())
			cs[id] = c
		}
		declared := &toolDeclared{tools: map[string][]string{"notes": {"summarize"}, "other": {"export"}}}
		ownerGateway := &gateway.Gateway{
			Admission: toolAdmission{}, Resolver: toolResolver{}, Runtime: toolRuntime{client}, Revalidate: revalidate,
			Tools: declared,
		}
		var err error
		owner.Config.Handler, err = cs["owner"].OwnerHandler(ownerGateway)
		Expect(err).NotTo(HaveOccurred())
		owner.TLS = cs["owner"].ServerTLS()
		owner.StartTLS()
		DeferCleanup(owner.Close)
		serve := func(g *gateway.Gateway) *httptest.Server {
			app := echo.New()
			cfg := &config.ApplicationConfig{}
			cfg.Auth.APIKeyHMACSecret = "secret"
			app.Use(auth.Middleware(db, cfg))
			app.POST("/api/addons/dispatch", Dispatch(g, db), auth.RequireFeature(db, auth.FeatureAddons), BrowserProtection())
			s := httptest.NewServer(app)
			DeferCleanup(s.Close)
			return s
		}
		frontends := map[string]*httptest.Server{
			"owner frontend":      serve(ownerGateway),
			"forwarding frontend": serve(&gateway.Gateway{Forward: cs["front-a"], Revalidate: revalidate}),
		}
		hc := httpclient.New()
		hc.Timeout = 5 * time.Second
		post := func(s *httptest.Server, addon, args string) (int, string) {
			body := `{"addonId":"` + addon + `","resourceId":"res","operationId":"op","generation":1,"action":"read","arguments":` + args + `}`
			req, e := http.NewRequest("POST", s.URL+"/api/addons/dispatch", bytes.NewBufferString(body))
			Expect(e).NotTo(HaveOccurred())
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			resp, e := hc.Do(req)
			Expect(e).NotTo(HaveOccurred())
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(b)
		}
		// A declared tool passes on both frontends.
		for name, s := range frontends {
			code, _ := post(s, "notes", `{"tool":"summarize","context":{}}`)
			Expect(code).To(Equal(http.StatusOK), name)
		}
		Expect(client.reads.Load()).To(Equal(int32(2)))
		denied := []string{`{"tool":"delete-all"}`, `{"tool":"export"}`, `{"tool":"Summarize"}`, `{"tool":"summarize "}`, `{"Tool":"summarize"}`, `{"tool":"summarіze"}`}
		for _, args := range denied {
			var bodies []string
			for name, s := range frontends {
				code, body := post(s, "notes", args)
				Expect(code).To(Equal(http.StatusForbidden), name+" "+args)
				bodies = append(bodies, body)
			}
			Expect(bodies[0]).To(Equal(bodies[1]), "the same denial from every frontend: "+args)
		}
		Expect(client.reads.Load()).To(Equal(int32(2)), "the add-on is not contacted on a denial")
		// Repeated keys are denied (403) on
		// every frontend, whatever the request decoder let through, and never reach the add-on.
		for _, args := range []string{`{"tool":"summarize","tool":"delete-all"}`, `{"tool":"delete-all","tool":"summarize"}`, `{"tool":"summarize","\u0074ool":"export"}`, `{"tool":"summarize","context":{"a":1,"a":2}}`} {
			for name, s := range frontends {
				code, _ := post(s, "notes", args)
				Expect(code).To(Equal(http.StatusForbidden), name+" "+args)
			}
		}
		Expect(client.reads.Load()).To(Equal(int32(2)), "the add-on is not contacted for a repeated key")
		// A source that cannot answer is unavailable on every frontend, with
		// the same body, and still never reaches the add-on.
		for _, cause := range []error{addons.ErrNotOwner, addons.ErrRecoveryRequired, context.Canceled, errors.New("manifest unavailable")} {
			declared.fail.Store(&cause)
			var bodies []string
			for name, s := range frontends {
				code, body := post(s, "notes", `{"tool":"summarize"}`)
				Expect(code).To(Equal(http.StatusServiceUnavailable), name+" "+cause.Error())
				bodies = append(bodies, body)
			}
			Expect(bodies[0]).To(Equal(bodies[1]), "the same answer from every frontend: "+cause.Error())
		}
		Expect(client.reads.Load()).To(Equal(int32(2)), "the add-on is not contacted while the source is unavailable")
		declared.fail.Store(nil)
		for name, s := range frontends {
			code, _ := post(s, "notes", `{"tool":"summarize"}`)
			Expect(code).To(Equal(http.StatusOK), name+" after recovery")
		}
	})
})
