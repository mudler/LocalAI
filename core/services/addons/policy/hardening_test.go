// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// chatKind is a kind shaped like a saved agent: use runs it (output), viewer
// also reads the definition, and everyone may hold a grant.
const chatKind = "chat"

func chatRoles(a Action) []Role {
	switch a {
	case ActionOutput:
		return []Role{RoleUse, RoleViewer}
	case ActionRead:
		return []Role{RoleViewer}
	}
	return nil
}

func chatSpec(backing func(context.Context, Ref) (bool, error)) KindSpec {
	if backing == nil {
		backing = func(context.Context, Ref) (bool, error) { return true, nil }
	}
	return KindSpec{
		Grantable:       []Role{RoleUse, RoleViewer},
		Roles:           chatRoles,
		Everyone:        true,
		Eligible:        anyone,
		CreateAuthority: func(context.Context, contract.Actor) error { return nil },
		Backing:         backing,
	}
}

// running is admitted work that runs until it is cancelled or released.
type running struct {
	entered  chan struct{}
	release  chan struct{}
	done     chan struct{}
	mu       sync.Mutex
	canceled bool
	err      error
}

func (w *running) wasCancelled() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.canceled }

func admitRunning(p *Policy, actor contract.Actor, ref Ref, action Action) *running {
	w := &running{entered: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		err := p.AdmitRef(context.Background(), actor, ref, action, func(c context.Context) error {
			close(w.entered)
			select {
			case <-w.release:
			case <-c.Done():
				w.mu.Lock()
				w.canceled = true
				w.mu.Unlock()
			}
			return nil
		})
		w.mu.Lock()
		w.err = err
		w.mu.Unlock()
	}()
	Eventually(w.entered, 5*time.Second).Should(BeClosed())
	return w
}

func (w *running) finish() {
	select {
	case <-w.release:
	default:
		close(w.release)
	}
	Eventually(w.done, 5*time.Second).Should(BeClosed())
}

