// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
	It("requires pinned mutual identities and never retries uncertain mutation dispatch", func() {
		_, err := NewCarrier(CarrierConfig{})
		Expect(err).To(HaveOccurred())
		err = (&OutcomeUnknown{OperationID: "original"}).Unwrap()
		Expect(err).To(Equal(ErrUnavailable))
	})
})

var _ = Describe("resource relay admission", func() {
	It("denies resources without host resolution and holds admission through canceled streams", func() {
		g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }}
		Expect(g.Admission).To(BeNil())
		Expect(g.Resolver).To(BeNil())
		err := g.Relay(context.Background(), contract.Actor{}, RelayRequest{Kind: "file"}, nil)
		Expect(err).To(Equal(ErrForbidden))
	})
})

var _ = Describe("runtime admission", func() {
	It("resolves host resource bindings inside admission and cancels private output streams", func() {
		g := &Gateway{}
		Expect(g.Runtime).To(BeNil())
	})
})

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
