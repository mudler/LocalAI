// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Addon recovery state of the policy", func() {
	bg := context.Background()

	It("holds the resources of one add-on and leaves another add-on alone", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		otherKey, err := f.policy.Register(bg, owner, "other")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.Commit(bg, owner, otherKey)).To(Succeed())
		release, err := f.policy.HoldAddon(bg, "fixture")
		Expect(err).NotTo(HaveOccurred())
		held := f.policy.Authorize(bg, owner, key, ActionRead)
		Expect(held).To(MatchError(gateway.ErrUnavailable))
		Expect(f.policy.Admit(bg, owner, key, ActionWrite, func(context.Context) error { return nil })).To(MatchError(gateway.ErrUnavailable))
		_, err = f.policy.Register(bg, owner, "fixture")
		Expect(err).To(MatchError(gateway.ErrUnavailable))
		Expect(f.policy.Authorize(bg, owner, otherKey, ActionRead)).To(Succeed())
		release()
		release()
		Expect(f.policy.Authorize(bg, owner, key, ActionRead)).To(Succeed())
	})

	It("cancels admitted work and reports a drain that did not finish", func() {
		f := newFixture(ModeNamedUser)
		f.opts.DrainTimeout = 100 * time.Millisecond
		f.policy = New(f.store, f.opts)
		owner := named("owner")
		key := f.create(owner)
		started, done := make(chan struct{}), make(chan error, 1)
		stop := make(chan struct{})
		go func() {
			done <- f.policy.Admit(bg, owner, key, ActionRead, func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			})
		}()
		<-started
		release, err := f.policy.HoldAddon(bg, "fixture")
		Expect(err).NotTo(HaveOccurred())
		Expect(<-done).To(HaveOccurred())
		release()
		// Work that ignores cancellation exceeds the bound.
		go func() {
			done <- f.policy.Admit(bg, owner, key, ActionRead, func(context.Context) error {
				stop <- struct{}{}
				<-stop
				return nil
			})
		}()
		<-stop
		_, err = f.policy.HoldAddon(bg, "fixture")
		Expect(err).To(MatchError(ErrDrainPending))
		Expect(f.policy.isHeld("fixture")).To(BeFalse())
		stop <- struct{}{}
		Expect(<-done).To(Succeed())
	})

	It("restores grants through the account directory and the lowerings since the backup, never beyond them", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		for u, role := range map[string]Role{"bob": RoleEditor, "carol": RoleEditor, "dave": RoleViewer, "erin": RoleEditor} {
			Expect(f.policy.SetGrant(bg, owner, key, u, role)).To(Succeed())
		}
		port := &RecoveryPort{Policy: f.policy}
		exp, err := port.Export(bg, "fixture")
		Expect(err).NotTo(HaveOccurred())
		since := time.Now().UTC().Add(-time.Millisecond)
		Expect(f.policy.Revoke(bg, owner, key, "bob")).To(Succeed())
		Expect(f.policy.SetGrant(bg, owner, key, "carol", RoleViewer)).To(Succeed())
		accounts := map[string]bool{"owner": true, "bob": true, "carol": true, "dave": true}
		rep, err := port.Restore(bg, "fixture", exp, recovery.RestoreOptions{
			Since: since,
			Account: func(_ context.Context, u string) error {
				if accounts[u] {
					return nil
				}
				return ErrForbidden
			},
		})
		Expect(err).NotTo(HaveOccurred())
		got, ok := f.store.get(key.ResourceID)
		Expect(ok).To(BeTrue())
		Expect(got.Grants).To(Equal(map[string]Role{"carol": RoleViewer, "dave": RoleViewer}))
		Expect(rep.GrantsRestored).To(Equal(2))
		Expect(rep.GrantsDropped).To(Equal(2))
		// A restore with no account directory gives nobody access.
		rep, err = port.Restore(bg, "fixture", exp, recovery.RestoreOptions{Since: since})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.ResourcesRestored).To(BeZero())
		Expect(rep.ResourcesSkipped).To(ConsistOf(key.ResourceID))
	})

	It("keeps a bounded durable log of lowered access", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		Expect(f.policy.SetGrant(bg, owner, key, "bob", RoleEditor)).To(Succeed())
		Expect(f.policy.SetGrant(bg, owner, key, "bob", RoleViewer)).To(Succeed())
		Expect(f.policy.Revoke(bg, owner, key, "bob")).To(Succeed())
		f.store.mu.Lock()
		log := append([]revocation(nil), f.store.doc.Revoked...)
		f.store.mu.Unlock()
		// The removal covers the earlier lowering of the same person, so the
		// log keeps the one that decides every restore time.
		Expect(log).To(HaveLen(1))
		Expect(log[0].Ceiling).To(Equal(Role("")))
		// It is in the file, so a restart keeps it.
		Expect(f.store.Close()).To(Succeed())
		again, err := OpenStore(f.dir, ModeNamedUser)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = again.Close() })
		Expect(again.doc.Revoked).To(HaveLen(1))
		// The log has a finite size: the oldest entries go first and the
		// floor says up to when it may be incomplete.
		old := revocationCap
		revocationCap = 5
		DeferCleanup(func() { revocationCap = old })
		Expect(again.revokedFloor().IsZero()).To(BeTrue())
		err = again.mutate(func(d *document) error {
			for i := 0; i < 8; i++ {
				d.addRevocation(revocation{AddonID: "fixture", ResourceID: key.ResourceID, UserID: "u" + string(rune('a'+i)), Ceiling: RoleViewer, At: time.Now().UTC()})
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(again.doc.Revoked).To(HaveLen(5))
		Expect(again.revokedFloor().IsZero()).To(BeFalse())
	})

	It("cancels a pending proposal and leaves an executing one outcome-unknown after a restore", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		_, err := f.broker.Propose(bg, owner, f.request(key, "pending-op", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.stored("pending-op")).To(Equal(gateway.ProposalPending))
		_, err = f.broker.Propose(bg, owner, f.request(key, "running-op", "write", `{"rows":[2]}`))
		Expect(err).NotTo(HaveOccurred())
		_, err = f.broker.store.transition("running-op", []ProposalState{gateway.ProposalPending}, func(x *proposalRecord) {
			x.State = gateway.ProposalExecuting
		})
		Expect(err).NotTo(HaveOccurred())
		port := &RecoveryPort{Policy: f.policy, Broker: f.broker}
		exp, err := port.Export(bg, "fixture")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(exp.Proposals)).To(ContainSubstring("pending-op"))
		rep, err := port.Restore(bg, "fixture", exp, recovery.RestoreOptions{Account: func(context.Context, string) error { return nil }})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.ProposalsCancelled).To(Equal(1))
		Expect(rep.ProposalsUnknown).To(Equal(1))
		Expect(f.stored("pending-op")).To(Equal(gateway.ProposalCancelled))
		Expect(f.stored("running-op")).To(Equal(gateway.ProposalOutcomeUnknown))
		// Neither is applied: the owner never sent them again.
		Expect(f.rt.applyCalls()).To(BeEmpty())
		_, err = f.broker.Confirm(bg, session("owner"), "pending-op")
		Expect(err).To(HaveOccurred())
		Expect(f.rt.applyCalls()).To(BeEmpty())
	})

	It("forgets the records of one add-on and no other", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		other, err := f.policy.Register(bg, owner, "other")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.Commit(bg, owner, other)).To(Succeed())
		Expect(f.policy.SetGrant(bg, owner, key, "bob", RoleViewer)).To(Succeed())
		Expect(f.policy.Revoke(bg, owner, key, "bob")).To(Succeed())
		_, err = f.broker.Propose(bg, owner, f.request(key, "forgotten", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		port := &RecoveryPort{Policy: f.policy, Broker: f.broker}
		n, err := port.Forget(bg, "fixture")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		_, ok := f.store.get(key.ResourceID)
		Expect(ok).To(BeFalse())
		_, ok = f.store.get(other.ResourceID)
		Expect(ok).To(BeTrue())
		Expect(f.stored("forgotten")).To(Equal(ProposalState("")))
		Expect(f.store.doc.Revoked).To(BeEmpty())
	})

	It("maps an owner on a restore to a local-owner store only when nothing named is in it", func() {
		f := newFixture(ModeLocalOwner)
		port := &RecoveryPort{Policy: f.policy}
		snap := `{"version":1,"addonId":"fixture","mode":"named-user","resources":{"r1":{"owner":"alice"}}}`
		_, err := port.Restore(bg, "fixture", recovery.PolicyExport{Policy: []byte(snap)}, recovery.RestoreOptions{})
		Expect(err).To(MatchError(recovery.ErrDowngradeUnsupported))
		empty := `{"version":1,"addonId":"fixture","mode":"named-user","resources":{}}`
		_, err = port.Restore(bg, "fixture", recovery.PolicyExport{Policy: []byte(empty)}, recovery.RestoreOptions{})
		Expect(err).NotTo(HaveOccurred())
	})

	It("migrates the ownership of a stopped store with a backup and an explicit mapping and refuses a populated downgrade", func() {
		root := GinkgoT().TempDir()
		dir := filepath.Join(root, "policy")
		s, err := OpenStore(dir, ModeLocalOwner)
		Expect(err).NotTo(HaveOccurred())
		p := New(s, Options{Revalidate: func(context.Context, contract.Actor) error { return nil }, CreateAuthority: func(context.Context, contract.Actor, string) error { return nil }})
		local := contract.Actor{UserID: LocalOwnerID, CredentialID: LocalOwnerID, Mode: "local-owner"}
		key, err := p.Register(bg, local, "fixture")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Commit(bg, local, key)).To(Succeed())
		// While the owner holds the store, the migration is refused.
		_, err = MigrateOwnership(root, ModeNamedUser, recovery.OwnershipMapping{LocalOwnerID: "alice"}, time.Now())
		Expect(err).To(MatchError(ErrStoreBusy))
		Expect(s.Close()).To(Succeed())
		for name, m := range map[string]recovery.OwnershipMapping{
			"wildcard":  {"*": "alice"},
			"foreign":   {"someone": "alice"},
			"synthetic": {LocalOwnerID: "anonymous"},
		} {
			_, err = MigrateOwnership(root, ModeNamedUser, m, time.Now())
			Expect(err).To(HaveOccurred(), name)
		}
		_, err = MigrateOwnership(root, ModeNamedUser, nil, time.Now())
		Expect(err).To(MatchError(recovery.ErrMappingRequired))
		backup, err := MigrateOwnership(root, ModeNamedUser, recovery.OwnershipMapping{LocalOwnerID: "alice"}, time.Now())
		Expect(err).NotTo(HaveOccurred())
		Expect(backup).To(HavePrefix(filepath.Join(root, "backups", "ownership")))
		Expect(filepath.Join(backup, "policy.json")).To(BeAnExistingFile())
		s, err = OpenStore(dir, ModeNamedUser)
		Expect(err).NotTo(HaveOccurred())
		rec, ok := s.get(key.ResourceID)
		Expect(ok).To(BeTrue())
		Expect(rec.Owner).To(Equal("alice"))
		Expect(s.Close()).To(Succeed())
		// Populated named-user data does not go back to the local owner.
		_, err = MigrateOwnership(root, ModeLocalOwner, nil, time.Now())
		Expect(err).To(MatchError(recovery.ErrDowngradeUnsupported))
		s, err = OpenStore(dir, ModeNamedUser)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
	})
})

