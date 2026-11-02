// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/recovery"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The policy export happens inside the real verified-backup path, before
// Installer.Commit. Cancelling here reproduces a caller going away while a
// backup completes, without relying on a large directory or a wall-clock race.
type upgradePolicy struct {
	exporting        func()
	exportingContext func(context.Context)
}

func (*upgradePolicy) Mode() string                                 { return "named-user" }
func (*upgradePolicy) Hold(context.Context, string) (func(), error) { return func() {}, nil }
func (p *upgradePolicy) Export(ctx context.Context, _ string) (recovery.PolicyExport, error) {
	if p.exportingContext != nil {
		p.exportingContext(ctx)
	}
	if p.exporting != nil {
		p.exporting()
	}
	return recovery.PolicyExport{}, nil
}
func (*upgradePolicy) Restore(context.Context, string, recovery.PolicyExport, recovery.RestoreOptions) (recovery.RestoreReport, error) {
	return recovery.RestoreReport{}, nil
}
func (*upgradePolicy) LostWrites(context.Context, string, time.Time) (int, error) { return 0, nil }
func (*upgradePolicy) Forget(context.Context, string) (int, error)                { return 0, nil }

var _ = Describe("owner-managed upgrade operations", func() {
	It("finishes a real backup and commit after the submitting request is cancelled", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("initial", "install"))
		Expect(e).NotTo(HaveOccurred())
		dir := filepath.Join(cfg.Root, "data", "sample")
		Expect(os.MkdirAll(dir, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "retained"), []byte("last good data"), 0600)).To(Succeed())
		request, cancel := context.WithCancel(ctx)
		defer cancel()
		exported := make(chan struct{})
		r, e := recovery.New(recovery.Config{Root: cfg.Root, Host: m.RecoveryHost(), Policy: &upgradePolicy{exporting: func() { close(exported); cancel() }}, Admin: admin})
		Expect(e).NotTo(HaveOccurred())
		m.SetRecovery(r)
		result, e := m.SubmitUpgrade(request, actor, command("upgrade-disconnect", "upgrade"))
		Expect(e).NotTo(HaveOccurred())
		Expect(result.Phase).To(Equal(string(lifecycleAccepted)))
		Eventually(exported).Should(BeClosed())
		Eventually(func() string {
			status, err := m.OperationStatus(ctx, actor, "upgrade-disconnect")
			Expect(err).NotTo(HaveOccurred())
			result = status
			return status.Phase
		}).Should(Equal(string(lifecycleCompleted)))
		Expect(result.Generation).To(Equal(uint64(2)))
	})
	It("polls and retries during a blocked backup, rejects conflicting IDs, and retains the receipt on restart", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("initial", "install"))
		Expect(e).NotTo(HaveOccurred())
		dir := filepath.Join(cfg.Root, "data", "sample")
		Expect(os.MkdirAll(dir, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "retained"), []byte("data"), 0600)).To(Succeed())
		entered, release := make(chan struct{}), make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		r, e := recovery.New(recovery.Config{Root: cfg.Root, Host: m.RecoveryHost(), Policy: &upgradePolicy{exporting: func() { close(entered); <-release }}, Admin: admin})
		Expect(e).NotTo(HaveOccurred())
		m.SetRecovery(r)
		cmd := command("lost-reply", "upgrade")
		_, e = m.SubmitUpgrade(ctx, actor, cmd)
		Expect(e).NotTo(HaveOccurred())
		Eventually(entered).Should(BeClosed())
		read, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		status, e := m.OperationStatus(read, actor, cmd.OperationID)
		Expect(e).NotTo(HaveOccurred())
		Expect(status.Stage).To(Equal("backing-up"))
		status, e = m.SubmitUpgrade(read, actor, cmd)
		Expect(e).NotTo(HaveOccurred())
		Expect(status.Phase).To(Equal(string(lifecycleRunning)))
		changed := cmd
		changed.Version = "9.0.0"
		_, e = m.SubmitUpgrade(read, actor, changed)
		Expect(e).To(MatchError(ErrConflict))
		changed = cmd
		changed.OperationID = "another"
		_, e = m.SubmitUpgrade(read, actor, changed)
		Expect(e).To(MatchError(ErrConflict))
		other := actor
		other.CredentialID = "expired"
		_, e = m.OperationStatus(read, other, cmd.OperationID)
		Expect(e).To(HaveOccurred())
		close(release)
		Eventually(func() string {
			st, e := m.OperationStatus(ctx, actor, cmd.OperationID)
			Expect(e).NotTo(HaveOccurred())
			return st.Phase
		}).Should(Equal(string(lifecycleCompleted)))
		Expect(m.Close()).To(Succeed())
		second := open(cfg, c, i)
		Expect(second.Reconcile(ctx)).To(Succeed())
		status, e = second.SubmitUpgrade(ctx, actor, cmd)
		Expect(e).NotTo(HaveOccurred())
		Expect(status.Phase).To(Equal(string(lifecycleCompleted)))
		Expect(status.Generation).To(Equal(uint64(2)))
	})
	It("reconciles durable accepted work to interrupted without replay", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("initial", "install"))
		Expect(e).NotTo(HaveOccurred())
		cmd := command("interrupted", "upgrade")
		m.mu.Lock()
		m.journal.Operations[cmd.OperationID] = journalOperation{Command: cmd, Actor: actor, Async: true, Status: OperationStatus{OperationID: cmd.OperationID, AddonID: cmd.AddonID, Phase: string(lifecycleAccepted)}}
		Expect(m.persist(string(lifecycleAccepted))).To(Succeed())
		m.mu.Unlock()
		Expect(m.Close()).To(Succeed())
		second := open(cfg, c, i)
		Expect(second.Reconcile(ctx)).To(Succeed())
		status, e := second.OperationStatus(ctx, actor, cmd.OperationID)
		Expect(e).NotTo(HaveOccurred())
		Expect(status.Phase).To(Equal(string(lifecycleFailed)))
		Expect(status.FailureCode).To(Equal("interrupted"))
		st, e := second.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Generation).To(Equal(uint64(1)))
	})
	It("Close cancels and joins an accepted owner worker before releasing storage", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("initial", "install"))
		Expect(e).NotTo(HaveOccurred())
		// Hold the execution gate to ensure Close must join the accepted worker.
		m.opMu.Lock()
		_, e = m.SubmitUpgrade(ctx, actor, command("shutdown", "upgrade"))
		Expect(e).NotTo(HaveOccurred())
		done := make(chan error, 1)
		go func() { done <- m.Close() }()
		Consistently(done, 30*time.Millisecond).ShouldNot(Receive())
		m.opMu.Unlock()
		Eventually(done).Should(Receive(BeNil()))
		second := open(cfg, c, i)
		Expect(second.Reconcile(ctx)).To(Succeed())
		status, e := second.OperationStatus(ctx, actor, "shutdown")
		Expect(e).NotTo(HaveOccurred())
		Expect(status.Phase).To(Equal(string(lifecycleFailed)))
		Expect(status.FailureCode).To(Equal("interrupted"))
	})

	It("Close cancels a backup in flight and joins its cleanup before releasing ownership", func() {
		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("initial", "install"))
		Expect(e).NotTo(HaveOccurred())
		dir := filepath.Join(cfg.Root, "data", "sample")
		Expect(os.MkdirAll(dir, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "retained"), []byte("last good data"), 0600)).To(Succeed())
		entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		r, e := recovery.New(recovery.Config{Root: cfg.Root, Host: m.RecoveryHost(), Policy: &upgradePolicy{exportingContext: func(work context.Context) { close(entered); <-work.Done(); close(cancelled); <-release }}, Admin: admin})
		Expect(e).NotTo(HaveOccurred())
		m.SetRecovery(r)
		_, e = m.SubmitUpgrade(ctx, actor, command("shutdown-backup", "upgrade"))
		Expect(e).NotTo(HaveOccurred())
		Eventually(entered).Should(BeClosed())
		done := make(chan error, 1)
		go func() { done <- m.Close() }()
		Eventually(cancelled).Should(BeClosed())
		// Cancellation reached real backup I/O, but its callback has not returned.
		select {
		case e := <-done:
			Fail("Close returned before backup cleanup: " + fmt.Sprint(e))
		default:
		}
		_, e = NewManager(ctx, cfg, c, i, admin)
		Expect(e).To(MatchError(ErrOwnerBusy))
		close(release)
		Eventually(done).Should(Receive(BeNil()))
		second := open(cfg, c, i)
		Expect(second.Reconcile(ctx)).To(Succeed())
		status, e := second.OperationStatus(ctx, actor, "shutdown-backup")
		Expect(e).NotTo(HaveOccurred())
		Expect(status.Phase).To(Equal(string(lifecycleFailed)))
		st, e := second.Status(ctx, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Generation).To(Equal(uint64(1)))
		data, e := os.ReadFile(filepath.Join(dir, "retained"))
		Expect(e).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("last good data"))
	})
	It("serializes synchronous disable against an accepted upgrade without overwriting its journal", func() {
		// An accepted journal loaded before restart reconciliation has no worker.
		// This pins the guard itself independently of opMu waiter ordering below.
		cfgPending, cPending, iPending := fixture()
		pending := open(cfgPending, cPending, iPending)
		_, err := pending.Submit(ctx, actor, command("initial", "install"))
		Expect(err).NotTo(HaveOccurred())
		acceptedCmd := command("pending", "upgrade")
		pending.mu.Lock()
		pending.journal.Operations[acceptedCmd.OperationID] = journalOperation{Command: acceptedCmd, Actor: actor, Async: true, Status: OperationStatus{OperationID: acceptedCmd.OperationID, AddonID: acceptedCmd.AddonID, Phase: string(lifecycleAccepted)}}
		Expect(pending.persist(string(lifecycleAccepted))).To(Succeed())
		pending.mu.Unlock()
		Expect(pending.Close()).To(Succeed())
		loaded := open(cfgPending, cPending, iPending)
		_, err = loaded.Submit(ctx, actor, command("must-not-overtake", "disable"))
		Expect(err).To(MatchError(ErrRecoveryRequired))
		loaded.mu.Lock()
		Expect(loaded.journal.Operations[acceptedCmd.OperationID].Status.Phase).To(Equal(string(lifecycleAccepted)))
		_, exists := loaded.journal.Operations["must-not-overtake"]
		Expect(exists).To(BeFalse())
		Expect(loaded.journal.Addons["sample"].Generation).To(Equal(uint64(1)))
		loaded.mu.Unlock()

		cfg, c, i := fixture()
		m := open(cfg, c, i)
		_, e := m.Submit(ctx, actor, command("initial", "install"))
		Expect(e).NotTo(HaveOccurred())
		m.opMu.Lock()
		locked := true
		defer func() {
			if locked {
				m.opMu.Unlock()
			}
		}()
		cmd := command("accepted-disable", "upgrade")
		_, e = m.SubmitUpgrade(ctx, actor, cmd)
		Expect(e).NotTo(HaveOccurred())
		status, e := m.OperationStatus(ctx, actor, cmd.OperationID)
		Expect(e).NotTo(HaveOccurred())
		Expect(status.Phase).To(Equal(string(lifecycleAccepted)))
		done := make(chan error, 1)
		started := make(chan struct{})
		go func() {
			close(started)
			_, err := m.Submit(ctx, actor, command("concurrent-disable", "disable"))
			done <- err
		}()
		<-started
		m.opMu.Unlock()
		locked = false
		var submitErr error
		Eventually(done).Should(Receive(&submitErr))
		// Either the journal guard rejects the unresolved acceptance, or opMu runs
		// disable after the upgrade completes. Neither ordering may overtake it.
		if submitErr != nil {
			Expect(submitErr).To(MatchError(ErrRecoveryRequired))
		}
		Eventually(func() string {
			st, e := m.OperationStatus(ctx, actor, cmd.OperationID)
			Expect(e).NotTo(HaveOccurred())
			return st.Phase
		}).Should(Equal(string(lifecycleCompleted)))
		m.mu.Lock()
		defer m.mu.Unlock()
		Expect(m.journal.Operations[cmd.OperationID].Command).To(Equal(cmd))
		Expect(m.journal.Addons["sample"].Generation).To(Equal(uint64(2)))
		if submitErr != nil {
			_, exists := m.journal.Operations["concurrent-disable"]
			Expect(exists).To(BeFalse())
		} else {
			Expect(m.journal.Operations["concurrent-disable"].Status.Phase).To(Equal(string(lifecycleCompleted)))
			Expect(m.journal.Addons["sample"].DesiredEnabled).To(BeFalse())
		}
	})

})
