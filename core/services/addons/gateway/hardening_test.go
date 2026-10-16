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
	"sync"
	"sync/atomic"
	"testing/iotest"
	"time"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var frontActor = contract.Actor{UserID: "u", CredentialID: "k", Mode: "named-user"}

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
			"no policy installed":    {Revalidate: ok},
			"artifact store refuses": {Revalidate: ok, Artifacts: &fakeArtifacts{err: ErrForbidden}},
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
		// The owner has an artifact store that cannot serve right now.
		env := owner(&Gateway{Revalidate: ok, Artifacts: &fakeArtifacts{err: ErrUnavailable}})
		_, e := relay(env, "file")
		Expect(e).To(Equal(ErrUnavailable))
	})
	It("reaches the same decision through both frontends and delivers bytes when allowed", func() {
		env := owner(&Gateway{Revalidate: ok, Artifacts: dataArtifacts("data")})
		for _, id := range []string{"front-a", "front-b"} {
			w := httptest.NewRecorder()
			Expect(env.carriers[id].Relay(context.Background(), frontActor, RelayRequest{Request: contract.Request{AddonID: "sample", OperationID: "op", Generation: 1, Action: "read"}, Kind: "file", Handle: fakeArtifactID}, w)).To(Succeed())
			Expect(w.Body.String()).To(Equal("data"))
			// The receiving frontend repeats the owner's attachment headers.
			Expect(w.Header().Get("Content-Disposition")).To(HavePrefix("attachment;"))
			Expect(w.Header().Get("Content-Length")).To(Equal("4"))
			Expect(w.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
			Expect(w.Header().Get("Cache-Control")).To(Equal("private, no-store"))
		}
		deny := owner(&Gateway{Revalidate: ok, Artifacts: dataArtifacts("data")})
		_, e := relay(deny, "bogus")
		Expect(e).To(Equal(ErrForbidden))
	})
	It("keeps aborting when revocation happens after bytes were written", func() {
		// The store streams one byte and then loses the permission: the
		// status is already committed, so the owner aborts the stream and the
		// receiving frontend never sees a clean end of file.
		files := &fakeArtifacts{
			art: Artifact{ID: fakeArtifactID, Name: "r.bin", MediaType: "application/octet-stream", Size: 8},
			open: func(context.Context) io.ReadCloser {
				return io.NopCloser(iotest.OneByteReader(io.MultiReader(strings.NewReader("a"), iotest.ErrReader(ErrForbidden))))
			},
		}
		env := owner(&Gateway{Artifacts: files, Revalidate: ok})
		w := httptest.NewRecorder()
		e := env.carriers["front-b"].Relay(context.Background(), frontActor, RelayRequest{Request: contract.Request{AddonID: "sample", OperationID: "op", Generation: 1, Action: "read"}, Kind: "file", Handle: fakeArtifactID}, w)
		Expect(e).To(HaveOccurred())
		Expect(e).NotTo(Equal(ErrForbidden))
		Expect(w.Body.String()).NotTo(Equal("abcdefgh"))
	})
})

