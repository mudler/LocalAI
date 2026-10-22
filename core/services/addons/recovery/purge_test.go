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

var _ = Describe("Purge of retained data", func() {
	setup := func() *rig {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(2)
		r.register("alice")
		_, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		r.closeDBs()
		r.host.set(func(i *recovery.Info) { i.Running, i.Enabled = false, false })
		return r
	}

	It("needs an administrator, the typed add-on ID, an explicit scope and an add-on that is not running", func() {
		r := setup()
		ctx := context.Background()
		all := recovery.PurgeScope{Data: true}
		_, e := r.rec.Purge(ctx, named("alice"), addon, all, addon)
		Expect(e).To(MatchError(recovery.ErrForbidden))
		_, e = r.rec.Purge(ctx, r.admin(), addon, all, "")
		Expect(e).To(MatchError(recovery.ErrConfirmation))
		_, e = r.rec.Purge(ctx, r.admin(), addon, all, "other")
		Expect(e).To(MatchError(recovery.ErrConfirmation))
		_, e = r.rec.Purge(ctx, r.admin(), addon, recovery.PurgeScope{}, addon)
		Expect(e).To(MatchError(recovery.ErrInvalid))
		r.host.set(func(i *recovery.Info) { i.Enabled = true })
		_, e = r.rec.Purge(ctx, r.admin(), addon, all, addon)
		Expect(e).To(MatchError(recovery.ErrAddonRunning))
		Expect(filepath.Join(r.root, "data", addon)).To(BeADirectory())
	})

	It("removes the data and its records but keeps the backups unless they are in the scope", func() {
		r := setup()
		ctx := context.Background()
		got, e := r.rec.Purge(ctx, r.admin(), addon, recovery.PurgeScope{Data: true}, addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(got.Resources).To(Equal(1))
		Expect(got.Warnings).NotTo(BeEmpty())
		Expect(filepath.Join(r.root, "data", addon)).NotTo(BeADirectory())
		list, _ := r.rec.List(ctx, addon)
		Expect(list).To(HaveLen(1))
		keys, e := r.policy.Accessible(ctx, named("alice"), addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(keys).To(BeEmpty())
		// The backups go only when they are named.
		_, e = r.rec.Purge(ctx, r.admin(), addon, recovery.PurgeScope{Backups: true}, addon)
		Expect(e).NotTo(HaveOccurred())
		list, _ = r.rec.List(ctx, addon)
		Expect(list).To(BeEmpty())
	})

	It("touches no other add-on", func() {
		r := setup()
		other := filepath.Join(r.root, "data", "notes")
		Expect(os.MkdirAll(other, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(other, "keep"), []byte("keep"), 0o600)).To(Succeed())
		key, e := r.policy.Register(context.Background(), named("alice"), "notes")
		Expect(e).NotTo(HaveOccurred())
		Expect(r.policy.Commit(context.Background(), named("alice"), key)).To(Succeed())
		_, e = r.rec.Purge(context.Background(), r.admin(), addon, recovery.PurgeScope{Data: true, Backups: true, RecoveryCopies: true}, addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(filepath.Join(other, "keep")).To(BeAnExistingFile())
		keys, e := r.policy.Accessible(context.Background(), named("alice"), "notes")
		Expect(e).NotTo(HaveOccurred())
		Expect(keys).To(HaveLen(1))
	})

	It("finishes a purge that stopped after its journal was written", func() {
		r := setup()
		r.rec.SetFailpoint(upgradeFailpoint("purge-journaled", recovery.ErrCrash))
		_, e := r.rec.Purge(context.Background(), r.admin(), addon, recovery.PurgeScope{Data: true, Backups: true}, addon)
		Expect(e).To(MatchError(recovery.ErrCrash))
		Expect(filepath.Join(r.root, "data", addon)).To(BeADirectory())
		r.host.restart()
		r.newRecovery()
		Expect(r.rec.Reconcile(context.Background())).To(Succeed())
		Expect(filepath.Join(r.root, "data", addon)).NotTo(BeADirectory())
		entries, _ := os.ReadDir(filepath.Join(r.root, "backups", addon))
		Expect(entries).To(BeEmpty())
		Expect(r.rec.Blocked(addon)).To(BeFalse())
	})

	It("clears needs-recovery when the data it described is purged", func() {
		r := setup()
		r.host.set(func(i *recovery.Info) { i.Enabled = true })
		r.rec.SetFailpoint(nil)
		// Put the add-on into needs-recovery by a failed format migration.
		r.host.set(func(i *recovery.Info) { i.Running = true })
		r.host.start = func(string) error { return os.ErrInvalid }
		_, e := r.rec.UpgradeReceipt(context.Background(), addon, staged{receipt("0.2.0", 2)})
		Expect(e).To(MatchError(recovery.ErrNeedsRecovery))
		Expect(r.rec.Blocked(addon)).To(BeTrue())
		r.host.set(func(i *recovery.Info) { i.Enabled = false })
		_, e = r.rec.Purge(context.Background(), r.admin(), addon, recovery.PurgeScope{Data: true}, addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(r.rec.Blocked(addon)).To(BeFalse())
	})
})
