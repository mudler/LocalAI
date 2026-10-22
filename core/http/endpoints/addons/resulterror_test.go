// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type deadRuntime struct{}

func (deadRuntime) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return addonruntime.Instance{}, addonruntime.ErrRuntime
}

// refusedRuntime is a running add-on whose every read is refused as invalid,
// the way the runtime client reports an HTTP 400 of the add-on.
type refusingClient struct{ contract.Runtime }

func (refusingClient) Read(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, &addonruntime.Refusal{Status: 400, Reason: "invalid request"}
}

type refusedRuntime struct{}

func (refusedRuntime) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return addonruntime.Instance{Generation: 1, Incarnation: "current", Client: refusingClient{}}, nil
}

type openAdmission struct{}

func (openAdmission) Admit(ctx context.Context, _ contract.Actor, _ gateway.ResourceKey, _ string, f func(context.Context) error) error {
	return f(ctx)
}

type boundResolver struct{}

func (boundResolver) Resolve(_ context.Context, _ contract.Actor, r contract.Request) (gateway.ResourceKey, error) {
	return gateway.ResourceKey{AddonID: r.AddonID, ResourceID: "resource"}, nil
}
func (boundResolver) Output(context.Context, contract.Actor, contract.Request, string) (contract.OutputDescription, error) {
	return contract.OutputDescription{}, gateway.ErrForbidden
}

// errorBody is the wire shape the endpoint emits for every failure.
type errorBody struct {
	Error map[string]any `json:"error"`
}

