// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// featureAccounts is the host's account check of the kinds that are not
// add-ons: it records the feature it was asked for and refuses the features
// a user does not hold.
type featureAccounts struct {
	mu     sync.Mutex
	asked  map[string]bool
	denied map[string]string
	down   bool
}

func (a *featureAccounts) check(_ context.Context, actor contract.Actor, feature string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked[feature] = true
	if a.down {
		return fmt.Errorf("account store down")
	}
	if feature != "" && a.denied[actor.UserID] == feature {
		return fmt.Errorf("feature %s off: %w", feature, gateway.ErrForbidden)
	}
	return nil
}

var _ = Describe("Kinds that are not add-ons and the add-ons feature", func() {
	bg := context.Background()
	addonsOff := func(context.Context, contract.Actor) error {
		return fmt.Errorf("addons feature off: %w", gateway.ErrForbidden)
	}
	spec := func(feature string) KindSpec {
		return KindSpec{
			Grantable:       notesGrantable,
			Roles:           notesRoles,
			Eligible:        notesEligible,
			Feature:         feature,
			CreateAuthority: func(context.Context, contract.Actor) error { return nil },
			Backing:         func(context.Context, Ref) (bool, error) { return true, nil },
		}
	}
	withAccounts := func(f *fixture, acc *featureAccounts) {
		f.opts.Revalidate = addonsOff
		f.opts.Account = nil
		if acc != nil {
			f.opts.Account = acc.check
		}
		f.opts.DrainTimeout = 2 * time.Second
		f.policy = New(f.store, f.opts)
	}

	It("checks only the account for a kind that names no feature while the add-ons check refuses everyone", func() {
		f := newFixture(ModeNamedUser)
		acc := &featureAccounts{asked: map[string]bool{}, denied: map[string]string{}}
		withAccounts(f, acc)
		Expect(f.policy.RegisterKind(notesKind, spec(""))).To(Succeed())
		owner, bob := named("owner"), named("bob")

		ref, err := f.policy.CreateRef(bg, owner, notesKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleEditor)).To(Succeed())
		Expect(f.policy.AuthorizeRef(bg, bob, ref, ActionWrite)).To(Succeed())
		grants, err := f.policy.GrantsRef(bg, owner, ref)
		Expect(err).NotTo(HaveOccurred())
		Expect(grants).To(Equal(map[string]Role{"bob": RoleEditor}))
		views, err := f.policy.ListingKind(bg, bob, notesKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(views).To(HaveLen(1))
		Expect(acc.asked).To(Equal(map[string]bool{"": true}))

		// The add-on resources keep the add-ons check.
		_, err = f.policy.Register(bg, owner, "fixture")
		Expect(err).To(MatchError(ErrForbidden))

		// A revocation still stops the grantee's admitted work before it
		// returns, with the add-ons check refusing.
		entered, cancelled, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			done <- f.policy.AdmitRef(bg, bob, ref, ActionWrite, func(ctx context.Context) error {
				close(entered)
				<-ctx.Done()
				close(cancelled)
				return ctx.Err()
			})
		}()
		Eventually(entered).Should(BeClosed())
		Expect(f.policy.RevokeRef(bg, owner, ref, "bob")).To(Succeed())
		Expect(cancelled).To(BeClosed())
		Eventually(done).Should(Receive())
		Expect(f.policy.AuthorizeRef(bg, bob, ref, ActionRead)).To(MatchError(ErrForbidden))
	})

	It("asks the account check for the feature a kind names, for that kind only", func() {
		f := newFixture(ModeNamedUser)
		acc := &featureAccounts{asked: map[string]bool{}, denied: map[string]string{"bob": "notes"}}
		withAccounts(f, acc)
		Expect(f.policy.RegisterKind(notesKind, spec(""))).To(Succeed())
		Expect(f.policy.RegisterKind("notes:secret", spec("notes"))).To(Succeed())
		owner, bob := named("owner"), named("bob")
		open, err := f.policy.CreateRef(bg, owner, notesKind)
		Expect(err).NotTo(HaveOccurred())
		secret, err := f.policy.CreateRef(bg, owner, "notes:secret")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.SetGrantRef(bg, owner, open, "bob", RoleViewer)).To(Succeed())
		Expect(f.policy.SetGrantRef(bg, owner, secret, "bob", RoleViewer)).To(Succeed())

		Expect(f.policy.AuthorizeRef(bg, bob, open, ActionRead)).To(Succeed())
		Expect(f.policy.AuthorizeRef(bg, bob, secret, ActionRead)).To(MatchError(ErrForbidden))
		_, err = f.policy.CreateRef(bg, bob, "notes:secret")
		Expect(err).To(MatchError(ErrForbidden))
		_, err = f.policy.ListingKind(bg, bob, "notes:secret")
		Expect(err).To(MatchError(ErrForbidden))
		Expect(acc.asked).To(HaveKey("notes"))

		// An account store that cannot answer is unavailable, never a denial.
		acc.mu.Lock()
		acc.down = true
		acc.mu.Unlock()
		Expect(f.policy.AuthorizeRef(bg, owner, open, ActionRead)).To(MatchError(ErrUnavailable))
	})

	It("denies every kind that is not an add-on when the host gives no account check", func() {
		f := newFixture(ModeNamedUser)
		withAccounts(f, nil)
		f.opts.Revalidate = f.accounts.check
		f.policy = New(f.store, f.opts)
		Expect(f.policy.RegisterKind(notesKind, spec(""))).To(Succeed())
		_, err := f.policy.CreateRef(bg, named("owner"), notesKind)
		Expect(err).To(MatchError(ErrForbidden))
		_, err = f.policy.ListingKind(bg, named("owner"), notesKind)
		Expect(err).To(MatchError(ErrForbidden))
		// The add-on resources do not need it.
		f.create(named("owner"))
	})

	It("refuses a feature name that is not the shape of a permission", func() {
		f := newFixture(ModeNamedUser)
		for _, bad := range []string{"Notes", "notes feature", "-notes", "n" + fmt.Sprintf("%065d", 0)} {
			Expect(f.policy.RegisterKind(notesKind, spec(bad))).NotTo(Succeed(), bad)
		}
		Expect(f.policy.RegisterKind(notesKind, spec("mcp_jobs"))).To(Succeed())
	})
})