var _ = Describe("Addon recovery state of the policy: revocation history", func() {
	bg := context.Background()
	open := func(users ...string) (*fixture, ResourceKey, *RecoveryPort, recovery.PolicyExport) {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		for _, u := range users {
			Expect(f.policy.SetGrant(bg, owner, key, u, RoleEditor)).To(Succeed())
		}
		port := &RecoveryPort{Policy: f.policy}
		exp, err := port.Export(bg, "fixture")
		Expect(err).NotTo(HaveOccurred())
		return f, key, port, exp
	}
	accountsAll := func(context.Context, string) error { return nil }
	small := func(n int) {
		old := revocationCap
		revocationCap = n
		DeferCleanup(func() { revocationCap = old })
	}

	It("keeps a revocation however many later lowerings of another person follow", func() {
		small(16)
		f, key, port, exp := open("bob", "zed")
		owner := named("owner")
		since := time.Now().UTC().Add(-time.Hour)
		Expect(f.policy.Revoke(bg, owner, key, "bob")).To(Succeed())
		for i := 0; i < 40; i++ {
			Expect(f.policy.SetGrant(bg, owner, key, "zed", RoleViewer)).To(Succeed())
			Expect(f.policy.SetGrant(bg, owner, key, "zed", RoleEditor)).To(Succeed())
		}
		rep, err := port.Restore(bg, "fixture", exp, recovery.RestoreOptions{Since: since, Account: accountsAll})
		Expect(err).NotTo(HaveOccurred())
		got, _ := f.store.get(key.ResourceID)
		Expect(got.Grants).NotTo(HaveKey("bob"))
		Expect(got.Grants).To(HaveKey("zed"))
		Expect(rep.HistoryTruncated).To(BeFalse())
		f.store.mu.Lock()
		n := len(f.store.doc.Revoked)
		f.store.mu.Unlock()
		Expect(n).To(BeNumerically("<=", 3*2))
	})

	It("restores the data but no access when the history may have lost a lowering", func() {
		small(8)
		users := []string{"bob", "carol"}
		for i := 0; i < 20; i++ {
			users = append(users, "u"+string(rune('a'+i)))
		}
		f, key, port, exp := open(users...)
		owner := named("owner")
		since := time.Now().UTC().Add(-time.Hour)
		for _, u := range users {
			Expect(f.policy.SetGrant(bg, owner, key, u, RoleViewer)).To(Succeed())
		}
		f.store.mu.Lock()
		floor := f.store.doc.RevokedFloor
		f.store.mu.Unlock()
		Expect(floor.IsZero()).To(BeFalse())
		rep, err := port.Restore(bg, "fixture", exp, recovery.RestoreOptions{Since: since, Account: accountsAll})
		Expect(err).NotTo(HaveOccurred())
		got, ok := f.store.get(key.ResourceID)
		Expect(ok).To(BeTrue())
		Expect(got.Owner).To(Equal("owner"))
		Expect(got.Grants).To(BeEmpty())
		Expect(rep.HistoryTruncated).To(BeTrue())
		Expect(rep.ResourcesRestored).To(Equal(1))
		Expect(rep.GrantsRestored).To(BeZero())
		Expect(rep.GrantsDropped).To(Equal(len(users)))
		// A backup made after the floor is not affected.
		_, err = port.Export(bg, "fixture")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.policy.SetGrant(bg, owner, key, "bob", RoleEditor)).To(Succeed())
		exp2, err := port.Export(bg, "fixture")
		Expect(err).NotTo(HaveOccurred())
		rep, err = port.Restore(bg, "fixture", exp2, recovery.RestoreOptions{Since: time.Now().UTC().Add(time.Hour), Account: accountsAll})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.HistoryTruncated).To(BeFalse())
		got, _ = f.store.get(key.ResourceID)
		Expect(got.Grants).To(HaveKey("bob"))
	})

	It("reads a store that is full of lowerings as one whose history may be cut", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		Expect(f.policy.SetGrant(bg, owner, key, "bob", RoleEditor)).To(Succeed())
		port := &RecoveryPort{Policy: f.policy}
		exp, err := port.Export(bg, "fixture")
		Expect(err).NotTo(HaveOccurred())
		at := time.Now().UTC().Add(time.Minute)
		Expect(f.store.mutate(func(d *document) error {
			for i := 0; i < MaxRevocations; i++ {
				d.Revoked = append(d.Revoked, revocation{AddonID: "other", ResourceID: "r", UserID: "u", Ceiling: RoleViewer, At: at.Add(time.Duration(i) * time.Second)})
			}
			return nil
		})).To(Succeed())
		Expect(f.store.Close()).To(Succeed())
		again, err := OpenStore(f.dir, ModeNamedUser)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = again.Close() })
		p2 := &RecoveryPort{Policy: New(again, f.opts)}
		rep, err := p2.Restore(bg, "fixture", exp, recovery.RestoreOptions{Since: time.Now().UTC().Add(-time.Hour), Account: accountsAll})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.HistoryTruncated).To(BeTrue())
		got, _ := again.get(key.ResourceID)
		Expect(got.Grants).To(BeEmpty())
	})

	It("applies a lowering made after the export whatever time the backup is given", func() {
		f, key, port, exp := open("bob")
		Expect(f.policy.Revoke(bg, named("owner"), key, "bob")).To(Succeed())
		// The backup's own creation time is later than the lowering.
		rep, err := port.Restore(bg, "fixture", exp, recovery.RestoreOptions{Since: time.Now().UTC().Add(time.Hour), Account: accountsAll})
		Expect(err).NotTo(HaveOccurred())
		got, _ := f.store.get(key.ResourceID)
		Expect(got.Grants).NotTo(HaveKey("bob"))
		Expect(rep.Revoked).To(Equal(1))
	})

	It("refuses a change that was authorized just before the hold began", func() {
		f := newFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		Expect(f.policy.SetGrant(bg, owner, key, "bob", RoleEditor)).To(Succeed())
		var release func()
		f.policy.afterAuthorize = func() {
			var err error
			release, err = f.policy.HoldAddon(bg, "fixture")
			Expect(err).NotTo(HaveOccurred())
		}
		err := f.policy.Revoke(bg, owner, key, "bob")
		Expect(err).To(MatchError(gateway.ErrUnavailable))
		release()
		got, _ := f.store.get(key.ResourceID)
		Expect(got.Grants).To(HaveKey("bob"))
		Expect(got.Epoch).To(Equal(uint64(0)))
	})
})

// revokedFloor reads the floor of the revocation log.
func (s *Store) revokedFloor() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.doc.RevokedFloor
}
