// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
	"time"
)

func TestGateway(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Addon gateway") }

var _ = Describe("Forward verifier", func() {
	It("rejects untrusted peers wrong audience body expiry replay and revoked credentials", func() {
		pub, priv, e := ed25519.GenerateKey(rand.Reader)
		Expect(e).NotTo(HaveOccurred())
		actor := contract.Actor{UserID: "user", CredentialID: "key", Mode: "named-user"}
		active := true
		v := &ForwardVerifier{OwnerID: "owner", Keys: map[string]ed25519.PublicKey{"front": pub}, Revalidate: func(context.Context, contract.Actor) error {
			if !active {
				return ErrForbidden
			}
			return nil
		}}
		message := ForwardedRequest{PeerID: "front", Audience: "owner", Actor: actor, OperationID: "op", Nonce: "unique", ExpiresAt: time.Now().Add(time.Minute), Body: []byte(`{"operationId":"op"}`)}
		message.Sign(priv)
		peer := PeerIdentity{ID: "front", Verified: true}
		bad := message
		bad.Audience = "other"
		bad.Sign(priv)
		_, e = v.Verify(context.Background(), peer, bad)
		Expect(e).To(HaveOccurred())
		bad = message
		bad.Body = []byte(`{}`)
		_, e = v.Verify(context.Background(), peer, bad)
		Expect(e).To(HaveOccurred())
		bad = message
		bad.ExpiresAt = time.Now().Add(-time.Second)
		bad.Sign(priv)
		_, e = v.Verify(context.Background(), peer, bad)
		Expect(e).To(HaveOccurred())
		_, e = v.Verify(context.Background(), PeerIdentity{ID: "front"}, message)
		Expect(e).To(HaveOccurred())
		_, e = v.Verify(context.Background(), PeerIdentity{ID: "other", Verified: true}, message)
		Expect(e).To(HaveOccurred())
		got, e := v.Verify(context.Background(), peer, message)
		Expect(e).NotTo(HaveOccurred())
		Expect(got).To(Equal(actor))
		_, e = v.Verify(context.Background(), peer, message)
		Expect(e).To(HaveOccurred())
		message.Nonce = "revoked"
		message.Sign(priv)
		active = false
		_, e = v.Verify(context.Background(), peer, message)
		Expect(e).To(HaveOccurred())
	})
})

var _ = Describe("owner dispatch", func() {
	It("fails closed without current identity and resource policy and preserves unknown mutation IDs", func() {
		a := contract.Actor{UserID: "user", CredentialID: "key", Mode: "named-user"}
		r := contract.Request{AddonID: "sample", OperationID: "op", Action: "apply", Generation: 1, Arguments: []byte(`{}`)}
		g := &Gateway{}
		_, e := g.Dispatch(context.Background(), a, r)
		Expect(e).To(HaveOccurred())
		g.Revalidate = func(context.Context, contract.Actor) error { return nil }
		_, e = g.Dispatch(context.Background(), a, r)
		Expect(e).To(Equal(ErrForbidden))
		r.Action = "read"
		_, e = g.Dispatch(context.Background(), a, r)
		Expect(e).To(Equal(ErrForbidden))
	})
})

var _ = Describe("typed lifecycle ingress", func() {
	It("rejects unknown lifecycle argument fields before any owner dispatch", func() {
		g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return nil }}
		_, e := g.Dispatch(context.Background(), contract.Actor{}, contract.Request{Action: "install", Arguments: []byte(`{"sourceId":"test","version":"1.0.0","actor":"admin"}`)})
		Expect(e).To(Equal(ErrForbidden))
	})
})