var _ = Describe("Quota of the records of shared kinds", func() {
	bg := context.Background()

	setup := func(opts func(*Options)) *fixture {
		f := newFixture(ModeNamedUser)
		if opts != nil {
			opts(&f.opts)
		}
		f.policy = New(f.store, f.opts)
		Expect(f.policy.RegisterKind(chatKind, chatSpec(nil))).To(Succeed())
		return f
	}

	It("never counts a record of a shared kind against the add-on resource limits", func() {
		f := setup(nil)
		owner := named("owner")
		// The reviewer's attack: create, share and delete in a loop. A share
		// record that is never dropped stays in the store.
		for i := 0; i < DefaultMaxResourcesPerUser; i++ {
			ref, err := f.policy.CreateRef(bg, owner, chatKind)
			Expect(err).NotTo(HaveOccurred(), "round %d", i)
			Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		}
		// The add-on resources of the same owner are not blocked.
		f.create(owner)
		// The per-owner cap of share records holds the owner back.
		_, err := f.policy.CreateRef(bg, owner, chatKind)
		Expect(err).To(MatchError(ErrQuota))
		_, err = f.policy.EnsureRef(bg, owner, chatKind, "name")
		Expect(err).To(MatchError(ErrQuota))
		// Another person still creates both.
		_, err = f.policy.CreateRef(bg, named("carol"), chatKind)
		Expect(err).NotTo(HaveOccurred())
		f.create(named("carol"))
	})

	It("does not let 16 accounts at their share cap block other people", func() {
		// The defaults keep 16 owners at their own cap far below the total.
		Expect(16 * DefaultMaxSharesPerUser).To(BeNumerically("<", DefaultMaxShares))
		Expect(DefaultMaxShares / DefaultMaxSharesPerUser).To(Equal(32))
		// The same ratio with small numbers, so the store stays small. The
		// add-on total is what the 16 owners create, so a share record that
		// counted against it would block every add-on resource.
		perOwner := DefaultMaxSharesPerUser / 16
		f := setup(func(o *Options) {
			o.MaxSharesPerUser, o.MaxShares = perOwner, DefaultMaxShares/16
			o.MaxResourcesPerUser, o.MaxResources = perOwner, 16*perOwner
		})
		for u := 0; u < 16; u++ {
			attacker := named(fmt.Sprintf("attacker-%02d", u))
			for i := 0; i < perOwner; i++ {
				_, err := f.policy.CreateRef(bg, attacker, chatKind)
				Expect(err).NotTo(HaveOccurred())
			}
			_, err := f.policy.CreateRef(bg, attacker, chatKind)
			Expect(err).To(MatchError(ErrQuota))
		}
		other := named("other")
		_, err := f.policy.CreateRef(bg, other, chatKind)
		Expect(err).NotTo(HaveOccurred())
		_, err = f.policy.EnsureRef(bg, other, chatKind, "mine")
		Expect(err).NotTo(HaveOccurred())
		f.create(other)
		_, err = f.policy.CreateWithID(bg, other, "fixture", "r"+fmt.Sprintf("%031d", 7), "ref-1", func(context.Context, ResourceKey) error { return nil })
		Expect(err).NotTo(HaveOccurred())
	})

	It("keeps the add-on resource limits for add-on resources only", func() {
		f := setup(func(o *Options) { o.MaxResourcesPerUser, o.MaxSharesPerUser = 2, 3 })
		owner := named("owner")
		f.create(owner)
		f.create(owner)
		_, err := f.policy.Register(bg, owner, "fixture")
		Expect(err).To(MatchError(ErrQuota))
		for i := 0; i < 3; i++ {
			_, err = f.policy.CreateRef(bg, owner, chatKind)
			Expect(err).NotTo(HaveOccurred())
		}
		_, err = f.policy.CreateRef(bg, owner, chatKind)
		Expect(err).To(MatchError(ErrQuota))
	})

	It("drops the record of a deleted resource so create, share and delete never fills the store", func() {
		f := setup(func(o *Options) { o.MaxSharesPerUser = 4 })
		owner := named("owner")
		for i := 0; i < 64; i++ {
			ref, err := f.policy.CreateRef(bg, owner, chatKind)
			Expect(err).NotTo(HaveOccurred(), "round %d", i)
			Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
			Expect(f.policy.ForgetRef(bg, owner, ref)).To(Succeed())
		}
		f.store.mu.Lock()
		n := len(f.store.doc.Resources)
		f.store.mu.Unlock()
		Expect(n).To(BeZero())
	})
})

var _ = Describe("Sweep of share records whose resource is gone", func() {
	bg := context.Background()

	It("drops an active record whose backing is gone, revokes its grants and keeps the rest", func() {
		f := newFixture(ModeNamedUser)
		var mu sync.Mutex
		gone := map[string]bool{}
		broken := map[string]bool{}
		backing := func(_ context.Context, r Ref) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			if broken[r.ResourceID] {
				return false, errors.New("store down")
			}
			return !gone[r.ResourceID], nil
		}
		Expect(f.policy.RegisterKind(chatKind, chatSpec(backing))).To(Succeed())
		owner := named("owner")
		orphan, err := f.policy.CreateRef(bg, owner, chatKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.SetGrantRef(bg, owner, orphan, "bob", RoleViewer)).To(Succeed())
		Expect(f.policy.SetGrantRef(bg, owner, orphan, Everyone, RoleUse)).To(Succeed())
		kept, err := f.policy.CreateRef(bg, owner, chatKind)
		Expect(err).NotTo(HaveOccurred())
		unsure, err := f.policy.CreateRef(bg, owner, chatKind)
		Expect(err).NotTo(HaveOccurred())
		addon := f.create(owner)
		mu.Lock()
		gone[orphan.ResourceID], broken[unsure.ResourceID] = true, true
		mu.Unlock()

		rep, err := f.policy.SweepOrphans(bg)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Dropped).To(Equal(1))
		Expect(rep.Kept).To(Equal(1))
		_, ok := f.store.get(orphan.ResourceID)
		Expect(ok).To(BeFalse())
		for _, id := range []string{kept.ResourceID, unsure.ResourceID, addon.ResourceID} {
			_, ok = f.store.get(id)
			Expect(ok).To(BeTrue(), id)
		}
		// The lowering is in the log of the kinds, not in the add-on one.
		f.store.mu.Lock()
		kinds := append([]revocation(nil), f.store.doc.RevokedKinds...)
		addons := len(f.store.doc.Revoked)
		f.store.mu.Unlock()
		Expect(addons).To(BeZero())
		Expect(kinds).To(HaveLen(2))
		// A second pass has nothing to do.
		rep, err = f.policy.SweepOrphans(bg)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Dropped).To(BeZero())
	})

	It("cancels the admitted work of a record it drops", func() {
		f := newFixture(ModeNamedUser)
		var gone sync.Map
		Expect(f.policy.RegisterKind(chatKind, chatSpec(func(_ context.Context, r Ref) (bool, error) {
			_, g := gone.Load(r.ResourceID)
			return !g, nil
		}))).To(Succeed())
		owner := named("owner")
		ref, err := f.policy.CreateRef(bg, owner, chatKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		w := admitRunning(f.policy, named("bob"), ref, ActionOutput)
		gone.Store(ref.ResourceID, true)
		rep, err := f.policy.SweepOrphans(bg)
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Dropped).To(Equal(1))
		Eventually(w.done, 5*time.Second).Should(BeClosed())
		Expect(w.wasCancelled()).To(BeTrue())
	})
})

