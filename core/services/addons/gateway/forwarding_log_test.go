// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("forwarding denial log", func() {
	var logs *bytes.Buffer
	BeforeEach(func() {
		logs = &bytes.Buffer{}
		xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn}), xlog.LogLevelWarn))
		DeferCleanup(func() { xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text")) })
	})

	It("names the exact check that refuses a forwarded request and keeps the answer generic", func() {
		pub, priv, e := ed25519.GenerateKey(rand.Reader)
		Expect(e).NotTo(HaveOccurred())
		other, _, e := ed25519.GenerateKey(rand.Reader)
		Expect(e).NotTo(HaveOccurred())
		actor := contract.Actor{UserID: "user", CredentialID: "key", Mode: "named-user"}
		revalidate := func(context.Context, contract.Actor) error { return nil }
		v := &ForwardVerifier{OwnerID: "owner", Keys: map[string]ed25519.PublicKey{"front": pub, "wrongkey": other}, Revalidate: revalidate}
		base := ForwardedRequest{PeerID: "front", Audience: "owner", Actor: actor, OperationID: "op", Nonce: "n", ExpiresAt: time.Now().Add(30 * time.Second), Body: []byte(`{"secret":"body-marker"}`)}
		peer := PeerIdentity{ID: "front", Verified: true}
		signed := func(edit func(*ForwardedRequest)) ForwardedRequest {
			m := base
			edit(&m)
			m.Sign(priv)
			return m
		}
		cases := []struct {
			check string
			peer  PeerIdentity
			m     ForwardedRequest
		}{
			{"peer-unverified", PeerIdentity{ID: "front"}, signed(func(*ForwardedRequest) {})},
			{"peer-id-mismatch", PeerIdentity{ID: "wrongkey", Verified: true}, signed(func(*ForwardedRequest) {})},
			{"audience-mismatch", peer, signed(func(m *ForwardedRequest) { m.Audience = "elsewhere" })},
			{"owner-key-missing", PeerIdentity{ID: "unknown", Verified: true}, signed(func(m *ForwardedRequest) { m.PeerID = "unknown" })},
			{"body-too-large", peer, signed(func(m *ForwardedRequest) { m.Body = make([]byte, int(contract.DefaultLimits().EnvelopeBytes)+1) })},
			{"envelope-expired", peer, signed(func(m *ForwardedRequest) { m.ExpiresAt = time.Now().Add(-time.Second) })},
			{"signature-invalid", PeerIdentity{ID: "wrongkey", Verified: true}, signed(func(m *ForwardedRequest) { m.PeerID = "wrongkey" })},
			{"actor-invalid", peer, signed(func(m *ForwardedRequest) { m.Actor.UserID = "" })},
		}
		for _, c := range cases {
			logs.Reset()
			_, e := v.Verify(context.Background(), c.peer, c.m)
			ExpectWithOffset(1, e).To(MatchError(ErrForbidden), c.check)
			ExpectWithOffset(1, logs.String()).To(ContainSubstring("check="+c.check), c.check)
			ExpectWithOffset(1, logs.String()).NotTo(ContainSubstring("body-marker"))
		}

		// A replay is named, and the first use leaves no denial line.
		logs.Reset()
		m := signed(func(m *ForwardedRequest) { m.Nonce = "once" })
		_, e = v.Verify(context.Background(), peer, m)
		Expect(e).NotTo(HaveOccurred())
		Expect(logs.String()).To(BeEmpty())
		_, e = v.Verify(context.Background(), peer, m)
		Expect(e).To(MatchError(ErrForbidden))
		Expect(logs.String()).To(ContainSubstring("check=replayed-nonce"))
		Expect(logs.String()).To(ContainSubstring("peer=front"))
		Expect(logs.String()).NotTo(ContainSubstring(string(m.Signature)))

		// A revoked credential is named, with the cause of the revalidation.
		logs.Reset()
		v.Revalidate = func(context.Context, contract.Actor) error { return ErrForbidden }
		_, e = v.Verify(context.Background(), peer, signed(func(m *ForwardedRequest) { m.Nonce = "revoked" }))
		Expect(errors.Is(e, ErrForbidden)).To(BeTrue())
		Expect(logs.String()).To(ContainSubstring("check=revalidate-failed"))
	})
})
