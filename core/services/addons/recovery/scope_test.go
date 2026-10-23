// SPDX-License-Identifier: MIT
package recovery_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Scope of a recovery operation", func() {
	candidate := func(format uint64) staged { return staged{receipt("0.2.0", format)} }

	It("refuses a backup of another add-on and touches neither data nor access", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice", "bob")
		r.seed(3)
		key := r.register("alice")
		ctx := context.Background()
		Expect(r.policy.SetGrant(ctx, named("alice"), key, "bob", policy.RoleEditor)).To(Succeed())
		own, e := r.rec.Backup(ctx, addon)
		Expect(e).NotTo(HaveOccurred())
		foreign, e := r.rec.Backup(ctx, "notes")
		Expect(e).NotTo(HaveOccurred())
		r.closeDBs()
		r.addRow("after")
		r.tick()
		Expect(r.policy.Revoke(ctx, named("alice"), key, "bob")).To(Succeed())
		r.host.set(func(i *recovery.Info) { i.Running = false })
		// The route names tables; the backup is the one of notes.
		_, e = r.rec.RestoreOp(ctx, r.admin(), "op-1", addon, foreign.ID, nil)
		Expect(e).To(MatchError(recovery.ErrBackupAddonMismatch))
		Expect(errors.Is(e, recovery.ErrInvalid)).To(BeTrue())
		Expect(r.rowsIn(r.dbPath())).To(Equal(4))
		Expect(r.rec.Blocked(addon)).To(BeFalse())
		st, _ := r.rec.Status(ctx, addon)
		Expect(st.LastRestore).To(BeNil())
		entries, _ := os.ReadDir(filepath.Join(r.root, "recovery", addon))
		for _, en := range entries {
			Expect(en.Name()).NotTo(HavePrefix("previous-"))
		}
		Expect(r.policy.Authorize(ctx, named("bob"), key, policy.ActionRead)).To(MatchError(policy.ErrForbidden))
		// The same holds for the other routes: the add-on of the route decides.
		_, e = r.rec.Verify(ctx, addon, foreign.ID)
		Expect(e).To(MatchError(recovery.ErrBackupNotFound))
		Expect(r.rec.DeleteBackup(ctx, r.admin(), addon, foreign.ID)).To(MatchError(recovery.ErrBackupNotFound))
		Expect(filepath.Join(r.root, "backups", "notes", foreign.ID)).To(BeADirectory())
		// A backup of the add-on itself is restored as before.
		got, e := r.rec.RestoreOp(ctx, r.admin(), "op-2", addon, own.ID, nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(got.AddonID).To(Equal(addon))
		Expect(r.rowsIn(r.dbPath())).To(Equal(3))
	})

	It("never lets a manual backup of half migrated data displace the pinned recovery copy", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.retain = recovery.Retention{Keep: 1, MaxAge: time.Minute}
		r.newRecovery()
		r.seed(4)
		r.closeDBs()
		r.host.start = func(string) error {
			r.addRow("half-migrated")
			return errors.New("exit status 9")
		}
		ctx := context.Background()
		_, e := r.rec.UpgradeReceipt(ctx, addon, candidate(2))
		Expect(e).To(MatchError(recovery.ErrNeedsRecovery))
		st, _ := r.rec.Status(ctx, addon)
		pinned := st.RecoveryBackup
		Expect(pinned).NotTo(BeEmpty())
		r.tick()
		// A backup of the half migrated data is refused, not made.
		_, e = r.rec.Backup(ctx, addon)
		Expect(e).To(MatchError(recovery.ErrNeedsRecovery))
		list, _ := r.rec.List(ctx, addon)
		Expect(list).To(HaveLen(1))
		// Neither age, count, a prune nor a delete takes the copy away.
		r.now = r.now.Add(72 * time.Hour)
		removed, e := r.rec.Prune(ctx, r.admin(), addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(removed).To(BeEmpty())
		Expect(r.rec.DeleteBackup(ctx, r.admin(), addon, pinned)).To(HaveOccurred())
		Expect(filepath.Join(r.root, "backups", addon, pinned)).To(BeADirectory())
		_, e = r.rec.Verify(ctx, addon, pinned)
		Expect(e).NotTo(HaveOccurred())
		// The explicit restore resolves it.
		r.host.set(func(i *recovery.Info) { i.Running = false })
		Expect(r.rec.Restore(ctx, r.admin(), pinned, nil)).To(Succeed())
		Expect(r.rowsIn(r.dbPath())).To(Equal(4))
	})
})