var _ = Describe("Action-aware drain of a lowering", func() {
	bg := context.Background()
	owner, bob := named("owner"), named("bob")

	setup := func() (*fixture, Ref) {
		f := newFixture(ModeNamedUser)
		f.opts.DrainTimeout = 2 * time.Second
		f.policy = New(f.store, f.opts)
		Expect(f.policy.RegisterKind(chatKind, chatSpec(nil))).To(Succeed())
		ref, err := f.policy.CreateRef(bg, owner, chatKind)
		Expect(err).NotTo(HaveOccurred())
		return f, ref
	}

	It("keeps the running turn of a grantee lowered from viewer to use and stops the read", func() {
		f, ref := setup()
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleViewer)).To(Succeed())
		turn := admitRunning(f.policy, bob, ref, ActionOutput)
		read := admitRunning(f.policy, bob, ref, ActionRead)
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		Eventually(read.done, 5*time.Second).Should(BeClosed())
		Expect(read.wasCancelled()).To(BeTrue())
		Consistently(turn.done, 200*time.Millisecond).ShouldNot(BeClosed())
		turn.finish()
		Expect(turn.wasCancelled()).To(BeFalse())
	})

	It("stops the running turn of a grantee whose grant is revoked", func() {
		f, ref := setup()
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleViewer)).To(Succeed())
		turn := admitRunning(f.policy, bob, ref, ActionOutput)
		Expect(f.policy.RevokeRef(bg, owner, ref, "bob")).To(Succeed())
		Expect(turn.wasCancelled()).To(BeTrue())
	})

	It("keeps a turn under everyone lowered from viewer to use and stops it when everyone is revoked", func() {
		f, ref := setup()
		Expect(f.policy.SetGrantRef(bg, owner, ref, Everyone, RoleViewer)).To(Succeed())
		turn := admitRunning(f.policy, bob, ref, ActionOutput)
		read := admitRunning(f.policy, bob, ref, ActionRead)
		Expect(f.policy.SetGrantRef(bg, owner, ref, Everyone, RoleUse)).To(Succeed())
		Expect(read.wasCancelled()).To(BeTrue())
		Consistently(turn.done, 200*time.Millisecond).ShouldNot(BeClosed())
		Expect(f.policy.RevokeRef(bg, owner, ref, Everyone)).To(Succeed())
		Expect(turn.wasCancelled()).To(BeTrue())
	})

	It("keeps a turn that the own grant allows when everyone is lowered or revoked", func() {
		f, ref := setup()
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
		Expect(f.policy.SetGrantRef(bg, owner, ref, Everyone, RoleViewer)).To(Succeed())
		turn := admitRunning(f.policy, bob, ref, ActionOutput)
		read := admitRunning(f.policy, bob, ref, ActionRead)
		Expect(f.policy.RevokeRef(bg, owner, ref, Everyone)).To(Succeed())
		Expect(read.wasCancelled()).To(BeTrue())
		Consistently(turn.done, 200*time.Millisecond).ShouldNot(BeClosed())
		turn.finish()
		Expect(turn.wasCancelled()).To(BeFalse())
	})

	It("still stops work an owed revocation covers when a later lowering leaves the action", func() {
		f, ref := setup()
		f.opts.DrainTimeout = 50 * time.Millisecond
		f.policy.opts.DrainTimeout = 50 * time.Millisecond
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleViewer)).To(Succeed())
		// The turn ignores its cancellation, so the revocation stays owed.
		stuck := make(chan struct{})
		entered := make(chan struct{})
		ended := make(chan struct{})
		var cancelled sync.Once
		var sawCancel bool
		go func() {
			defer close(ended)
			_ = f.policy.AdmitRef(bg, bob, ref, ActionOutput, func(c context.Context) error {
				close(entered)
				<-c.Done()
				cancelled.Do(func() { sawCancel = true })
				<-stuck
				return nil
			})
		}()
		Eventually(entered).Should(BeClosed())
		Expect(f.policy.RevokeRef(bg, owner, ref, "bob")).To(MatchError(ErrDrainPending))
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleViewer)).To(MatchError(ErrDrainPending))
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(MatchError(ErrDrainPending))
		close(stuck)
		Eventually(ended).Should(BeClosed())
		Expect(sawCancel).To(BeTrue())
		Expect(f.policy.SetGrantRef(bg, owner, ref, "bob", RoleUse)).To(Succeed())
	})
})