var _ = Describe("dispatch failure responses on the registered route", func() {
	const secret = "secret"
	var token string
	var raw map[string]any
	// serve mounts the real handler behind the same middleware chain as the
	// registered /api/addons group and returns a caller for typed requests.
	serve := func(g *gateway.Gateway) func(id, action string) (int, errorBody) {
		gdb, e := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(e).NotTo(HaveOccurred())
		u := auth.User{ID: "admin", Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(gdb.Create(&u).Error).To(Succeed())
		token, _, e = auth.CreateAPIKey(gdb, u.ID, "test", auth.RoleAdmin, secret, nil)
		Expect(e).NotTo(HaveOccurred())
		cfg := &config.ApplicationConfig{}
		cfg.Auth.APIKeyHMACSecret = secret
		app := echo.New()
		app.Use(auth.Middleware(gdb, cfg))
		group := app.Group("/api/addons", auth.RequireFeature(gdb, auth.FeatureAddons), auth.CSRFMiddleware(), BrowserProtection())
		group.POST("/dispatch", Dispatch(g, gdb))
		return func(id, action string) (int, errorBody) {
			args := `{}`
			if action == "install" || action == "enable" {
				args = `{"sourceId":"absent","version":"1.0.0"}`
			}
			body := `{"addonId":"sample","operationId":"` + id + `","generation":1,"action":"` + action + `","arguments":` + args + `}`
			r := httptest.NewRequest(http.MethodPost, "/api/addons/dispatch", bytes.NewBufferString(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			raw = nil
			_ = json.Unmarshal(w.Body.Bytes(), &raw)
			var out errorBody
			ExpectWithOffset(1, json.Unmarshal(w.Body.Bytes(), &out)).To(Succeed(), w.Body.String())
			return w.Code, out
		}
	}
	expectUnknown := func(code int, body errorBody, id string) {
		ExpectWithOffset(1, code).To(Equal(http.StatusServiceUnavailable))
		ExpectWithOffset(1, body.Error["type"]).To(Equal("outcome_unknown"))
		ExpectWithOffset(1, body.Error["operationId"]).To(Equal(id))
	}
	admin := contract.Actor{UserID: "admin", Mode: "named-user"}
	allow := func(context.Context, contract.Actor) error { return nil }

	It("reports an unknown mutation outcome from a forwarding frontend with the original operation", func() {
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
		// The owner answers the audience bootstrap and then fails every forward.
		owner := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/addons/audience" {
				_ = json.NewEncoder(w).Encode(map[string]string{"audience": strings.Repeat("a", 64)})
				return
			}
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		carrier := func(id string) *gateway.Carrier {
			c, e := gateway.NewCarrier(gateway.CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + owner.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
			Expect(e).NotTo(HaveOccurred())
			return c
		}
		owner.TLS = carrier("owner").ServerTLS()
		owner.StartTLS()
		DeferCleanup(owner.Close)
		call := serve(&gateway.Gateway{Forward: carrier("front-a"), Revalidate: allow})
		code, body := call("lost-op", "enable")
		expectUnknown(code, body, "lost-op")
		// A read of the same owner is plain unavailability.
		code, body = call("status-op", "operation-status")
		Expect(code).To(Equal(http.StatusServiceUnavailable))
		Expect(body.Error["type"]).To(Equal("addon_error"))
		Expect(body.Error).NotTo(HaveKey("operationId"))
	})
	It("documents every field the error responses emit in the Swagger schema", func() {
		spec, e := os.ReadFile(filepath.Join("..", "..", "..", "..", "swagger", "swagger.json"))
		Expect(e).NotTo(HaveOccurred())
		var doc map[string]any
		Expect(json.Unmarshal(spec, &doc)).To(Succeed())
		at := func(from any, keys ...string) map[string]any {
			m, _ := from.(map[string]any)
			for _, k := range keys {
				m, _ = m[k].(map[string]any)
			}
			return m
		}
		definition := func(ref any) map[string]any {
			name, _ := ref.(string)
			return at(doc, "definitions", strings.TrimPrefix(name, "#/definitions/"))
		}
		response := at(doc, "paths", "/api/addons/dispatch", "post", "responses", "503", "schema")
		Expect(response).NotTo(BeNil())
		envelope := definition(response["$ref"])
		Expect(envelope).NotTo(BeNil())
		inner := at(envelope, "properties", "error")
		documented := at(inner, "properties")
		if documented == nil {
			documented = at(definition(inner["$ref"]), "properties")
		}
		call := serve(&gateway.Gateway{Revalidate: allow})
		for _, action := range []string{"enable", "operation-status"} {
			status, _ := call("doc-op", action)
			Expect(status).To(Equal(http.StatusServiceUnavailable))
			emitted, ok := raw["error"].(map[string]any)
			Expect(ok).To(BeTrue())
			for field := range emitted {
				Expect(documented).To(HaveKey(field), action+" emits "+field)
			}
		}
	})
	It("reports an unavailable read as plain unavailability", func() {
		call := serve(&gateway.Gateway{Revalidate: allow, Admission: openAdmission{}, Resolver: boundResolver{}, Runtime: deadRuntime{}})
		code, body := call("read-op", "read")
		Expect(code).To(Equal(http.StatusServiceUnavailable))
		Expect(body.Error["type"]).To(Equal("addon_error"))
		Expect(body.Error).NotTo(HaveKey("operationId"))
	})
	It("answers an add-on's invalid-request refusal as a typed 400 from the owner and from a forwarding frontend", func() {
		allowAll := func(context.Context, contract.Actor) error { return nil }
		ownerGateway := &gateway.Gateway{Revalidate: allowAll, Admission: openAdmission{}, Resolver: boundResolver{}, Runtime: refusedRuntime{}}
		code, body := serve(ownerGateway)("refused-op", "read")
		Expect(code).To(Equal(http.StatusBadRequest))
		Expect(body.Error["type"]).To(Equal("invalid_request"))
		Expect(body.Error).NotTo(HaveKey("operationId"))

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
		server := httptest.NewUnstartedServer(nil)
		carrier := func(id string) *gateway.Carrier {
			c, e := gateway.NewCarrier(gateway.CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + server.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
			Expect(e).NotTo(HaveOccurred())
			return c
		}
		ownerCarrier := carrier("owner")
		handler, e := ownerCarrier.OwnerHandler(ownerGateway)
		Expect(e).NotTo(HaveOccurred())
		server.Config.Handler = handler
		server.TLS = ownerCarrier.ServerTLS()
		server.StartTLS()
		DeferCleanup(server.Close)
		code, body = serve(&gateway.Gateway{Forward: carrier("front-a"), Revalidate: allowAll})("refused-op", "read")
		Expect(code).To(Equal(http.StatusBadRequest))
		Expect(body.Error["type"]).To(Equal("invalid_request"))
		Expect(body.Error).NotTo(HaveKey("operationId"))
	})
	It("reports a denial as forbidden", func() {
		call := serve(&gateway.Gateway{Revalidate: allow})
		code, body := call("denied-op", "apply")
		Expect(code).To(Equal(http.StatusForbidden))
		Expect(body.Error["type"]).To(Equal("addon_error"))
		Expect(body.Error).NotTo(HaveKey("operationId"))
	})
	It("reports an unclassified account check error as unavailable and a definite denial as forbidden", func() {
		plain := func(context.Context, contract.Actor) error {
			return errors.New("backend answered something unexpected")
		}
		code, body := serve(&gateway.Gateway{Revalidate: plain})("plain-op", "operation-status")
		Expect(code).To(Equal(http.StatusServiceUnavailable))
		Expect(body.Error["type"]).To(Equal("addon_error"))
		denied := func(context.Context, contract.Actor) error {
			return fmt.Errorf("account paused: %w", gateway.ErrForbidden)
		}
		code, _ = serve(&gateway.Gateway{Revalidate: denied})("denied-op", "operation-status")
		Expect(code).To(Equal(http.StatusForbidden))
	})
	It("reports an unknown mutation outcome from the owner's own frontend with the original operation", func() {
		root := filepath.Join(GinkgoT().TempDir(), "owner")
		m, e := managerpkg.NewManager(context.Background(), managerpkg.OwnerConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "disk", Root: root}, catalog.Catalog{}, &packages.Installer{Root: filepath.Join(root, "packages")}, func(context.Context, contract.Actor) error { return nil })
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(m.Close)
		_, e = m.Submit(context.Background(), admin, managerpkg.LifecycleCommand{AddonID: "sample", OperationID: "seed", Action: "install", SourceID: "absent", Version: "1.0.0"})
		Expect(e).To(HaveOccurred())
		// The journal write fails after the owner recorded the intent in memory.
		journal := filepath.Join(root, "journal.json")
		Expect(os.Rename(journal, journal+".saved")).To(Succeed())
		Expect(os.Mkdir(journal, 0700)).To(Succeed())
		call := serve(&gateway.Gateway{Manager: m, Revalidate: allow})
		code, body := call("second", "install")
		expectUnknown(code, body, "second")
		// The caller resolves the original operation by polling its identifier.
		code, _ = call("second", "operation-status")
		Expect(code).To(Equal(http.StatusOK))
	})
})
