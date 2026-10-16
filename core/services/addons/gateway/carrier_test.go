// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"io"
	"net/http/httptest"
	"strings"
	"time"
)

var _ = Describe("owner bootstrap", func() {
	It("rotates the enforced audience on restart and rejects captured requests", func() {
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		cfg := VerifierConfig{Keys: map[string]ed25519.PublicKey{"front": pub}, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		first, err := NewForwardVerifier(cfg)
		Expect(err).NotTo(HaveOccurred())
		actor := contract.Actor{UserID: "u", CredentialID: "k", Mode: "named-user"}
		m := ForwardedRequest{PeerID: "front", Audience: first.Audience(), Actor: actor, OperationID: "op", Nonce: "nonce", ExpiresAt: time.Now().Add(30 * time.Second), Body: []byte(`{}`)}
		m.Sign(key)
		_, err = first.Verify(context.Background(), PeerIdentity{ID: "front", Verified: true}, m)
		Expect(err).NotTo(HaveOccurred())
		_, err = first.Verify(context.Background(), PeerIdentity{ID: "front", Verified: true}, m)
		Expect(err).To(HaveOccurred())
		second, err := NewForwardVerifier(cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(second.Audience()).NotTo(Equal(first.Audience()))
		_, err = second.Verify(context.Background(), PeerIdentity{ID: "front", Verified: true}, m)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("mTLS carrier", func() {
	It("builds a carrier only from complete pinned identities", func() {
		certs, roots := certificates()
		pins := map[string][32]byte{}
		for _, id := range []string{"owner", "front-a"} {
			pins[id] = sha256Of(certs[id])
		}
		_, key, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		valid := func() CarrierConfig {
			p := map[string][32]byte{}
			for k, v := range pins {
				p[k] = v
			}
			return CarrierConfig{InstanceID: "front-a", OwnerID: "owner", OwnerAddress: "https://127.0.0.1:9443", Certificate: certs["front-a"], Roots: roots, Pins: p, SigningKey: key}
		}
		_, err = NewCarrier(valid())
		Expect(err).NotTo(HaveOccurred())
		// A later change to the caller's map must not change the pinned identities.
		cfg := valid()
		c, err := NewCarrier(cfg)
		Expect(err).NotTo(HaveOccurred())
		delete(cfg.Pins, "owner")
		Expect(c.config.Pins).To(HaveKey("owner"))
		Expect(c.config.Limits).To(Equal(DefaultRelayLimits()))
		for name, mutate := range map[string]func(*CarrierConfig){
			"owner pin missing":      func(c *CarrierConfig) { delete(c.Pins, "owner") },
			"own pin missing":        func(c *CarrierConfig) { delete(c.Pins, "front-a") },
			"plain http owner":       func(c *CarrierConfig) { c.OwnerAddress = "http://127.0.0.1:9443" },
			"owner path":             func(c *CarrierConfig) { c.OwnerAddress = "https://127.0.0.1:9443/x" },
			"owner credentials":      func(c *CarrierConfig) { c.OwnerAddress = "https://u:p@127.0.0.1:9443" },
			"no trust roots":         func(c *CarrierConfig) { c.Roots = nil },
			"no client certificate":  func(c *CarrierConfig) { c.Certificate = tls.Certificate{} },
			"short signing key":      func(c *CarrierConfig) { c.SigningKey = key[:8] },
			"empty instance":         func(c *CarrierConfig) { c.InstanceID = "" },
			"partial limit override": func(c *CarrierConfig) { c.Limits = RelayLimits{Bytes: 1 << 20} },
		} {
			cfg := valid()
			mutate(&cfg)
			_, err := NewCarrier(cfg)
			Expect(err).To(MatchError(ErrForbidden), name)
		}
	})
	It("types an unknown mutation outcome as unavailable without losing its operation", func() {
		var unknown error = &OutcomeUnknown{OperationID: "original"}
		Expect(errors.Is(unknown, ErrUnavailable)).To(BeTrue())
		Expect(errors.Is(unknown, ErrForbidden)).To(BeFalse())
		var typed *OutcomeUnknown
		Expect(errors.As(fmt.Errorf("wrapped: %w", unknown), &typed)).To(BeTrue())
		Expect(typed.OperationID).To(Equal("original"))
	})
})

var _ = Describe("resource relay admission", func() {
	It("denies private relays without host resolution and admission", func() {
		ok := func(context.Context, contract.Actor) error { return nil }
		admission := &admitted{}
		client := runtimeFixture{admission: admission, open: func(context.Context) io.ReadCloser { return io.NopCloser(strings.NewReader("data")) }}
		request := contract.Request{AddonID: "sample", Generation: 1, OperationID: "op", Action: "read", Arguments: []byte(`{}`)}
		for name, g := range map[string]*Gateway{
			"nothing":        {Revalidate: ok, Runtime: currentFixture{client}},
			"resolver only":  {Revalidate: ok, Resolver: resolved{}, Runtime: currentFixture{client}},
			"admission only": {Revalidate: ok, Admission: admission, Runtime: currentFixture{client}},
		} {
			for _, kind := range []string{"file", "json"} {
				w := httptest.NewRecorder()
				Expect(g.Relay(context.Background(), contract.Actor{}, RelayRequest{Request: request, Kind: kind}, w)).To(MatchError(ErrForbidden), name+" "+kind)
				Expect(w.Body.Len()).To(BeZero(), name+" "+kind)
			}
			_, err := g.Dispatch(context.Background(), contract.Actor{}, request)
			Expect(err).To(MatchError(ErrForbidden), name)
		}
		// The same request succeeds once the policies and the artifact store
		// are installed.
		g := &Gateway{Revalidate: ok, Admission: admission, Resolver: resolved{}, Runtime: currentFixture{client}, Artifacts: dataArtifacts("data")}
		w := httptest.NewRecorder()
		Expect(g.Relay(context.Background(), contract.Actor{}, RelayRequest{Request: request, Kind: "file", Handle: fakeArtifactID}, w)).To(Succeed())
		Expect(w.Body.String()).To(Equal("data"))
		Expect(g.Relay(context.Background(), contract.Actor{}, RelayRequest{Request: request, Kind: "json"}, httptest.NewRecorder())).To(Succeed())
	})
})

var _ = Describe("runtime admission", func() {
	It("adopts read outputs as private artifacts inside admission and replaces a forged resource identity", func() {
		admission := &admitted{}
		client := outputRuntime{runtimeFixture{admission: admission, open: func(context.Context) io.ReadCloser { return io.NopCloser(strings.NewReader("data")) }}}
		files := dataArtifacts("data")
		g := &Gateway{Admission: admission, Resolver: resolved{}, Runtime: currentFixture{client}, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		// The runtime accepts only the host-recorded resource, so success proves the
		// forged value from the request was not used.
		request := contract.Request{AddonID: "sample", ResourceID: "forged", Generation: 1, OperationID: "op", Action: "read", Arguments: []byte(`{}`)}
		// Without an artifact store a result with outputs is denied whole, and
		// the addon handle never reaches the caller.
		result, e := g.Dispatch(context.Background(), contract.Actor{}, request)
		Expect(e).To(MatchError(ErrForbidden))
		Expect(result.Outputs).To(BeEmpty())
		g.Artifacts = files
		result, e = g.Dispatch(context.Background(), contract.Actor{}, request)
		Expect(e).NotTo(HaveOccurred())
		Expect(result.Outputs).To(HaveLen(1))
		Expect(result.Outputs[0].Handle).To(Equal(fakeArtifactID))
		Expect(result.Outputs[0].Handle).NotTo(Equal("private"))
		Expect(files.adopted.Load()).To(Equal(int32(1)))
		Expect(admission.active.Load()).To(BeFalse())
		// With no current runtime the host reports the owner unavailable, not forbidden.
		g.Runtime = nil
		_, e = g.Dispatch(context.Background(), contract.Actor{}, request)
		Expect(e).To(MatchError(ErrUnavailable))
	})
})

// outputRuntime answers a read with one addon output handle.
type outputRuntime struct{ runtimeFixture }

func (o outputRuntime) Read(ctx context.Context, e contract.Envelope) (contract.Result, error) {
	if _, err := o.runtimeFixture.Read(ctx, e); err != nil {
		return contract.Result{}, err
	}
	return contract.Result{Payload: []byte(`{"ok":true}`), Outputs: []contract.OutputDescription{{Handle: "private", Name: "report.csv", MediaType: "text/csv", Size: 4}}}, nil
}

var _ = Describe("gateway limits", func() {
	It("validates finite relay budgets and prevents disabling polling bounds", func() {
		l := DefaultRelayLimits()
		Expect(l.Validate()).To(Succeed())
		l.PollInterval = 0
		Expect(l.Validate()).To(HaveOccurred())
		l = DefaultRelayLimits()
		l.Concurrency = 0
		Expect(l.Validate()).To(HaveOccurred())
		l = DefaultRelayLimits()
		l.Bytes = 0
		Expect(l.Validate()).To(HaveOccurred())
	})
})

var _ = Describe("file result serving bound", func() {
	It("refuses at ingest a file result larger than the relay file bound so nothing unservable is staged", func() {
		admission := &admitted{}
		client := outputRuntime{runtimeFixture{admission: admission, open: func(context.Context) io.ReadCloser { return io.NopCloser(strings.NewReader("data")) }}}
		files := dataArtifacts("data")
		limits := DefaultRelayLimits()
		limits.Bytes = 3 // the runtime output declares 4 bytes
		g := &Gateway{Limits: limits, Artifacts: files, Admission: admission, Resolver: resolved{}, Runtime: currentFixture{client}, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		request := contract.Request{AddonID: "sample", ResourceID: "forged", Generation: 1, OperationID: "op", Action: "read", Arguments: []byte(`{}`)}
		result, e := g.Dispatch(context.Background(), contract.Actor{}, request)
		Expect(e).To(MatchError(ErrForbidden))
		Expect(result.Outputs).To(BeEmpty())
		Expect(files.adopted.Load()).To(BeZero())
		// At the bound the result is adopted.
		limits.Bytes = 4
		g.Limits = limits
		result, e = g.Dispatch(context.Background(), contract.Actor{}, request)
		Expect(e).NotTo(HaveOccurred())
		Expect(result.Outputs).To(HaveLen(1))
		Expect(files.adopted.Load()).To(Equal(int32(1)))
	})
})
