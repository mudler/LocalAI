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
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Backup of add-on data", func() {
	It("copies a WAL database consistently with a manifest, digests, restricted permissions and the owner state", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice", "bob")
		path := r.seed(25)
		key := r.register("alice")
		Expect(r.policy.SetGrant(context.Background(), named("alice"), key, "bob", policy.RoleViewer)).To(Succeed())
		// The rows are in the WAL of an open connection; the main file alone
		// does not hold them.
		_, e := os.Stat(path + "-wal")
		Expect(e).NotTo(HaveOccurred())
		rc, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.Verified).To(BeTrue())
		Expect(rc.Digest).To(HavePrefix("sha256:"))
		Expect(rc.Files).To(BeNumerically(">=", 4))
		Expect(rc.Size).To(BeNumerically(">", 0))
		dir := filepath.Join(r.root, "backups", addon, rc.ID)
		Expect(r.rowsIn(filepath.Join(dir, "data", "tables", "databases", "r1.db"))).To(Equal(25))
		// Scratch exports are not part of a backup, and no WAL is copied.
		Expect(filepath.Join(dir, "data", "exports")).NotTo(BeADirectory())
		Expect(filepath.Join(dir, "data", "tables", "databases", "r1.db-wal")).NotTo(BeAnExistingFile())
		info, e := os.Stat(dir)
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o700)))
		for _, f := range []string{"manifest.json", "state/policy.json", "state/proposals.json", "data/note.txt"} {
			fi, e := os.Stat(filepath.Join(dir, f))
			Expect(e).NotTo(HaveOccurred(), f)
			Expect(fi.Mode().Perm()).To(Equal(os.FileMode(0o600)), f)
		}
		pol, e := os.ReadFile(filepath.Join(dir, "state", "policy.json"))
		Expect(e).NotTo(HaveOccurred())
		Expect(string(pol)).To(ContainSubstring(key.ResourceID))
		Expect(string(pol)).To(ContainSubstring(`"bob":"viewer"`))
		man, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
		Expect(e).NotTo(HaveOccurred())
		Expect(string(man)).To(And(ContainSubstring(`"sha256"`), ContainSubstring(`"sourceId": "library"`), ContainSubstring(`"mode": "named-user"`), ContainSubstring(`"sqlite": true`)))
		// The add-on is running again and nothing else was left behind.
		Expect(r.host.resumes).To(Equal(1))
		entries, _ := os.ReadDir(filepath.Join(r.root, "backups", addon))
		Expect(entries).To(HaveLen(1))
	})

	It("holds admission and invalidates pending proposals for the add-on while it copies", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(3)
		key := r.register("alice")
		var during error
		r.host.quiesceFn = func() error {
			// The process is stopping: a new admission is unavailable now.
			during = r.policy.Authorize(context.Background(), named("alice"), key, policy.ActionRead)
			return nil
		}
		_, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(during).To(MatchError(ContainSubstring("backed up or recovered")))
		// The hold is lifted afterwards.
		Expect(r.policy.Authorize(context.Background(), named("alice"), key, policy.ActionRead)).To(Succeed())
	})

	It("reports a corrupt backup and refuses to use it", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(5)
		rc, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		db := filepath.Join(r.root, "backups", addon, rc.ID, "data", "tables", "databases", "r1.db")
		f, e := os.OpenFile(db, os.O_WRONLY, 0)
		Expect(e).NotTo(HaveOccurred())
		_, e = f.WriteAt([]byte("garbage garbage garbage"), 100)
		Expect(e).NotTo(HaveOccurred())
		Expect(f.Close()).To(Succeed())
		_, e = r.rec.Verify(context.Background(), addon, rc.ID)
		Expect(e).To(MatchError(recovery.ErrBackupCorrupt))
		list, e := r.rec.List(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Verification).To(Equal("failed"))
		// A restore stops before it changes anything.
		r.host.set(func(i *recovery.Info) { i.Running = false })
		before, _ := os.ReadFile(filepath.Join(r.root, "data", addon, "note.txt"))
		e = r.rec.Restore(context.Background(), r.admin(), rc.ID, nil)
		Expect(e).To(MatchError(recovery.ErrBackupCorrupt))
		after, _ := os.ReadFile(filepath.Join(r.root, "data", addon, "note.txt"))
		Expect(after).To(Equal(before))
		Expect(r.rowsIn(r.dbPath())).To(Equal(5))
	})

	It("stops before copying when the storage is too small and leaves no partial backup", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(5)
		r.rec.SetFree(func(string) (uint64, error) { return 1024, nil })
		_, e := r.rec.Backup(context.Background(), addon)
		Expect(e).To(MatchError(recovery.ErrInsufficientStorage))
		entries, _ := os.ReadDir(filepath.Join(r.root, "backups", addon))
		Expect(entries).To(BeEmpty())
		// The add-on was never stopped for it... and runs.
		Expect(r.host.info.Running).To(BeTrue())
	})

	It("leaves no partial backup after a cancellation and starts the add-on again", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(5)
		ctx, cancel := context.WithCancel(context.Background())
		r.rec.SetFailpoint(func(phase string) error {
			if phase == "copy-file" {
				cancel()
			}
			return nil
		})
		_, e := r.rec.Backup(ctx, addon)
		Expect(e).To(MatchError(context.Canceled))
		entries, _ := os.ReadDir(filepath.Join(r.root, "backups", addon))
		Expect(entries).To(BeEmpty())
		Expect(r.host.resumes).To(Equal(1))
		Expect(r.host.info.Running).To(BeTrue())
	})

	It("starts the add-on again when the copy fails", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(5)
		r.rec.SetFailpoint(func(phase string) error {
			if phase == "before-publish" {
				return errors.New("disk failure")
			}
			return nil
		})
		_, e := r.rec.Backup(context.Background(), addon)
		Expect(e).To(MatchError(ContainSubstring("disk failure")))
		entries, _ := os.ReadDir(filepath.Join(r.root, "backups", addon))
		Expect(entries).To(BeEmpty())
		Expect(r.host.resumes).To(Equal(1))
	})

	It("keeps the only verified copy and the upgrade recovery copy under an explicit retention policy", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.retain = recovery.Retention{Keep: 1}
		r.newRecovery()
		r.seed(2)
		first, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		// The retention policy is count one: the newest verified stays and
		// the older one goes.
		r.tick()
		second, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		list, e := r.rec.List(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].ID).To(Equal(second.ID))
		Expect(first.ID).NotTo(Equal(second.ID))
		// The sole verified copy cannot be deleted by name.
		e = r.rec.DeleteBackup(context.Background(), r.admin(), addon, second.ID)
		Expect(e).To(MatchError(recovery.ErrSoleRecoveryCopy))
		// Only an administrator may delete at all.
		e = r.rec.DeleteBackup(context.Background(), named("alice"), addon, second.ID)
		Expect(e).To(MatchError(recovery.ErrForbidden))
		// Age alone never removes the newest verified backup either.
		r.retain = recovery.Retention{Keep: 1, MaxAge: time.Minute}
		r.newRecovery()
		r.now = r.now.Add(48 * time.Hour)
		third, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		list, _ = r.rec.List(context.Background(), addon)
		Expect(list).To(HaveLen(1))
		Expect(list[0].ID).To(Equal(third.ID))
	})

	It("never lets age or count remove the newest verified backup when retention is applied on demand", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.retain = recovery.Retention{Keep: 1, MaxAge: time.Hour}
		r.newRecovery()
		r.seed(2)
		only, e := r.rec.Backup(context.Background(), addon)
		Expect(e).NotTo(HaveOccurred())
		r.now = r.now.Add(30 * 24 * time.Hour)
		removed, e := r.rec.Prune(context.Background(), r.admin(), addon)
		Expect(e).NotTo(HaveOccurred())
		Expect(removed).To(BeEmpty())
		list, _ := r.rec.List(context.Background(), addon)
		Expect(list).To(HaveLen(1))
		Expect(list[0].ID).To(Equal(only.ID))
		_, e = r.rec.Prune(context.Background(), named("alice"), addon)
		Expect(e).To(MatchError(recovery.ErrForbidden))
	})
})

var _ = Describe("Backup of add-on state through the real policy", func() {
	It("refuses a backup when the policy cannot drain admitted work", func() {
		r := newRig(policy.ModeNamedUser, "admin", "alice")
		r.seed(1)
		key := r.register("alice")
		started := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- r.policy.Admit(context.Background(), named("alice"), key, policy.ActionRead, func(ctx context.Context) error {
				close(started)
				<-release
				return nil
			})
		}()
		<-started
		// The admitted work ignores cancellation, so the drain is bounded and
		// the backup does not go on.
		_, e := r.rec.Backup(context.Background(), addon)
		Expect(e).To(MatchError(policy.ErrDrainPending))
		Expect(r.host.quiesced).To(BeFalse())
		close(release)
		Expect(<-done).To(Succeed())
		_ = contract.Actor{}
	})
})
