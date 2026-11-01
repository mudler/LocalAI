// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The chat binding of a forwarded request", func() {
	actor := contract.Actor{UserID: "user", CredentialID: "key", Mode: "named-user"}
	verifier := func() (*ForwardVerifier, ed25519.PrivateKey) {
		pub, priv, e := ed25519.GenerateKey(rand.Reader)
		Expect(e).NotTo(HaveOccurred())
		return &ForwardVerifier{OwnerID: "owner", Keys: map[string]ed25519.PublicKey{"front": pub}, Revalidate: func(context.Context, contract.Actor) error { return nil }}, priv
	}
	message := func(nonce string, chat *hostops.Binding) ForwardedRequest {
		return ForwardedRequest{PeerID: "front", Audience: "owner", Actor: actor, OperationID: "op", Nonce: nonce, ExpiresAt: time.Now().Add(time.Minute), Body: []byte(`{"operationId":"op"}`), Chat: chat}
	}
	peer := PeerIdentity{ID: "front", Verified: true}

	It("carries the chat binding signed and rejects a changed one", func() {
		v, priv := verifier()
		chat := &hostops.Binding{Principal: "user", ChatID: "c1"}
		m := message("n1", chat)
		m.Sign(priv)
		_, e := v.Verify(context.Background(), peer, m)
		Expect(e).NotTo(HaveOccurred())
		b, ok := m.Binding()
		Expect(ok).To(BeTrue())
		Expect(b).To(Equal(*chat))

		changed := message("n2", chat)
		changed.Sign(priv)
		changed.Chat = &hostops.Binding{Principal: "user", ChatID: "c2"}
		_, e = v.Verify(context.Background(), peer, changed)
		Expect(e).To(MatchError(ErrForbidden), "another chat than the signed one")

		dropped := message("n3", chat)
		dropped.Sign(priv)
		dropped.Chat = nil
		_, e = v.Verify(context.Background(), peer, dropped)
		Expect(e).To(MatchError(ErrForbidden), "a signed binding cannot be removed")

		added := message("n4", nil)
		added.Sign(priv)
		added.Chat = chat
		_, e = v.Verify(context.Background(), peer, added)
		Expect(e).To(MatchError(ErrForbidden), "a binding cannot be added to a signed request")

		incomplete := message("n5", &hostops.Binding{Principal: "user"})
		incomplete.Sign(priv)
		_, e = v.Verify(context.Background(), peer, incomplete)
		Expect(e).To(MatchError(ErrForbidden), "a signed but incomplete binding")
	})

	It("signs a request without a binding as before", func() {
		v, priv := verifier()
		m := message("n1", nil)
		m.Sign(priv)
		Expect(string(m.signingBytes())).NotTo(ContainSubstring(`"chat"`))
		_, e := v.Verify(context.Background(), peer, m)
		Expect(e).NotTo(HaveOccurred())
		_, ok := m.Binding()
		Expect(ok).To(BeFalse())
	})

	It("gives the owner the chat the frontend bound the call to", func() {
		var mu sync.Mutex
		var seen []hostops.Binding
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(&Gateway{Revalidate: func(ctx context.Context, _ contract.Actor) error {
				b, _ := hostops.BindingOf(ctx)
				mu.Lock()
				seen = append(seen, b)
				mu.Unlock()
				return nil
			}})
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		a := contract.Actor{UserID: "u", CredentialID: "key:k", Mode: "named-user"}
		ctx := hostops.WithBinding(context.Background(), hostops.Binding{Principal: "u", ChatID: "c1"})
		_, _ = cs["front-a"].Dispatch(ctx, a, contract.Request{OperationID: "op1", Action: "operation-status"})
		mu.Lock()
		Expect(seen).To(ContainElement(hostops.Binding{Principal: "u", ChatID: "c1"}), "the owner serves the call for the chat")
		seen = nil
		mu.Unlock()
		_, _ = cs["front-a"].Dispatch(context.Background(), a, contract.Request{OperationID: "op2", Action: "operation-status"})
		mu.Lock()
		defer mu.Unlock()
		Expect(seen).NotTo(BeEmpty())
		Expect(seen).To(HaveEach(hostops.Binding{}), "a call that no chat made has no binding at the owner")
	})
})
