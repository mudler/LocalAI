// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing/iotest"

	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var frontActor = contract.Actor{UserID: "u", CredentialID: "k", Mode: "named-user"}

// denyResolver always resolves the request to the same host-recorded binding but
// can be told to refuse, which models a resource that was removed or never bound.
type denyResolver struct{ resolved }

func (denyResolver) Resolve(context.Context, contract.Actor, contract.Request) (ResourceKey, error) {
	return ResourceKey{}, ErrForbidden
}

var _ = Describe("owner denial before the first byte", func() {
	owner := func(g *Gateway) *fixtureEnv {
		return carrierFixtureWith(RelayLimits{}, func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(g)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
	}
	ok := func(context.Context, contract.Actor) error { return nil }
	relay := func(env *fixtureEnv, kind string) (*httptest.ResponseRecorder, error) {
		w := httptest.NewRecorder()
		e := env.carriers["front-b"].Relay(context.Background(), frontActor, RelayRequest{Request: contract.Request{AddonID: "sample", OperationID: "op", Generation: 1, Action: "read"}, Kind: kind, Handle: "h"}, w)
		return w, e
	}
	It("returns the owner's forbidden status for an invalid kind and for missing policy", func() {
		for name, g := range map[string]*Gateway{
			"no policy installed": {Revalidate: ok},
			"resolver refuses":    {Revalidate: ok, Admission: &admitted{}, Resolver: denyResolver{}},
		} {
			env := owner(g)
			w, e := relay(env, "file")
			Expect(e).To(Equal(ErrForbidden), name)
			Expect(w.Body.Len()).To(BeZero(), name)
			Expect(w.Header().Get("Content-Type")).To(BeEmpty(), name)
		}
		_, e := relay(owner(&Gateway{Revalidate: ok}), "bogus")
		Expect(e).To(Equal(ErrForbidden))
	})
	It("still reports unavailable when the owner is unable to serve and not forbidden", func() {
		// The owner has policy but no installed runtime for the addon.
		admission := &admitted{}
		env := owner(&Gateway{Revalidate: ok, Admission: admission, Resolver: resolved{}, Runtime: failingRuntime{}})
		_, e := relay(env, "file")
		Expect(e).To(Equal(ErrUnavailable))
	})
	It("reaches the same decision through both frontends and delivers bytes when allowed", func() {
		admission := &admitted{}
		client := runtimeFixture{admission: admission, open: func(context.Context) io.ReadCloser { return io.NopCloser(strings.NewReader("data")) }}
		env := owner(&Gateway{Revalidate: ok, Admission: admission, Resolver: resolved{}, Runtime: currentFixture{client}})
		for _, id := range []string{"front-a", "front-b"} {
			w := httptest.NewRecorder()
			Expect(env.carriers[id].Relay(context.Background(), frontActor, RelayRequest{Request: contract.Request{AddonID: "sample", OperationID: "op", Generation: 1, Action: "read"}, Kind: "file", Handle: "h"}, w)).To(Succeed())
			Expect(w.Body.String()).To(Equal("data"))
		}
		deny := owner(&Gateway{Revalidate: ok, Admission: admission, Resolver: resolved{}, Runtime: currentFixture{client}})
		_, e := relay(deny, "bogus")
		Expect(e).To(Equal(ErrForbidden))
	})
	It("keeps aborting when revocation happens after bytes were written", func() {
		admission := &admitted{}
		var checks atomic.Int32
		client := runtimeFixture{admission: admission, open: func(context.Context) io.ReadCloser {
			return io.NopCloser(iotest.OneByteReader(strings.NewReader("abcdefgh")))
		}}
		env := owner(&Gateway{Admission: admission, Resolver: bigOutput{}, Runtime: currentFixture{client}, Revalidate: func(context.Context, contract.Actor) error {
			// verify, relay, admission closure and the first copy check pass;
			// the second copy check runs after one byte was already written.
			if checks.Add(1) > 4 {
				return ErrForbidden
			}
			return nil
		}})
		_, e := relay(env, "file")
		Expect(e).To(MatchError(ErrUnavailable))
		Expect(checks.Load()).To(BeNumerically(">=", 5))
	})
})

type bigOutput struct{ resolved }

func (bigOutput) Output(context.Context, contract.Actor, contract.Request, string) (contract.OutputDescription, error) {
	return contract.OutputDescription{Handle: "private", Name: "r.bin", MediaType: "application/octet-stream", Size: 8}, nil
}

type failingRuntime struct{}

func (failingRuntime) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return addonruntime.Instance{}, ErrUnavailable
}

