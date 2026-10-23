// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestGateway(t *testing.T) {
	// The owner pins every component of its root and refuses a symbolic link on
	// the path. A temporary directory on macOS lies behind one, so the specs use
	// its resolved path.
	if dir, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		t.Setenv("TMPDIR", dir)
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "Addon gateway")
}

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
	It("fails closed without current identity or resource policy and reports the original ID of an unknown mutation outcome", func() {
		a := contract.Actor{UserID: "user", CredentialID: "key", Mode: "named-user"}
		var forwarded atomic.Int32
		lost := func(*Carrier) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/addons/audience" {
					_, _ = w.Write([]byte(`{"audience":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
					return
				}
				forwarded.Add(1)
				conn, _, e := w.(http.Hijacker).Hijack()
				Expect(e).NotTo(HaveOccurred())
				_ = conn.Close()
			})
		}
		env := carrierFixtureWith(RelayLimits{}, lost)
		ok := func(context.Context, contract.Actor) error { return nil }
		install := contract.Request{AddonID: "sample", OperationID: "original-op", Action: "install", Generation: 1, Arguments: []byte(`{"sourceId":"s","version":"1.0.0"}`)}

		// Without a current identity check nothing leaves the frontend.
		frontend := &Gateway{Forward: env.carriers["front-a"]}
		_, e := frontend.Dispatch(context.Background(), a, install)
		Expect(e).To(MatchError(ErrForbidden))
		Expect(forwarded.Load()).To(BeZero())

		// A lost reply to a mutation keeps the original operation for the caller.
		frontend.Revalidate = ok
		_, e = frontend.Dispatch(context.Background(), a, install)
		var unknown *OutcomeUnknown
		Expect(errors.As(e, &unknown)).To(BeTrue())
		Expect(unknown.OperationID).To(Equal("original-op"))
		Expect(forwarded.Load()).To(Equal(int32(1)))

		// A lost reply to a read is plain unavailability.
		read := install
		read.Action = "operation-status"
		_, e = frontend.Dispatch(context.Background(), a, read)
		Expect(e).To(MatchError(ErrUnavailable))
		var typed *OutcomeUnknown
		Expect(errors.As(e, &typed)).To(BeFalse())

		// The owner side denies unknown actions and unconfigured resource reads.
		owner := &Gateway{Revalidate: ok}
		for _, action := range []string{"apply", "read"} {
			request := install
			request.Action = action
			_, e = owner.Dispatch(context.Background(), a, request)
			Expect(e).To(MatchError(ErrForbidden), action)
		}
	})
})

var _ = Describe("typed lifecycle ingress", func() {
	It("rejects unknown lifecycle argument fields before touching the owner journal", func() {
		actor := contract.Actor{UserID: "admin", CredentialID: "key", Mode: "named-user"}
		m := ownerManager(actor, false)
		g := &Gateway{Manager: m, Revalidate: func(context.Context, contract.Actor) error { return nil }}
		bad := contract.Request{AddonID: "sample", OperationID: "bad", Action: "install", Arguments: []byte(`{"sourceId":"test","version":"1.2.3","actor":"admin"}`)}
		_, e := g.Dispatch(context.Background(), actor, bad)
		Expect(e).To(MatchError(ErrForbidden))
		_, e = m.OperationStatus(context.Background(), actor, "bad")
		Expect(e).To(MatchError(addons.ErrOperationNotFound))

		// The same request without the extra field is accepted into the journal.
		good := bad
		good.OperationID = "good"
		good.Arguments = []byte(`{"sourceId":"test","version":"1.2.3"}`)
		out, e := g.Dispatch(context.Background(), actor, good)
		Expect(e).NotTo(HaveOccurred())
		var status addons.OperationStatus
		Expect(json.Unmarshal(out.Payload, &status)).To(Succeed())
		Expect(status.OperationID).To(Equal("good"))
		Expect(status.Phase).To(Equal("completed"))
		recorded, e := m.OperationStatus(context.Background(), actor, "good")
		Expect(e).NotTo(HaveOccurred())
		Expect(recorded.Phase).To(Equal("completed"))
	})
})

var _ = Describe("unclassified host account check failures", func() {
	It("reports an unclassified account check error as unavailable on every path and a wrapped denial as forbidden", func() {
		read := contract.Request{AddonID: "sample", OperationID: "op", Action: "operation-status"}
		actor := contract.Actor{UserID: "user", CredentialID: "key", Mode: "named-user"}
		plain := errors.New("backend answered something unexpected")
		denial := fmt.Errorf("account paused: %w", ErrForbidden)
		for _, row := range []struct {
			name string
			err  error
			want int
		}{{"plain", plain, 503}, {"cancelled", context.Canceled, 503}, {"denial", denial, 403}} {
			g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return row.err }}
			_, e := g.Dispatch(context.Background(), actor, read)
			Expect(StatusFor(e)).To(Equal(row.want), row.name+" dispatch")
			e = g.Relay(context.Background(), actor, RelayRequest{Request: read, Kind: "status"}, httptest.NewRecorder())
			Expect(StatusFor(e)).To(Equal(row.want), row.name+" relay")
			pub, priv, ke := ed25519.GenerateKey(rand.Reader)
			Expect(ke).NotTo(HaveOccurred())
			v := &ForwardVerifier{OwnerID: "owner", Keys: map[string]ed25519.PublicKey{"front": pub}, Revalidate: g.Revalidate}
			m := ForwardedRequest{PeerID: "front", Audience: "owner", Actor: actor, OperationID: "op", Nonce: "n", ExpiresAt: time.Now().Add(time.Minute), Body: []byte(`{"operationId":"op"}`)}
			m.Sign(priv)
			_, e = v.Verify(context.Background(), PeerIdentity{ID: "front", Verified: true}, m)
			Expect(StatusFor(e)).To(Equal(row.want), row.name+" forwarded")
		}
	})
})

var _ = Describe("host account check failures", func() {
	It("reports an account store outage as unavailable and a refused account as forbidden", func() {
		outage := fmt.Errorf("account store: %w", ErrUnavailable)
		refused := fmt.Errorf("account disabled: %w", ErrForbidden)
		read := contract.Request{AddonID: "sample", OperationID: "op", Action: "operation-status"}
		actor := contract.Actor{UserID: "user", CredentialID: "key", Mode: "named-user"}
		for check, want := range map[error]int{outage: 503, refused: 403} {
			g := &Gateway{Revalidate: func(context.Context, contract.Actor) error { return check }}
			_, e := g.Dispatch(context.Background(), actor, read)
			Expect(StatusFor(e)).To(Equal(want), "dispatch")
			e = g.Relay(context.Background(), actor, RelayRequest{Request: read, Kind: "status"}, httptest.NewRecorder())
			Expect(StatusFor(e)).To(Equal(want), "relay")
			pub, priv, ke := ed25519.GenerateKey(rand.Reader)
			Expect(ke).NotTo(HaveOccurred())
			v := &ForwardVerifier{OwnerID: "owner", Keys: map[string]ed25519.PublicKey{"front": pub}, Revalidate: g.Revalidate}
			m := ForwardedRequest{PeerID: "front", Audience: "owner", Actor: actor, OperationID: "op", Nonce: "n", ExpiresAt: time.Now().Add(time.Minute), Body: []byte(`{"operationId":"op"}`)}
			m.Sign(priv)
			_, e = v.Verify(context.Background(), PeerIdentity{ID: "front", Verified: true}, m)
			Expect(StatusFor(e)).To(Equal(want), "forwarded")
		}
	})
})
