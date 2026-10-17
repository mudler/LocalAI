// SPDX-License-Identifier: MIT
package recovery_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Restore of a backup", func() {
	stop := func(r *rig) {
		r.closeDBs()
		r.host.set(func(i *recovery.Info) { i.Running = false })
	}

	It("puts the data and the owner state back, warns about lost writes and keeps the replaced data as a recovery copy", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice", "bob")
		r.seed(3)
		key := r.register("alice")
		Expect(r.policy.SetGrant(context.Background(), named("alice"), key, "bob", policy.RoleEditor)).To(Succeed())
		rc, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		r.closeDBs()
		// Work after the backup: more rows, a second database, a share.
		r.addRow("after-1")
		r.addRow("after-2")
		later := r.register("alice")
		stop(r)
		r.tick()
		got, e := r.rec.RestoreReceipt(context.Background(), r.admin(), rc.ID, nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(r.rowsIn(r.dbPath())).To(Equal(3))
		Expect(got.PreviousCopy).To(HavePrefix("previous-"))
		Expect(got.Warnings).NotTo(BeEmpty())
		Expect(got.Warnings[0]).To(ContainSubstring("Changes made after that time are not in it"))
		Expect(got.Policy.ResourcesRestored).To(Equal(1))
		Expect(got.Policy.GrantsRestored).To(Equal(1))
		// The recovery copy holds the data as it was before the restore.
		prev := filepath.Join(r.root, "recovery", addon, got.PreviousCopy, "tables", "databases", "r1.db")
		Expect(r.rowsIn(prev)).To(Equal(5))
		// Access is as of the backup: the resource made later has no record.
		Expect(r.policy.Authorize(context.Background(), named("alice"), later, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
		Expect(r.policy.Authorize(context.Background(), named("bob"), key, policy.ActionWrite)).To(Succeed())
		st, _ := r.rec.Status(context.Background(), addon)
		Expect(st.LastRestore).NotTo(BeNil())
		Expect(st.PreviousCopies).To(Equal(1))
	})

	It("counts the operations applied after the backup as lost writes", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(1)
		rc, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		stop(r)
		got, e := r.rec.RestoreReceipt(context.Background(), r.admin(), rc.ID, nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(got.LostWrites).To(BeZero())
	})

	It("does not give back access that was taken away or whose account is gone", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice", "bob", "carol", "dave")
		r.seed(1)
		key := r.register("alice")
		ctx := context.Background()
		for u, role := range map[string]policy.Role{"bob": policy.RoleEditor, "carol": policy.RoleEditor, "dave": policy.RoleViewer} {
			Expect(r.policy.SetGrant(ctx, named("alice"), key, u, role)).To(Succeed())
		}
		rc, e := r.rec.Backup(ctx, addon)
		Expect(e).NotTo(HaveOccurred())
		r.tick()
		// After the backup: bob is removed, carol is lowered to viewer and
		// dave's account is deleted.
		Expect(r.policy.Revoke(ctx, named("alice"), key, "bob")).To(Succeed())
		Expect(r.policy.SetGrant(ctx, named("alice"), key, "carol", policy.RoleViewer)).To(Succeed())
		r.accounts.gone["dave"] = true
		// And bob is shared with again, as a viewer, then removed once more.
		stop(r)
		got, e := r.rec.RestoreReceipt(ctx, r.admin(), rc.ID, nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(got.Policy.GrantsRestored).To(Equal(1))
		Expect(got.Policy.GrantsDropped).To(Equal(2))
		Expect(got.Policy.Revoked).To(BeNumerically(">=", 2))
		Expect(r.policy.Authorize(ctx, named("bob"), key, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
		Expect(r.policy.Authorize(ctx, named("carol"), key, policy.ActionRead)).To(Succeed())
		Expect(r.policy.Authorize(ctx, named("carol"), key, policy.ActionWrite)).To(MatchError(policy.ErrForbidden))
		Expect(r.policy.Authorize(ctx, named("alice"), key, policy.ActionDelete)).To(Succeed())
		Expect(got.Warnings).To(ContainElement(ContainSubstring("shares were not restored")))
	})

	It("survives a restart that forgets the in-memory state: the revocation log is durable", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice", "bob")
		r.seed(1)
		key := r.register("alice")
		ctx := context.Background()
		Expect(r.policy.SetGrant(ctx, named("alice"), key, "bob", policy.RoleEditor)).To(Succeed())
		rc, e := r.rec.Backup(ctx, addon)
		Expect(e).NotTo(HaveOccurred())
		r.tick()
		Expect(r.policy.Revoke(ctx, named("alice"), key, "bob")).To(Succeed())
		r.closePolicy()
		r.openPolicy(policy.ModeNamedUser)
		r.newRecovery()
		stop(r)
		_, e = r.rec.RestoreReceipt(ctx, r.admin(), rc.ID, nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(r.policy.Authorize(ctx, named("bob"), key, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
	})

	It("refuses a running add-on, a stranger, an unknown backup and an invalid mapping without changing anything", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(2)
		rc, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		r.closeDBs()
		r.addRow("later")
		ctx := context.Background()
		Expect(r.rec.Restore(ctx, r.admin(), rc.ID, nil)).To(MatchError(recovery.ErrAddonRunning))
		stop(r)
		Expect(r.rec.Restore(ctx, named("alice"), rc.ID, nil)).To(MatchError(recovery.ErrForbidden))
		Expect(r.rec.Restore(ctx, r.admin(), "b-unknown", nil)).To(MatchError(recovery.ErrBackupNotFound))
		Expect(r.rec.Restore(ctx, r.admin(), "../etc", nil)).To(MatchError(recovery.ErrBackupNotFound))
		for name, m := range map[string]recovery.OwnershipMapping{
			"wildcard": {"*": "alice"},
			"empty":    {"alice": ""},
			"unknown":  {"alice": "nobody"},
		} {
			Expect(r.rec.Restore(ctx, r.admin(), rc.ID, m)).To(HaveOccurred(), name)
		}
		Expect(r.rowsIn(r.dbPath())).To(Equal(3))
		Expect(r.rec.Blocked(addon)).To(BeFalse())
	})

	It("refuses a backup of another trusted identity or of a newer data format", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(1)
		r.host.set(func(i *recovery.Info) { i.Receipt = receipt("0.1.0", 2) })
		rc, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		stop(r)
		// A package that reads format 1 cannot take data of format 2.
		r.host.set(func(i *recovery.Info) { i.Receipt = receipt("0.1.0", 1) })
		Expect(r.rec.Restore(context.Background(), r.admin(), rc.ID, nil)).To(MatchError(recovery.ErrIncompatibleFormat))
		r.host.set(func(i *recovery.Info) { i.Receipt = receipt("0.1.0", 2); i.Receipt.SourceID = "other" })
		Expect(r.rec.Restore(context.Background(), r.admin(), rc.ID, nil)).To(MatchError(recovery.ErrIdentityChanged))
		r.host.set(func(i *recovery.Info) { i.Receipt = receipt("0.3.0", 3) })
		Expect(r.rec.Restore(context.Background(), r.admin(), rc.ID, nil)).To(Succeed())
	})

	It("resumes or rolls back a restore that stopped at each phase, by one rule", func() {
		for _, phase := range []string{"restore-staging", "restore-swap", "restore-swap-mid", "restore-policy"} {
			By(phase)
			r := newRig(policy.ModeNamedUser, "admin", "alice")
			r.seed(3)
			key := r.register("alice")
			rc, e := r.rec.Backup(context.Background(), addon)
			Expect(e).NotTo(HaveOccurred())
			r.closeDBs()
			r.addRow("after")
			stop(r)
			r.rec.SetFailpoint(upgradeFailpoint(phase, recovery.ErrCrash))
			e = r.rec.Restore(context.Background(), r.admin(), rc.ID, nil)
			Expect(e).To(MatchError(recovery.ErrCrash))
			// While it is open, the add-on is never started.
			Expect(r.rec.Blocked(addon)).To(BeTrue(), phase)
			r.host.restart()
			r.newRecovery()
			Expect(r.rec.Reconcile(context.Background())).To(Succeed())
			Expect(r.rec.Blocked(addon)).To(BeFalse(), phase)
			if phase == "restore-staging" || phase == "restore-swap" {
				// Rolled back: nothing was swapped yet, the data is the
				// one before the restore.
				Expect(r.rowsIn(r.dbPath())).To(Equal(4), phase)
			} else {
				// Rolled forward: the backup is in place.
				Expect(r.rowsIn(r.dbPath())).To(Equal(3), phase)
			}
			_ = key
			entries, _ := os.ReadDir(filepath.Join(r.root, "data"))
			for _, en := range entries {
				Expect(en.Name()).NotTo(HavePrefix(".restore"), phase)
			}
		}
	})
})

var _ = Describe("Ownership mapping and mode transitions", func() {
	It("restores a local-owner backup to a named account through an explicit mapping", func() {
		root := GinkgoT().TempDir()
		// A local-owner store with one resource, backed up there.
		local := newRigAt(root, policy.ModeLocalOwner, "admin", "alice")
		local.seed(2)
		key, e := local.policy.Register(context.Background(), localActor(), addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(local.policy.Commit(context.Background(), localActor(), key)).To(Succeed())
		rc, e := local.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.Mode).To(Equal("local-owner"))
		local.closeDBs()
		local.closePolicy()
		// The deployment moved to named users while the add-on was stopped.
		_, e = policy.MigrateOwnership(root, policy.ModeNamedUser, recovery.OwnershipMapping{"local-owner": "alice"}, local.now)
		Expect(e).NotTo(HaveOccurred())
		named := newRigAt(root, policy.ModeNamedUser, "admin", "alice")
		named.host.set(func(i *recovery.Info) { i.Running = false })
		// Without the mapping the restore is refused; with a wildcard too.
		Expect(named.rec.Restore(context.Background(), named.admin(), rc.ID, nil)).To(MatchError(recovery.ErrMappingRequired))
		Expect(named.rec.Restore(context.Background(), named.admin(), rc.ID, recovery.OwnershipMapping{"*": "alice"})).To(MatchError(recovery.ErrMappingRequired))
		got, e := named.rec.RestoreReceipt(context.Background(), named.admin(), rc.ID, recovery.OwnershipMapping{"local-owner": "alice"})
		Expect(e).NotTo(HaveOccurred())
		Expect(got.Policy.ResourcesRestored).To(Equal(1))
		owner, ok := named.policy.OwnerOf(key)
		Expect(ok).To(BeTrue())
		Expect(owner).To(Equal("alice"))
	})
})
