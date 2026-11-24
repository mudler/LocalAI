// SPDX-License-Identifier: MIT
package recovery_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// migrating is the candidate of a test: it starts, changes the data as a
// migration does and reports it complete.
func migrating(r *rig) func(string) error {
	return func(dataDir string) error {
		r.addRow("migrated")
		return os.WriteFile(filepath.Join(dataDir, "migration.json"), []byte(`{"state":"complete","from":1,"to":1}`), 0o600)
	}
}

func upgradeFailpoint(phase string, err error) func(string) error {
	return func(p string) error {
		if p == phase {
			return err
		}
		return nil
	}
}

var _ = Describe("Upgrade of an add-on with data", func() {
	candidate := func(format uint64) staged { return staged{receipt("0.2.0", format)} }

	It("backs up, publishes the package, migrates, checks health and publishes the generation in that order", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(4)
		r.closeDBs()
		var order []string
		r.rec.SetFailpoint(func(phase string) error {
			order = append(order, phase)
			return nil
		})
		r.host.start = func(dir string) error {
			// The backup of the old data exists before the candidate runs.
			entries, _ := os.ReadDir(filepath.Join(r.root, "backups", addon))
			Expect(entries).To(HaveLen(1))
			Expect(r.host.quiesced).To(BeTrue())
			return migrating(r)(dir)
		}
		rc, e := r.rec.UpgradeReceipt(context.Background(), addon, candidate(1))
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.Manifest.Version).To(Equal("0.2.0"))
		Expect(r.host.published).To(Equal([]uint64{2}))
		Expect(order).To(ContainElements("before-backup", "after-backup", "after-stage", "mid-migrate", "after-migrate", "after-publish"))
		// The data is migrated; the backup holds what it was before.
		Expect(r.rowsIn(r.dbPath())).To(Equal(5))
		list, e := r.rec.List(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Reason).To(Equal("upgrade"))
		Expect(list[0].Pinned).To(BeTrue())
		Expect(r.rowsIn(filepath.Join(r.root, "backups", addon, list[0].ID, "data", "tables", "databases", "r1.db"))).To(Equal(4))
		st, e := r.rec.Status(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(st.State).To(Equal("ready"))
		Expect(r.rec.Blocked(addon)).To(BeFalse())
		Expect(r.host.info.Running).To(BeTrue())
	})

	It("undoes a failure at every journal phase before the publication and puts the old generation back on its own data", func() {
		for _, phase := range []string{"before-backup", "after-backup", "after-stage", "mid-migrate", "after-migrate"} {
			By(phase)
			r := newRig(policy.ModeNamedUser, "admin", "alice")
			r.seed(4)
			r.closeDBs()
			r.host.start = migrating(r)
			boom := errors.New("injected failure at " + phase)
			r.rec.SetFailpoint(upgradeFailpoint(phase, boom))
			_, e := r.rec.UpgradeReceipt(context.Background(), addon, candidate(1))
			Expect(e).To(MatchError(boom))
			// Not published, not blocked, the old generation runs on the
			// data it had.
			Expect(r.host.published).To(BeEmpty(), phase)
			Expect(r.host.info.Generation).To(Equal(uint64(1)))
			Expect(r.rec.Blocked(addon)).To(BeFalse(), phase)
			Expect(r.rowsIn(r.dbPath())).To(Equal(4), phase)
			Expect(r.host.info.Running).To(BeTrue(), phase)
			Expect(r.host.quiesced).To(BeFalse(), phase)
			_, journaled := os.Stat(filepath.Join(r.root, "recovery", addon, "journal.json"))
			Expect(os.IsNotExist(journaled)).To(BeTrue(), phase)
		}
	})

	It("finishes an upgrade whose publication was made when a later step failed", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(4)
		r.closeDBs()
		r.host.start = migrating(r)
		r.rec.SetFailpoint(upgradeFailpoint("after-publish", errors.New("late failure")))
		_, e := r.rec.UpgradeReceipt(context.Background(), addon, candidate(1))
		Expect(e).To(HaveOccurred())
		Expect(r.host.published).To(Equal([]uint64{2}))
		Expect(r.rowsIn(r.dbPath())).To(Equal(5))
		Expect(r.rec.Blocked(addon)).To(BeFalse())
		st, _ := r.rec.Status(context.Background(), addon)
		Expect(st.Phase).To(BeEmpty())
	})

	It("applies one rule after a restart at any phase: unpublished is undone, published is finished", func() {
		for _, phase := range []string{"after-backup", "after-stage", "mid-migrate", "after-migrate", "after-publish"} {
			By(phase)
			r := newRig(policy.ModeNamedUser, "admin", "alice")
			r.seed(4)
			r.closeDBs()
			r.host.start = migrating(r)
			// The process dies at the phase: nothing cleans up.
			r.rec.SetFailpoint(upgradeFailpoint(phase, recovery.ErrCrash))
			_, e := r.rec.UpgradeReceipt(context.Background(), addon, candidate(1))
			Expect(e).To(MatchError(recovery.ErrCrash))
			// A migration that ran has already changed the data.
			if phase == "mid-migrate" {
				r.addRow("half-migrated")
			}
			r.host.restart()
			r.newRecovery()
			Expect(r.rec.Reconcile(context.Background())).To(Succeed())
			st, _ := r.rec.Status(context.Background(), addon)
			Expect(st.State).To(Equal("ready"), phase)
			Expect(st.Phase).To(BeEmpty(), phase)
			if phase == "after-publish" {
				Expect(r.host.info.Generation).To(Equal(uint64(2)))
				Expect(r.rowsIn(r.dbPath())).To(Equal(5))
			} else {
				Expect(r.host.info.Generation).To(Equal(uint64(1)))
				Expect(r.rowsIn(r.dbPath())).To(Equal(4), phase)
			}
		}
	})

	It("refuses a changed trusted identity and an older data format before it stops or writes anything", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(2)
		r.host.set(func(i *recovery.Info) { i.Receipt = receipt("0.1.0", 2) })
		for name, c := range map[string]staged{
			"source":       {receiptWith(func(p *packages.PackageReceipt) { p.SourceID = "other-library" })},
			"channel":      {receiptWith(func(p *packages.PackageReceipt) { p.Channel = "beta" })},
			"verification": {receiptWith(func(p *packages.PackageReceipt) { p.Verification = "sha256" })},
			"addon":        {receiptWith(func(p *packages.PackageReceipt) { p.Manifest.ID = "someone-else" })},
		} {
			_, e := r.rec.UpgradeReceipt(context.Background(), addon, c)
			Expect(e).To(MatchError(recovery.ErrIdentityChanged), name)
		}
		_, e := r.rec.UpgradeReceipt(context.Background(), addon, staged{receipt("0.2.0", 1)})
		Expect(e).To(MatchError(recovery.ErrIncompatibleFormat))
		Expect(r.host.quiesced).To(BeFalse())
		Expect(r.host.resumes).To(BeZero())
		entries, _ := os.ReadDir(filepath.Join(r.root, "backups", addon))
		Expect(entries).To(BeEmpty())
		Expect(r.rec.Blocked(addon)).To(BeFalse())
	})

	It("keeps the add-on unavailable in needs-recovery after a failed migration to a new format and retains the package and the recovery copy", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(4)
		r.closeDBs()
		r.host.start = func(dir string) error {
			r.addRow("half-migrated")
			return errors.New("exit status 9")
		}
		_, e := r.rec.UpgradeReceipt(context.Background(), addon, candidate(2))
		Expect(e).To(MatchError(recovery.ErrNeedsRecovery))
		Expect(r.host.published).To(BeEmpty())
		Expect(r.host.info.Generation).To(Equal(uint64(1)))
		Expect(r.host.info.Receipt.Manifest.Version).To(Equal("0.1.0"))
		// Nothing starts it: the package is back but the data is not.
		Expect(r.rec.Blocked(addon)).To(BeTrue())
		Expect(r.host.info.Running).To(BeFalse())
		Expect(r.host.resumes).To(BeZero())
		st, e := r.rec.Status(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(st.State).To(Equal("needs-recovery"))
		Expect(st.RecoveryBackup).NotTo(BeEmpty())
		Expect(st.Reason).To(ContainSubstring("previous package cannot read"))
		// The half migrated data is kept as it is for inspection and the
		// recovery copy is verified and still there.
		Expect(r.rowsIn(r.dbPath())).To(Equal(5))
		_, e = r.rec.Verify(context.Background(), addon, st.RecoveryBackup)
		Expect(e).NotTo(HaveOccurred())
		// A second upgrade is refused until the data is recovered.
		_, e = r.rec.UpgradeReceipt(context.Background(), addon, candidate(2))
		Expect(e).To(MatchError(recovery.ErrNeedsRecovery))
		// The explicit restore brings the old data back and lifts the block.
		Expect(r.rec.Restore(context.Background(), r.admin(), st.RecoveryBackup, nil)).To(Succeed())
		Expect(r.rec.Blocked(addon)).To(BeFalse())
		Expect(r.rowsIn(r.dbPath())).To(Equal(4))
	})

	It("restores the backup itself when a migration that keeps the format fails halfway", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(4)
		r.closeDBs()
		r.host.start = func(dir string) error {
			r.addRow("half-migrated")
			return errors.New("exit status 9")
		}
		_, e := r.rec.UpgradeReceipt(context.Background(), addon, candidate(1))
		Expect(e).To(HaveOccurred())
		Expect(errors.Is(e, recovery.ErrNeedsRecovery)).To(BeFalse())
		Expect(r.rowsIn(r.dbPath())).To(Equal(4))
		Expect(r.rec.Blocked(addon)).To(BeFalse())
		Expect(r.host.info.Running).To(BeTrue())
		// The data the candidate left is kept beside, for inspection.
		st, _ := r.rec.Status(context.Background(), addon)
		Expect(st.PreviousCopies).To(Equal(1))
	})

	It("keeps a running add-on unavailable and recoverable when the backup cannot be verified at rollback", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(4)
		r.closeDBs()
		r.host.start = func(dir string) error {
			// The recovery copy is damaged while the candidate runs.
			entries, _ := os.ReadDir(filepath.Join(r.root, "backups", addon))
			f := filepath.Join(r.root, "backups", addon, entries[0].Name(), "data", "note.txt")
			Expect(os.WriteFile(f, []byte("damaged"), 0o600)).To(Succeed())
			return errors.New("exit status 9")
		}
		_, e := r.rec.UpgradeReceipt(context.Background(), addon, candidate(1))
		Expect(e).To(MatchError(recovery.ErrNeedsRecovery))
		st, _ := r.rec.Status(context.Background(), addon)
		Expect(st.State).To(Equal("needs-recovery"))
		Expect(st.Reason).To(ContainSubstring("could not be put back"))
		Expect(r.host.info.Running).To(BeFalse())
		raw, _ := json.Marshal(st)
		Expect(string(raw)).To(ContainSubstring("needs-recovery"))
	})
})

func receiptWith(f func(*packages.PackageReceipt)) packages.PackageReceipt {
	r := receipt("0.2.0", 2)
	f(&r)
	return r
}