var _ = Describe("per kind frontend budgets", func() {
	It("bounds progress, asset and file relays by their own limit", func() {
		limits := DefaultRelayLimits()
		limits.Bytes, limits.ProgressBytes, limits.AssetBytes = 4000, 1000, 100
		var size atomic.Int64
		env := carrierFixtureWith(limits, func(*Carrier) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/addons/audience" {
					_, _ = w.Write([]byte(`{"audience":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = w.Write(make([]byte, size.Load()))
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
			_ = resp.Body.Close()
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
			_, _ = w.Write([]byte(`{"audience":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
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

var _ = Describe("installed generation assets", Label("assets"), func() {
	It("serves declared assets of the current enabled generation by package digest only", func() {
		admin := contract.Actor{UserID: "admin", CredentialID: "k", Mode: "named-user"}
		reader := contract.Actor{UserID: "reader", CredentialID: "k", Mode: "named-user"}
		m := ownerManager(admin, true)
		_, e := m.Submit(context.Background(), admin, addons.LifecycleCommand{AddonID: "sample", OperationID: "enable", Action: "enable", Generation: 1})
		Expect(e).NotTo(HaveOccurred())
		status, e := m.Status(context.Background(), "sample")
		Expect(e).NotTo(HaveOccurred())
		digest := strings.TrimPrefix(status.Generations[0].Receipt.Digest, "sha256:")
		Expect(digest).To(HaveLen(64))
		limits := DefaultRelayLimits()
		limits.AssetBytes = 16
		g := &Gateway{Manager: m, Limits: limits, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		fetch := func(a contract.Actor, id, digest, handle string) (*httptest.ResponseRecorder, error) {
			w := httptest.NewRecorder()
			e := g.Relay(context.Background(), a, RelayRequest{Request: contract.Request{AddonID: id, OperationID: "asset-op", Action: "app-asset"}, Kind: "asset", Handle: handle, Digest: digest}, w)
			return w, e
		}
		// Any caller the route admitted may read the code of an enabled addon.
		w, e := fetch(reader, "sample", digest, "ui.css")
		Expect(e).NotTo(HaveOccurred())
		Expect(w.Body.String()).To(Equal("body{}"))
		Expect(w.Header().Get("Content-Type")).To(Equal("text/css"))
		Expect(w.Header().Get("X-Content-Type-Options")).To(Equal("nosniff"))
		Expect(w.Header().Get("Cache-Control")).To(Equal("private, max-age=31536000, immutable"))
		Expect(w.Header().Get("Content-Length")).To(Equal("6"))
		Expect(w.Header().Get("Content-Disposition")).To(BeEmpty())

		// The entry is 18 bytes, over the 16 byte asset budget: refused before any byte.
		w, e = fetch(reader, "sample", digest, "ui.js")
		Expect(e).To(Equal(ErrForbidden))
		Expect(w.Body.Len()).To(BeZero())
		limits.AssetBytes = 1 << 10
		g.Limits = limits
		w, e = fetch(reader, "sample", digest, "ui.js")
		Expect(e).NotTo(HaveOccurred())
		Expect(w.Body.String()).To(Equal("export default {};"))
		Expect(w.Header().Get("Content-Type")).To(Equal("text/javascript"))

		// Only the digest of the current package, never another or an earlier one.
		for _, wrong := range []string{"", strings.Repeat("f", 64), "sha256:" + digest, strings.ToUpper(digest), digest[:63], digest + "0"} {
			w, e = fetch(reader, "sample", wrong, "ui.js")
			Expect(e).To(Equal(ErrForbidden), wrong)
			Expect(w.Body.Len()).To(BeZero(), wrong)
			Expect(w.Header().Get("Cache-Control")).NotTo(ContainSubstring("immutable"), "denials are never cached as immutable")
		}
		// Only declared assets, never other package files, directories or traversal.
		for _, handle := range []string{"secret.txt", "bin/sample", "manifest.json", "../manifest.json", "/etc/passwd", "ui/../ui.js", "./ui.js", "ui.js/", "UI.JS", ""} {
			w, e = fetch(reader, "sample", digest, handle)
			Expect(e).To(Equal(ErrForbidden), handle)
			Expect(w.Body.Len()).To(BeZero(), handle)
		}
		// Another addon identity has no package with this digest.
		w, e = fetch(reader, "absent", digest, "ui.js")
		Expect(e).To(Equal(ErrForbidden))
		Expect(w.Body.Len()).To(BeZero())
		// Without an owner journal the frontend cannot serve assets locally.
		e = (&Gateway{Revalidate: g.Revalidate}).asset(context.Background(), reader, RelayRequest{Request: contract.Request{AddonID: "sample"}, Handle: "ui.js", Digest: digest}, httptest.NewRecorder())
		Expect(e).To(Equal(ErrUnavailable))

		// A disabled addon serves nothing, to a reader or an administrator.
		_, e = m.Submit(context.Background(), admin, addons.LifecycleCommand{AddonID: "sample", OperationID: "disable", Action: "disable", Generation: 1})
		Expect(e).NotTo(HaveOccurred())
		for _, who := range []contract.Actor{reader, admin} {
			w, e = fetch(who, "sample", digest, "ui.js")
			Expect(e).To(Equal(ErrForbidden))
			Expect(w.Body.Len()).To(BeZero())
		}
	})
})

// deadlineWriter records every write deadline the relay arms on the response.
type deadlineWriter struct {
	*httptest.ResponseRecorder
	mu        sync.Mutex
	deadlines []time.Time
}

func (d *deadlineWriter) SetWriteDeadline(t time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deadlines = append(d.deadlines, t)
	return nil
}

var _ = Describe("relay write deadline", func() {
	It("never expires the response deadline after the relay returned", func() {
		g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }}
		for i := 0; i < 200; i++ {
			w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
			e := g.Relay(context.Background(), frontActor, RelayRequest{Request: contract.Request{OperationID: "op", Action: "operation-status"}, Kind: "status"}, w)
			Expect(e).To(MatchError(ErrUnavailable))
			time.Sleep(time.Millisecond)
			w.mu.Lock()
			for _, t := range w.deadlines {
				// The owner still writes a typed denial after Relay returns.
				Expect(time.Until(t)).To(BeNumerically(">", time.Second), "iteration %d", i)
			}
			w.mu.Unlock()
		}
	})
})
