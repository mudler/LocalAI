// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// oauthRecorder is an owner OAuth service that records the calls it got.
type oauthRecorder struct {
	mu     sync.Mutex
	calls  []OAuthCall
	actors []contract.Actor
	reply  OAuthReply
	err    error
}

func (o *oauthRecorder) ServeOAuth(_ context.Context, a contract.Actor, call OAuthCall) (OAuthReply, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, call)
	o.actors = append(o.actors, a)
	return o.reply, o.err
}

var _ = Describe("OAuth calls in a split deployment", func() {
	ok := func(context.Context, contract.Actor) error { return nil }
	actor := contract.Actor{UserID: "alice", CredentialID: "session:s1", Mode: "named-user"}

	split := func(owner OAuthService) (a, b *Gateway) {
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(&Gateway{OAuthOwner: owner, Revalidate: ok})
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		return &Gateway{Forward: cs["front-a"], Revalidate: ok}, &Gateway{Forward: cs["front-b"], Revalidate: ok}
	}

	It("forwards the callback with its typed fields and the signed actor to the owner", func() {
		rec := &oauthRecorder{reply: OAuthReply{Outcome: "ok", ReturnTo: "/app/chats/connectors", BindingID: "b1"}}
		_, b := split(rec)
		got, err := b.OAuth(context.Background(), actor, OAuthCall{Op: OAuthComplete, Nonce: "nonce-1", State: "state-1", Code: "code-1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(rec.reply))
		Expect(rec.calls).To(HaveLen(1))
		Expect(rec.calls[0]).To(Equal(OAuthCall{Op: OAuthComplete, Nonce: "nonce-1", State: "state-1", Code: "code-1"}))
		Expect(rec.actors[0]).To(Equal(actor))
	})

	It("carries a client secret to the owner and returns its typed refusal unchanged", func() {
		secret := "secret-sentinel"
		rec := &oauthRecorder{err: &OAuthError{Status: 409, Type: OAuthApprovalStale, Message: "review it again", CurrentDigest: "d2"}}
		a, _ := split(rec)
		_, err := a.OAuth(context.Background(), actor, OAuthCall{Op: OAuthClientSet, AddonID: "providerA", ClientID: "c1", ClientSecret: &secret, Digest: "d1"})
		var oe *OAuthError
		Expect(errors.As(err, &oe)).To(BeTrue())
		Expect(*oe).To(Equal(OAuthError{Status: 409, Type: OAuthApprovalStale, Message: "review it again", CurrentDigest: "d2"}))
		Expect(*rec.calls[0].ClientSecret).To(Equal(secret))
	})

	It("answers unavailable when the owner has no OAuth service, and never from the frontend's own", func() {
		local := &oauthRecorder{reply: OAuthReply{Outcome: "ok"}}
		a, _ := split(nil)
		a.OAuthOwner = local
		_, err := a.OAuth(context.Background(), actor, OAuthCall{Op: OAuthStart, BindingID: "b1"})
		Expect(errors.Is(err, ErrUnavailable)).To(BeTrue())
		var ue *UnavailableError
		Expect(errors.As(err, &ue)).To(BeTrue())
		Expect(ue.Reason).To(Equal(reasonOAuthUnsupported))
		Expect(local.calls).To(BeEmpty())
	})

	It("serves the call on the owner itself when nothing forwards", func() {
		rec := &oauthRecorder{reply: OAuthReply{Revoked: true}}
		g := &Gateway{OAuthOwner: rec, Revalidate: ok}
		got, err := g.OAuth(context.Background(), actor, OAuthCall{Op: OAuthDisconnect, BindingID: "b1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Revoked).To(BeTrue())
		_, err = (&Gateway{Revalidate: ok}).OAuth(context.Background(), actor, OAuthCall{Op: OAuthDisconnect, BindingID: "b1"})
		Expect(errors.Is(err, ErrUnavailable)).To(BeTrue())
	})

	It("refuses an actor the host does not accept before anything is sent", func() {
		rec := &oauthRecorder{}
		g := &Gateway{OAuthOwner: rec, Revalidate: func(context.Context, contract.Actor) error { return ErrForbidden }}
		_, err := g.OAuth(context.Background(), actor, OAuthCall{Op: OAuthStart, BindingID: "b1"})
		Expect(errors.Is(err, ErrForbidden)).To(BeTrue())
		Expect(rec.calls).To(BeEmpty())
	})

	DescribeTable("refuses a call that is not well formed",
		func(call OAuthCall) {
			rec := &oauthRecorder{}
			g := &Gateway{OAuthOwner: rec, Revalidate: ok}
			_, err := g.OAuth(context.Background(), actor, call)
			Expect(errors.Is(err, ErrForbidden)).To(BeTrue())
			Expect(rec.calls).To(BeEmpty())
		},
		Entry("an unknown op", OAuthCall{Op: "refresh", BindingID: "b1"}),
		Entry("a start with no binding", OAuthCall{Op: OAuthStart}),
		Entry("a client call with no add-on", OAuthCall{Op: OAuthClientGet}),
		Entry("a field that is too long", OAuthCall{Op: OAuthComplete, State: string(make([]byte, maxOAuthField+1))}),
		Entry("too many scopes", OAuthCall{Op: OAuthStart, BindingID: "b1", Scopes: make([]string, maxOAuthScopes+1)}),
	)

	It("prints and logs no secret of a call", func() {
		secret := "secret-sentinel"
		call := OAuthCall{Op: OAuthClientSet, AddonID: "providerA", ClientSecret: &secret, Nonce: "nonce-sentinel", State: "state-sentinel", Code: "code-sentinel", ProviderError: "access_denied"}
		var buf bytes.Buffer
		slog.New(slog.NewTextHandler(&buf, nil)).Info("call", "call", call)
		for _, text := range []string{fmt.Sprint(call), fmt.Sprintf("%+v", call), fmt.Sprintf("%#v", call), buf.String()} {
			for _, s := range []string{secret, "nonce-sentinel", "state-sentinel", "code-sentinel"} {
				Expect(text).NotTo(ContainSubstring(s))
			}
		}
		raw, err := json.Marshal(call)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(secret), "the wire carries the fields; only the text forms hide them")
	})

	It("keeps a lost answer to a write an unknown outcome and a lost read unavailable", func() {
		_, cs := carrierFixture(func(c *Carrier) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/addons/audience":
					_, _ = w.Write([]byte(`{"audience":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
				case "/addons/capabilities":
					_ = json.NewEncoder(w).Encode(Capabilities{Protocol: 2, Calls: []string{callOAuth}})
				default:
					_, _ = w.Write([]byte(`not json`))
				}
			})
		})
		g := &Gateway{Forward: cs["front-a"], Revalidate: ok}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := g.OAuth(ctx, actor, OAuthCall{Op: OAuthDisconnect, BindingID: "b1"})
		var unknown *OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue())
		_, err = g.OAuth(ctx, actor, OAuthCall{Op: OAuthClientGet, AddonID: "providerA"})
		Expect(err).To(Equal(ErrUnavailable))
	})
})