var _ = Describe("Leases of a frontend per person", func() {
	bg := context.Background()

	It("refuses a person over their lease cap and still admits another person", func() {
		old := shareLeasesPerUser
		shareLeasesPerUser = 3
		DeferCleanup(func() { shareLeasesPerUser = old })
		Expect(MaxShareLeasesPerUser).To(BeNumerically("<", MaxShareLeases))
		c := newCluster(time.Second, 10*time.Second, 2*time.Second)
		owner := named("owner")
		ref, err := c.f.policy.CreateRef(bg, owner, boardKind)
		Expect(err).NotTo(HaveOccurred())
		Expect(c.f.policy.SetGrantRef(bg, owner, ref, Everyone, RoleUse)).To(Succeed())
		var leases []gateway.ShareLease
		for i := 0; i < 3; i++ {
			l, err := c.host.AdmitShare(bg, named("bob"), boardKind, ref.ResourceID, string(ActionOutput))
			Expect(err).NotTo(HaveOccurred())
			leases = append(leases, l)
		}
		_, err = c.host.AdmitShare(bg, named("bob"), boardKind, ref.ResourceID, string(ActionOutput))
		Expect(err).To(MatchError(ErrShareLeaseQuota))
		Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue())
		carol, err := c.host.AdmitShare(bg, named("carol"), boardKind, ref.ResourceID, string(ActionOutput))
		Expect(err).NotTo(HaveOccurred())
		// A released lease gives the room back.
		Expect(c.host.ReleaseShare(bg, named("bob"), leases[0].Lease)).To(Succeed())
		Eventually(func() error {
			l, err := c.host.AdmitShare(bg, named("bob"), boardKind, ref.ResourceID, string(ActionOutput))
			if err == nil {
				leases = append(leases, l)
			}
			return err
		}, 5*time.Second).Should(Succeed())
		for _, l := range append(leases[1:], carol) {
			_ = c.host.ReleaseShare(bg, named("bob"), l.Lease)
			_ = c.host.ReleaseShare(bg, named("carol"), l.Lease)
		}
		Eventually(c.host.leaseCount, 5*time.Second).Should(BeZero())
	})
})

