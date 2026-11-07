package connectorstest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
)

func grant(bindingID string) connectors.Grant {
	// #nosec G101 -- test data: a made-up sealed blob and example addresses, not a credential
	return connectors.Grant{Binding: bindingID, Owner: "u1", AddonID: "providerA", Sealed: []byte("sealed-grant-" + bindingID),
		ExpiresAt: t0.Add(time.Hour), Scopes: []string{"read", "write"}, AccountLabel: "someone@example.test",
		State: connectors.GrantConnected, DescriptorDigest: "d1", TokenURL: "https://auth.example.test/token", RevokeURL: "https://auth.example.test/revoke", Updated: t0}
}

// bindFor stores a binding of u1 for a grant: a store keeps a grant only for
// a binding that exists.
func bindFor(s connectors.Store, id string) {
	ExpectWithOffset(1, s.PutBinding(context.Background(), binding(id, "u1", "Bot "+id))).To(Succeed())
}

// runOAuth registers the specs of the OAuth records: the binding's auth kind,
// the grant of a binding, the client of an add-on and the login state.
func runOAuth(newStore NewStore) {
	ctx := context.Background()

	It("keeps the auth kind of a binding, and a binding stored without one is a token binding", func() {
		s := newStore()
		b := binding("o1", "u1", "OAuth")
		b.AuthKind, b.Token = connectors.AuthOAuth2, nil
		Expect(s.PutBinding(ctx, b)).To(Succeed())
		got, err := s.GetBinding(ctx, "o1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.AuthKind).To(Equal(connectors.AuthOAuth2))
		Expect(got.Token).To(BeEmpty())
		Expect(s.PutBinding(ctx, binding("t1", "u1", "Token"))).To(Succeed())
		old, _ := s.GetBinding(ctx, "t1")
		Expect(old.AuthKind).To(BeEmpty())
		Expect(old.UsesOAuth()).To(BeFalse())
		Expect(old.Token).To(Equal([]byte("sealed-bytes-t1")), "an old binding still reads")
	})

	It("stores one grant per binding and bumps its version on every write", func() {
		s := newStore()
		bindFor(s, "b1")
		_, err := s.GetGrant(ctx, "b1")
		Expect(errors.Is(err, connectors.ErrNotFound)).To(BeTrue())
		v, err := s.PutGrant(ctx, grant("b1"))
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(BeEquivalentTo(1))
		got, err := s.GetGrant(ctx, "b1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Version).To(BeEquivalentTo(1))
		Expect(got.Sealed).To(Equal([]byte("sealed-grant-b1")))
		Expect(got.Scopes).To(Equal([]string{"read", "write"}))
		Expect(got.ExpiresAt.Equal(t0.Add(time.Hour))).To(BeTrue())
		Expect([]string{got.Owner, got.AddonID, got.State, got.AccountLabel, got.DescriptorDigest, got.TokenURL, got.RevokeURL}).
			To(Equal([]string{"u1", "providerA", connectors.GrantConnected, "someone@example.test", "d1", "https://auth.example.test/token", "https://auth.example.test/revoke"}))
		got.Scopes[0], got.Sealed[0] = "changed", 'X'
		again, _ := s.GetGrant(ctx, "b1")
		Expect(again.Scopes[0]).To(Equal("read"), "the store returned its own slice")
		Expect(again.Sealed[0]).To(Equal(byte('s')))
		v, err = s.PutGrant(ctx, grant("b1"))
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(BeEquivalentTo(2), "a new login replaces the grant with a new version")
	})

	It("writes a grant back only at the version it read", func() {
		s := newStore()
		bindFor(s, "b1")
		_, _ = s.PutGrant(ctx, grant("b1"))
		next := grant("b1")
		next.Sealed, next.ExpiresAt, next.Scopes = []byte("rotated"), t0.Add(2*time.Hour), []string{"read"}
		next.State, next.DescriptorDigest, next.RevokeURL = connectors.GrantNeedsApproval, "d2", ""
		written, err := s.UpdateGrant(ctx, next, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(written).To(BeEquivalentTo(2), "the new version")
		written, err = s.UpdateGrant(ctx, grant("b1"), 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(written).To(BeZero(), "a stale version wrote")
		got, _ := s.GetGrant(ctx, "b1")
		Expect(got.Version).To(BeEquivalentTo(2))
		Expect(got.Sealed).To(Equal([]byte("rotated")))
		Expect(got.Scopes).To(Equal([]string{"read"}))
		Expect([]string{got.State, got.DescriptorDigest, got.TokenURL, got.RevokeURL}).To(Equal([]string{connectors.GrantNeedsApproval, "d2", "https://auth.example.test/token", ""}))
		written, err = s.UpdateGrant(ctx, grant("missing"), 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(written).To(BeZero())
	})

	It("leases a grant to one refresher until the lease ends or the grant is written", func() {
		s := newStore()
		bindFor(s, "b1")
		_, _ = s.PutGrant(ctx, grant("b1"))
		ok, err := s.LeaseGrant(ctx, "b1", 1, "front-a", t0, 30*time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		ok, _ = s.LeaseGrant(ctx, "b1", 1, "front-b", t0.Add(10*time.Second), 30*time.Second)
		Expect(ok).To(BeFalse(), "a live lease was taken")
		got, _ := s.GetGrant(ctx, "b1")
		Expect(got.LeaseOwner).To(Equal("front-a"))
		Expect(got.LeaseUntil.Equal(t0.Add(30 * time.Second))).To(BeTrue())
		ok, _ = s.LeaseGrant(ctx, "b1", 2, "front-b", t0.Add(time.Hour), 30*time.Second)
		Expect(ok).To(BeFalse(), "a lease at another version")
		ok, _ = s.LeaseGrant(ctx, "b1", 1, "front-b", t0.Add(31*time.Second), 30*time.Second)
		Expect(ok).To(BeTrue(), "an ended lease is taken over")
		written, _ := s.UpdateGrant(ctx, grant("b1"), 1)
		Expect(written).To(BeEquivalentTo(2))
		got, _ = s.GetGrant(ctx, "b1")
		Expect(got.LeaseOwner).To(BeEmpty(), "a write ends the lease")
		ok, _ = s.LeaseGrant(ctx, "b1", 2, "front-a", t0.Add(32*time.Second), 30*time.Second)
		Expect(ok).To(BeTrue())
	})

	It("releases a lease only for its owner and version", func() {
		s := newStore()
		bindFor(s, "b1")
		_, _ = s.PutGrant(ctx, grant("b1"))
		ok, _ := s.LeaseGrant(ctx, "b1", 1, "front-a", t0, time.Minute)
		Expect(ok).To(BeTrue())
		Expect(s.ReleaseGrant(ctx, "b1", 1, "front-b")).To(Succeed())
		Expect(s.ReleaseGrant(ctx, "b1", 2, "front-a")).To(Succeed())
		ok, _ = s.LeaseGrant(ctx, "b1", 1, "front-b", t0.Add(time.Second), time.Minute)
		Expect(ok).To(BeFalse(), "another owner or version released the lease")
		Expect(s.ReleaseGrant(ctx, "b1", 1, "front-a")).To(Succeed())
		ok, _ = s.LeaseGrant(ctx, "b1", 1, "front-b", t0.Add(time.Second), time.Minute)
		Expect(ok).To(BeTrue(), "a released lease is free")
		got, _ := s.GetGrant(ctx, "b1")
		Expect(got.Version).To(BeEquivalentTo(1), "a lease does not change the version")
		Expect(s.ReleaseGrant(ctx, "missing", 1, "x")).To(Succeed())
	})

	It("lets only one of many racing refreshers lease a grant", func() {
		s := newStore()
		bindFor(s, "race")
		_, _ = s.PutGrant(ctx, grant("race"))
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				ok, err := s.LeaseGrant(ctx, "race", 1, "x", t0, time.Minute)
				Expect(err).NotTo(HaveOccurred())
				if ok {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		Expect(wins).To(Equal(1))
	})

	It("keeps a grant only for a binding that exists, so a login that finishes after a delete leaves nothing", func() {
		s := newStore()
		_, err := s.PutGrant(ctx, grant("gone"))
		Expect(errors.Is(err, connectors.ErrNotFound)).To(BeTrue(), "%v", err)
		_, err = s.GetGrant(ctx, "gone")
		Expect(errors.Is(err, connectors.ErrNotFound)).To(BeTrue())
		bindFor(s, "gone")
		_, err = s.PutGrant(ctx, grant("gone"))
		Expect(err).NotTo(HaveOccurred())
		Expect(s.DeleteBinding(ctx, "gone")).To(Succeed())
		_, err = s.PutGrant(ctx, grant("gone"))
		Expect(errors.Is(err, connectors.ErrNotFound)).To(BeTrue(), "%v", err)
		_, err = s.GetGrant(ctx, "gone")
		Expect(errors.Is(err, connectors.ErrNotFound)).To(BeTrue())
	})

	It("deletes a grant, alone or with its binding", func() {
		s := newStore()
		bindFor(s, "b2")
		_ = s.PutBinding(ctx, binding("b1", "u1", "Bot"))
		_, _ = s.PutGrant(ctx, grant("b1"))
		_, _ = s.PutGrant(ctx, grant("b2"))
		Expect(s.DeleteGrant(ctx, "b2")).To(Succeed())
		Expect(s.DeleteGrant(ctx, "b2")).To(Succeed(), "a second delete")
		_, err := s.GetGrant(ctx, "b2")
		Expect(errors.Is(err, connectors.ErrNotFound)).To(BeTrue())
		Expect(s.DeleteBinding(ctx, "b1")).To(Succeed())
		_, err = s.GetGrant(ctx, "b1")
		Expect(errors.Is(err, connectors.ErrNotFound)).To(BeTrue(), "the grant outlived its binding")
	})

	It("keeps the OAuth client of an add-on", func() {
		s := newStore()
		_, err := s.GetOAuthClient(ctx, "providerA")
		Expect(errors.Is(err, connectors.ErrNotFound)).To(BeTrue())
		c := connectors.OAuthClient{AddonID: "providerA", DescriptorDigest: "d1", ClientID: "client-1", Secret: []byte("sealed-secret"), ApprovedBy: "admin", ApprovedAt: t0}
		Expect(s.PutOAuthClient(ctx, c)).To(Succeed())
		got, err := s.GetOAuthClient(ctx, "providerA")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ClientID).To(Equal("client-1"))
		Expect(got.Secret).To(Equal([]byte("sealed-secret")))
		Expect(got.DescriptorDigest).To(Equal("d1"))
		Expect(got.ApprovedBy).To(Equal("admin"))
		Expect(got.ApprovedAt.Equal(t0)).To(BeTrue())
		c.DescriptorDigest, c.Secret = "d2", nil
		Expect(s.PutOAuthClient(ctx, c)).To(Succeed())
		got, _ = s.GetOAuthClient(ctx, "providerA")
		Expect(got.DescriptorDigest).To(Equal("d2"))
		Expect(got.Secret).To(BeEmpty())
		Expect(s.DeleteOAuthClient(ctx, "providerA")).To(Succeed())
		_, err = s.GetOAuthClient(ctx, "providerA")
		Expect(errors.Is(err, connectors.ErrNotFound)).To(BeTrue())
	})

	It("uses a login state once, before it expires, on any frontend", func() {
		s := newStore()
		st := connectors.LoginState{Hash: "h1", Binding: "b1", Owner: "u1", AddonID: "providerA", DescriptorDigest: "d1", Verifier: []byte("sealed-verifier"),
			RedirectURI: "https://localai.example.test/cb", Scopes: []string{"read"}, ReturnTo: "/app/x", NonceHash: "n1", Expires: t0.Add(connectors.LoginStateTTL)}
		Expect(s.PutLoginState(ctx, st, t0)).To(Succeed())
		_, ok, err := s.ConsumeLoginState(ctx, "wrong", t0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		got, ok, err := s.ConsumeLoginState(ctx, "h1", t0.Add(time.Minute))
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got.Binding).To(Equal("b1"))
		Expect(got.Verifier).To(Equal([]byte("sealed-verifier")))
		Expect(got.RedirectURI).To(Equal("https://localai.example.test/cb"))
		Expect(got.Scopes).To(Equal([]string{"read"}))
		Expect([]string{got.Owner, got.AddonID, got.DescriptorDigest, got.ReturnTo, got.NonceHash}).To(Equal([]string{"u1", "providerA", "d1", "/app/x", "n1"}))
		_, ok, _ = s.ConsumeLoginState(ctx, "h1", t0.Add(time.Minute))
		Expect(ok).To(BeFalse(), "a state works once")

		st.Hash = "h2"
		Expect(s.PutLoginState(ctx, st, t0)).To(Succeed())
		_, ok, _ = s.ConsumeLoginState(ctx, "h2", t0.Add(connectors.LoginStateTTL+time.Second))
		Expect(ok).To(BeFalse(), "an expired state is refused")
		_, ok, _ = s.ConsumeLoginState(ctx, "h2", t0)
		Expect(ok).To(BeFalse(), "an expired state is gone after its first use")
	})

	It("lets only one of many racing callbacks consume a login state", func() {
		s := newStore()
		Expect(s.PutLoginState(ctx, connectors.LoginState{Hash: "race", Binding: "b1", Owner: "u1", Expires: t0.Add(time.Minute)}, t0)).To(Succeed())
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, ok, err := s.ConsumeLoginState(ctx, "race", t0)
				Expect(err).NotTo(HaveOccurred())
				if ok {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		Expect(wins).To(Equal(1))
	})

	It("caps the logins that wait for their callback, per binding and per owner, and an expired one frees its place", func() {
		s := newStore()
		state := func(hash, bindingID, owner string, expires time.Time) connectors.LoginState {
			return connectors.LoginState{Hash: hash, Binding: bindingID, Owner: owner, AddonID: "providerA", Expires: expires}
		}
		live := t0.Add(connectors.LoginStateTTL)
		for i := range connectors.MaxLoginsPerBinding {
			Expect(s.PutLoginState(ctx, state(fmt.Sprintf("b1-%d", i), "b1", "u1", live), t0)).To(Succeed())
		}
		err := s.PutLoginState(ctx, state("b1-over", "b1", "u1", live), t0)
		Expect(errors.Is(err, connectors.ErrTooManyLogins)).To(BeTrue(), "%v", err)
		_, ok, _ := s.ConsumeLoginState(ctx, "b1-over", t0)
		Expect(ok).To(BeFalse(), "a refused login is not stored")
		Expect(s.PutLoginState(ctx, state("other-owner", "b1x", "u2", live), t0)).To(Succeed(), "another owner has its own count")

		// The owner's other bindings fill the owner's cap.
		for i := connectors.MaxLoginsPerBinding; i < connectors.MaxLoginsPerOwner; i++ {
			Expect(s.PutLoginState(ctx, state(fmt.Sprintf("o-%d", i), fmt.Sprintf("b%d", 100+i), "u1", live), t0)).To(Succeed())
		}
		err = s.PutLoginState(ctx, state("o-over", "b999", "u1", live), t0)
		Expect(errors.Is(err, connectors.ErrTooManyLogins)).To(BeTrue(), "%v", err)

		// A used login frees its place at once, and an expired one when the
		// next login is stored.
		_, ok, _ = s.ConsumeLoginState(ctx, "o-6", t0)
		Expect(ok).To(BeTrue())
		Expect(s.PutLoginState(ctx, state("o-again", "b999", "u1", live), t0)).To(Succeed())
		later := t0.Add(connectors.LoginStateTTL + time.Second)
		Expect(s.PutLoginState(ctx, state("b1-later", "b1", "u1", later.Add(connectors.LoginStateTTL)), later)).To(Succeed())
		_, ok, _ = s.ConsumeLoginState(ctx, "b1-later", later)
		Expect(ok).To(BeTrue())
	})

	It("drops expired login states when a new one is stored, and the states of a deleted binding", func() {
		s := newStore()
		Expect(s.PutLoginState(ctx, connectors.LoginState{Hash: "old", Binding: "b1", Owner: "u1", Expires: t0.Add(time.Minute)}, t0)).To(Succeed())
		Expect(s.PutLoginState(ctx, connectors.LoginState{Hash: "new", Binding: "b2", Owner: "u1", Expires: t0.Add(time.Hour)}, t0.Add(2*time.Minute))).To(Succeed())
		Expect(s.PutLoginState(ctx, connectors.LoginState{Hash: "b2s", Binding: "b2", Owner: "u1", Expires: t0.Add(time.Hour)}, t0)).To(Succeed())
		Expect(s.DeleteBinding(ctx, "b2")).To(Succeed())
		for _, h := range []string{"old", "new", "b2s"} {
			_, ok, err := s.ConsumeLoginState(ctx, h, t0)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse(), h)
		}
	})
}