var _ = Describe("per kind frontend budgets", func() {
	It("bounds progress, asset and file relays by their own limit", func() {
		limits := DefaultRelayLimits()
		limits.Bytes, limits.ProgressBytes, limits.AssetBytes = 4000, 1000, 100
		var size atomic.Int64
		env := carrierFixtureWith(limits, func(*Carrier) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/addons/audience" {
					w.Write([]byte(`{"audience":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
					return
				}
				io.Copy(io.Discard, r.Body)
				w.Write(make([]byte, size.Load()))
			})
		})
		relay := func(kind string) error {
			return env.carriers["front-a"].Relay(context.Background(), frontActor, RelayRequest{Request: contract.Request{OperationID: "op", Action: "operation-status"}, Kind: kind}, httptest.NewRecorder())
		}
		size.Store(2000)
		Expect(relay("file")).To(Succeed())
		Expect(relay("progress")).To(MatchError(ErrUnavailable))
		Expect(relay("asset")).To(MatchError(ErrUnavailable))
		size.Store(50)
		Expect(relay("progress")).To(Succeed())
		Expect(relay("asset")).To(Succeed())
		size.Store(4001)
		Expect(relay("file")).To(MatchError(ErrUnavailable))
		Expect(relay("bogus")).To(MatchError(ErrForbidden))
	})
})

var _ = Describe("certificate pins", func() {
	It("rejects a CA signed peer without a pin on the owner side", func() {
		var revalidated atomic.Int32
		env := carrierFixtureWith(RelayLimits{}, func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(&Gateway{Revalidate: func(context.Context, contract.Actor) error { revalidated.Add(1); return nil }})
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		get := func(id string) int {
			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: env.roots, Certificates: []tls.Certificate{env.certs[id]}, MinVersion: tls.VersionTLS13}}}
			DeferCleanup(client.CloseIdleConnections)
			resp, e := client.Get("https://" + env.server.Listener.Addr().String() + "/addons/audience")
			Expect(e).NotTo(HaveOccurred())
			resp.Body.Close()
			return resp.StatusCode
		}
		Expect(get("front-a")).To(Equal(200))
		Expect(get("stranger")).To(Equal(403))
		// The unpinned peer cannot dispatch either, even with a valid-looking API.
		env.carriers["front-a"].client.Transport.(*http.Transport).TLSClientConfig.Certificates = []tls.Certificate{env.certs["stranger"]}
		_, e := env.carriers["front-a"].Dispatch(context.Background(), frontActor, contract.Request{OperationID: "op", Action: "operation-status"})
		Expect(e).To(Equal(ErrUnavailable))
		Expect(revalidated.Load()).To(BeZero())
	})
	It("refuses an owner whose CA signed certificate is not the pinned owner", func() {
		var hits atomic.Int32
		env := carrierFixtureWith(RelayLimits{}, func(c *Carrier) http.Handler {
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) })
		})
		impostor := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Write([]byte(`{"audience":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
		}))
		impostor.TLS = &tls.Config{Certificates: []tls.Certificate{env.certs["stranger"]}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: env.roots, MinVersion: tls.VersionTLS13}
		impostor.StartTLS()
		DeferCleanup(impostor.Close)
		c, e := NewCarrier(CarrierConfig{InstanceID: "front-a", OwnerID: "owner", OwnerAddress: "https://" + impostor.Listener.Addr().String(), Certificate: env.certs["front-a"], Roots: env.roots, Pins: env.pins, SigningKey: env.private["front-a"], VerificationKeys: env.keys})
		Expect(e).NotTo(HaveOccurred())
		_, e = c.Dispatch(context.Background(), frontActor, contract.Request{OperationID: "op", Action: "operation-status"})
		Expect(e).To(Equal(ErrUnavailable))
		Expect(hits.Load()).To(BeZero())
		// Control: pinning the impostor's certificate would have connected, so
		// the pin and not the CA decided the outcome.
		pins := map[string][32]byte{}
		for k, v := range env.pins {
			pins[k] = v
		}
		pins["owner"] = sha256Of(env.certs["stranger"])
		c, e = NewCarrier(CarrierConfig{InstanceID: "front-a", OwnerID: "owner", OwnerAddress: "https://" + impostor.Listener.Addr().String(), Certificate: env.certs["front-a"], Roots: env.roots, Pins: pins, SigningKey: env.private["front-a"], VerificationKeys: env.keys})
		Expect(e).NotTo(HaveOccurred())
		_, _ = c.Dispatch(context.Background(), frontActor, contract.Request{OperationID: "op", Action: "operation-status"})
		Expect(hits.Load()).To(BeNumerically(">", 0))
	})
})

func sha256Of(c tls.Certificate) [32]byte { return sha256.Sum256(c.Certificate[0]) }

var _ = Describe("installed generation assets", func() {
	It("serves declared assets for the actor's own operation and generation only", func() {
		admin := contract.Actor{UserID: "admin", CredentialID: "k", Mode: "named-user"}
		m := ownerManager(admin, true)
		limits := DefaultRelayLimits()
		limits.AssetBytes = 16
		g := &Gateway{Manager: m, Limits: limits, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		fetch := func(a contract.Actor, op string, generation uint64, handle string) (*httptest.ResponseRecorder, error) {
			w := httptest.NewRecorder()
			e := g.Relay(context.Background(), a, RelayRequest{Request: contract.Request{AddonID: "sample", OperationID: op, Generation: generation, Action: "read"}, Kind: "asset", Handle: handle}, w)
			return w, e
		}
		w, e := fetch(admin, "install", 1, "ui.css")
		Expect(e).NotTo(HaveOccurred())
		Expect(w.Body.String()).To(Equal("body{}"))
		Expect(w.Header().Get("Content-Type")).To(Equal("text/css"))
		Expect(w.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))

		// The entry is 18 bytes, over the 16 byte asset budget: refused before any byte.
		w, e = fetch(admin, "install", 1, "ui.js")
		Expect(e).To(Equal(ErrForbidden))
		Expect(w.Body.Len()).To(BeZero())
		limits.AssetBytes = 1 << 10
		g.Limits = limits
		w, e = fetch(admin, "install", 1, "ui.js")
		Expect(e).NotTo(HaveOccurred())
		Expect(w.Body.String()).To(Equal("export default {};"))
		Expect(w.Header().Get("Content-Type")).To(Equal("text/javascript"))

		// Generation must match the installed generation.
		_, e = fetch(admin, "install", 2, "ui.js")
		Expect(e).To(Equal(ErrForbidden))
		// The operation must exist and belong to the actor.
		_, e = fetch(admin, "unknown", 1, "ui.js")
		Expect(e).To(Equal(ErrForbidden))
		other := contract.Actor{UserID: "other", CredentialID: "k", Mode: "named-user"}
		_, e = fetch(other, "install", 1, "ui.js")
		Expect(e).To(Equal(ErrForbidden))
		// Only declared assets, never other package files or traversal.
		for _, handle := range []string{"secret.txt", "bin/sample", "manifest.json", "../manifest.json", "/etc/passwd", "ui/../ui.js", ""} {
			w, e = fetch(admin, "install", 1, handle)
			Expect(e).To(Equal(ErrForbidden), handle)
			Expect(w.Body.Len()).To(BeZero(), handle)
		}
		// Another addon identity has no installed generation.
		w = httptest.NewRecorder()
		e = g.Relay(context.Background(), admin, RelayRequest{Request: contract.Request{AddonID: "absent", OperationID: "install", Generation: 1}, Kind: "asset", Handle: "ui.js"}, w)
		Expect(e).To(HaveOccurred())
		Expect(w.Body.Len()).To(BeZero())
		// Without an owner journal the frontend cannot serve assets locally.
		e = (&Gateway{Revalidate: g.Revalidate}).asset(context.Background(), admin, RelayRequest{Request: contract.Request{AddonID: "sample", OperationID: "install", Generation: 1}, Handle: "ui.js"}, httptest.NewRecorder())
		Expect(e).To(Equal(ErrUnavailable))
	})
})
