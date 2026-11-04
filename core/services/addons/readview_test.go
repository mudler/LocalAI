// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/recovery"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("reads during a long upgrade", func() {
	// blockedUpgrade starts an upgrade that stops inside the verified backup,
	// where the owner holds its state lock, and returns the function that
	// lets it end.
	blockedUpgrade := func() (*Manager, LifecycleCommand, func()) {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("initial", "install"))
		Expect(e).NotTo(HaveOccurred())
		dir := filepath.Join(cfg.Root, "data", "sample")
		Expect(os.MkdirAll(dir, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "retained"), []byte("data"), 0600)).To(Succeed())
		entered, release := make(chan struct{}), make(chan struct{})
		r, e := recovery.New(recovery.Config{Root: cfg.Root, Host: m.RecoveryHost(), Policy: &upgradePolicy{exporting: func() { close(entered); <-release }}, Admin: admin})
		Expect(e).NotTo(HaveOccurred())
		m.SetRecovery(r)
		cmd := command("long-upgrade", "upgrade")
		_, e = m.SubmitUpgrade(ctx, actor, cmd)
		Expect(e).NotTo(HaveOccurred())
		Eventually(entered).Should(BeClosed())
		done := func() {
			close(release)
			Eventually(func() string {
				st, e := m.OperationStatus(ctx, actor, cmd.OperationID)
				Expect(e).NotTo(HaveOccurred())
				return st.Phase
			}).Should(Equal(string(lifecycleCompleted)))
		}
		return m, cmd, done
	}

	It("answers the apps list at once, from the snapshot, with the operation state", func() {
		m, _, done := blockedUpgrade()
		defer done()
		read, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		started := time.Now()
		list, e := m.Apps(read, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(time.Since(started)).To(BeNumerically("<", 400*time.Millisecond))
		Expect(list).To(HaveLen(1))
		Expect(list[0].ID).To(Equal("sample"))
		Expect(list[0].Generation).To(Equal(uint64(1)), "the snapshot is the last published generation")
		Expect(list[0].Operation).NotTo(BeNil())
		Expect(list[0].Operation.Kind).To(Equal("upgrade"))
		Expect(list[0].Operation.Phase).To(Equal("backing-up"))
		Expect(list[0].Operation.Since).NotTo(BeEmpty())
	})

	It("serves every other read from the snapshot too", func() {
		m, _, done := blockedUpgrade()
		defer done()
		read, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		ids, e := m.InstalledIDs(read)
		Expect(e).NotTo(HaveOccurred())
		Expect(ids).To(Equal([]string{"sample"}))
		access, e := m.HostAccessInstalled(read)
		Expect(e).NotTo(HaveOccurred())
		Expect(access).To(HaveLen(1))
		installed, e := m.Installed(read, actor)
		Expect(e).NotTo(HaveOccurred())
		Expect(installed).To(HaveLen(1))
		Expect(installed[0].Operation).NotTo(BeNil())
		Expect(installed[0].Operation.Phase).To(Equal("backing-up"))
	})

	It("refuses a second upgrade with a conflict instead of racing or waiting", func() {
		m, cmd, done := blockedUpgrade()
		defer done()
		other := command("second-upgrade", "upgrade")
		other.AddonID = cmd.AddonID
		read, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		started := time.Now()
		_, e := m.SubmitUpgrade(read, actor, other)
		Expect(e).To(MatchError(ErrConflict))
		Expect(time.Since(started)).To(BeNumerically("<", 400*time.Millisecond))
	})

	It("shows the new generation and no operation once the upgrade ended", func() {
		m, _, done := blockedUpgrade()
		done()
		Eventually(func() uint64 {
			list, e := m.Apps(ctx, actor)
			Expect(e).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			return list[0].Generation
		}).Should(Equal(uint64(2)))
		list, _ := m.Apps(ctx, actor)
		Expect(list[0].Operation).To(BeNil())
	})

	It("still refuses a read after the owner closed", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		Expect(m.Close()).To(Succeed())
		_, e := m.Apps(ctx, actor)
		Expect(e).To(MatchError(ErrNotOwner))
	})
})