var _ = Describe("Revocation logs per class of kind", func() {
	bg := context.Background()
	accountsAll := func(context.Context, string) error { return nil }

	It("never moves the floor of the add-on log for lowerings of other kinds", func() {
		old := revocationCap
		revocationCap = 8
		DeferCleanup(func() { revocationCap = old })
		f := newFixture(ModeNamedUser)
		Expect(f.policy.RegisterKind(chatKind, chatSpec(nil))).To(Succeed())
		owner := named("owner")
		key := f.create(owner)
		Expect(f.policy.SetGrant(bg, owner, key, "bob", RoleEditor)).To(Succeed())
		port := &RecoveryPort{Policy: f.policy}
		exp, err := port.Export(bg, "fixture")
		Expect(err).NotTo(HaveOccurred())
		since := time.Now().UTC().Add(-time.Hour)
		Expect(f.policy.Revoke(bg, owner, key, "bob")).To(Succeed())
		// Churn on a kind that is not an add-on: many distinct lowerings.
		ref, err := f.policy.CreateRef(bg, owner, chatKind)
		Expect(err).NotTo(HaveOccurred())
		for i := 0; i < 40; i++ {
			u := fmt.Sprintf("user-%02d", i)
			Expect(f.policy.SetGrantRef(bg, owner, ref, u, RoleViewer)).To(Succeed())
			Expect(f.policy.RevokeRef(bg, owner, ref, u)).To(Succeed())
		}
		Expect(f.store.revokedFloor().IsZero()).To(BeTrue())
		rep, err := port.Restore(bg, "fixture", exp, recovery.RestoreOptions{Since: since, Account: accountsAll})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.HistoryTruncated).To(BeFalse())
		got, _ := f.store.get(key.ResourceID)
		Expect(got.Grants).NotTo(HaveKey("bob"), "the lowering after the backup is applied again")
		f.store.mu.Lock()
		n := len(f.store.doc.RevokedKinds)
		floor := f.store.doc.RevokedKindsFloor
		f.store.mu.Unlock()
		Expect(n).To(Equal(8))
		Expect(floor.IsZero()).To(BeFalse())
	})

	It("moves the lowerings of other kinds out of a single log written before the split", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, fileName), []byte(singleLogFile), 0o600)).To(Succeed())
		s, err := OpenStore(dir, ModeNamedUser)
		Expect(err).NotTo(HaveOccurred())
		s.mu.Lock()
		addonLog := append([]revocation(nil), s.doc.Revoked...)
		kindLog := append([]revocation(nil), s.doc.RevokedKinds...)
		floor := s.doc.RevokedFloor
		s.mu.Unlock()
		Expect(addonLog).To(HaveLen(1))
		Expect(addonLog[0].Kind).To(Equal("addon:fixture"))
		Expect(kindLog).To(HaveLen(2))
		for _, v := range kindLog {
			Expect(v.Kind).To(Equal("collection"))
		}
		// The floor of the old log may come from any kind: it stays on the
		// add-on log, the safe side.
		Expect(floor).To(Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
		Expect(s.Close()).To(Succeed())
		raw, err := os.ReadFile(filepath.Join(dir, fileName))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"revokedKinds"`))
		again, err := OpenStore(dir, ModeNamedUser)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = again.Close() })
		again.mu.Lock()
		Expect(again.doc.Revoked).To(HaveLen(1))
		Expect(again.doc.RevokedKinds).To(HaveLen(2))
		again.mu.Unlock()
	})

	It("refuses a store whose log of the other kinds holds an add-on lowering", func() {
		dir := filepath.Join(GinkgoT().TempDir(), "policy")
		Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, fileName), []byte(misplacedKindLog), 0o600)).To(Succeed())
		_, err := OpenStore(dir, ModeNamedUser)
		Expect(err).To(HaveOccurred())
	})
})

const singleLogFile = `{
 "version": 1,
 "mode": "named-user",
 "resources": {
  "rold": {"kind": "addon:fixture", "addonId": "fixture", "owner": "owner", "state": "active", "grants": {"carol": "editor"}, "epoch": 1}
 },
 "revoked": [
  {"kind": "collection", "resourceId": "rc1", "userId": "bob", "at": "2026-02-01T00:00:00Z"},
  {"kind": "addon:fixture", "resourceId": "rold", "userId": "bob", "at": "2026-03-01T00:00:00Z"},
  {"kind": "collection", "resourceId": "rc2", "userId": "bob", "ceiling": "viewer", "at": "2026-03-02T00:00:00Z"}
 ],
 "revokedFloor": "2026-01-01T00:00:00Z"
}`

const misplacedKindLog = `{
 "version": 1,
 "mode": "named-user",
 "resources": {},
 "revokedKinds": [{"kind": "addon:fixture", "resourceId": "r1", "userId": "bob", "at": "2026-03-01T00:00:00Z"}]
}`
