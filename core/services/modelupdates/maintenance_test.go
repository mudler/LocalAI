package modelupdates_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/modelupdates"
)

type removedNotifier struct {
	upgradeNotifier
	removed []string
}

func (n *removedNotifier) ModelRemovedOld(_ context.Context, key, name string, _ int) error {
	n.removed = append(n.removed, key+" "+name)
	return nil
}

var _ = Describe("The old version and the hold", func() {
	var (
		f   *fixture
		ctx context.Context
		u   *modelupdates.Upgrader
		n   *removedNotifier
	)

	sidecar := func() *gallery.ModelConfig {
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		return sc
	}
	setJournal := func(j *gallery.Journal) {
		sc := sidecar()
		sc.Upgrade = j
		Expect(gallery.WriteSidecar(f.dir, "tiny", sc)).To(Succeed())
	}

	BeforeEach(func() {
		f = newFixture()
		ctx = context.Background()
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v1")))
		f.install("tiny")
		n = &removedNotifier{}
		u = &modelupdates.Upgrader{SystemState: f.systemState, ListGallery: f.list, Runtime: newRuntime(), Notifier: n}
	})

	upgrade := func() string {
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		Expect(u.Upgrade(ctx, modelupdates.Request{OpID: "op-1", Name: "tiny"})).To(Succeed())
		retained := sidecar().Upgrade.RetainedFiles
		Expect(retained).To(HaveLen(1))
		return retained[0]
	}

	It("removes the old files of a confirmed upgrade once", func() {
		old := upgrade()
		removed, err := u.RemoveOld(ctx, "tiny")
		Expect(err).ToNot(HaveOccurred())
		Expect(removed).To(Equal([]string{old}))
		Expect(filepath.Join(f.dir, old)).ToNot(BeAnExistingFile())
		Expect(filepath.Join(f.dir, "tiny.gguf")).To(BeAnExistingFile())
		Expect(sidecar().Upgrade.RetainedFiles).To(BeEmpty())
		Expect(n.removed).To(Equal([]string{"op-1 tiny"}))

		_, err = u.RemoveOld(ctx, "tiny")
		Expect(err).To(MatchError(modelupdates.ErrNoOldVersion))
	})

	It("refuses without a confirmed upgrade, and while one runs", func() {
		_, err := u.RemoveOld(ctx, "tiny")
		Expect(err).To(MatchError(modelupdates.ErrNoOldVersion))
		setJournal(&gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseRolledBack, RetainedFiles: []string{"x"}})
		_, err = u.RemoveOld(ctx, "tiny")
		Expect(err).To(MatchError(modelupdates.ErrNoOldVersion))
		setJournal(&gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseLoading})
		_, err = u.RemoveOld(ctx, "tiny")
		Expect(err).To(MatchError(modelupdates.ErrBusy))
	})

	It("keeps an old file that another model uses now", func() {
		old := upgrade()
		Expect(os.WriteFile(filepath.Join(f.dir, "other.yaml"), []byte("name: other\nparameters:\n  model: "+old+"\n"), 0600)).To(Succeed())
		removed, err := u.RemoveOld(ctx, "tiny")
		Expect(err).To(MatchError(ContainSubstring("used by other")))
		Expect(removed).To(BeEmpty())
		Expect(filepath.Join(f.dir, old)).To(BeAnExistingFile())
		Expect(sidecar().Upgrade.RetainedFiles).To(Equal([]string{old}))
	})

	It("reports what it kept when it removed only some of the old files", func() {
		Expect(os.WriteFile(filepath.Join(f.dir, "a.prev"), []byte("a"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(f.dir, "b.prev"), []byte("b"), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(f.dir, "other.yaml"), []byte("name: other\nparameters:\n  model: b.prev\n"), 0600)).To(Succeed())
		setJournal(&gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseConfirmed, RetainedFiles: []string{"a.prev", "b.prev"}})
		res, err := u.RemoveOldVersion(ctx, "tiny")
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Removed).To(Equal([]string{"a.prev"}))
		Expect(res.Kept).To(Equal([]string{"b.prev"}))
		Expect(res.Errors).To(ConsistOf(ContainSubstring("b.prev is used by other")))
		Expect(res.Partial()).To(BeTrue())
		Expect(sidecar().Upgrade.RetainedFiles).To(Equal([]string{"b.prev"}))

		_, err = u.RemoveOldVersion(ctx, "tiny")
		Expect(errors.Is(err, modelupdates.ErrFilesKept)).To(BeTrue())
	})

	It("deletes the kept old files with the model", func() {
		old := upgrade()
		Expect(gallery.DeleteModelFromSystem(f.systemState, "tiny")).To(Succeed())
		Expect(filepath.Join(f.dir, old)).ToNot(BeAnExistingFile())
	})

	It("holds a model in the gallery file only, through a reinstall", func() {
		before, err := os.ReadFile(filepath.Join(f.dir, "tiny.yaml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(u.SetHold(ctx, "tiny", true)).To(Succeed())
		Expect(sidecar().Hold).To(BeTrue())
		after, err := os.ReadFile(filepath.Join(f.dir, "tiny.yaml"))
		Expect(err).ToNot(HaveOccurred())
		Expect(after).To(Equal(before), "the hold does not touch the model config")
		Expect(sidecar().ConfigSHA256).To(Equal(gallery.SHA256Hex(before)))

		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		svc := f.service(modelupdates.Options{})
		Expect(itemOf(svc.CheckNow(ctx), "tiny").State).To(Equal(modelupdates.StateHeld))

		f.install("tiny")
		Expect(sidecar().Hold).To(BeTrue(), "a reinstall keeps the hold")

		Expect(u.SetHold(ctx, "tiny", false)).To(Succeed())
		Expect(sidecar().Hold).To(BeFalse())
	})

	It("refreshes the digest notification at once when the hold changes", func() {
		n := &recordingNotifier{}
		svc := f.service(modelupdates.Options{Notifier: n})
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		svc.CheckNow(ctx)
		Expect(n.calls[len(n.calls)-1]).To(HaveLen(1))
		checks := n.count()

		var changed []string
		u.Changed = func(name string) {
			changed = append(changed, name)
			svc.Notify(ctx)
		}
		Expect(u.SetHold(ctx, "tiny", true)).To(Succeed())
		Expect(changed).To(Equal([]string{"tiny"}))
		Expect(n.count()).To(Equal(checks+1), "no gallery check, only the digest")
		Expect(n.calls[len(n.calls)-1]).To(BeEmpty(), "a held model needs nobody")

		Expect(u.SetHold(ctx, "tiny", true)).To(Succeed())
		Expect(changed).To(HaveLen(1), "no change, no refresh")

		Expect(u.SetHold(ctx, "tiny", false)).To(Succeed())
		Expect(n.calls[len(n.calls)-1]).To(HaveLen(1))
	})

	It("refuses to upgrade a held model, whoever asks", func() {
		Expect(u.SetHold(ctx, "tiny", true)).To(Succeed())
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		Expect(u.Validate("tiny", false)).To(MatchError(modelupdates.ErrHeld))
		Expect(modelupdates.CheckRequest(u, nil, "tiny", false, nil)).To(MatchError(modelupdates.ErrHeld))
		Expect(u.Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(MatchError(modelupdates.ErrHeld))
		Expect(sidecar().Upgrade).To(BeNil(), "a refusal leaves no journal")
		Expect(sidecar().Hold).To(BeTrue())
	})

	It("does not remove the old version while another frontend holds the model lock", func() {
		upgrade()
		locks := newClusterLocks()
		u.Lock = locks.lockFor("a")
		release, err := locks.lockFor("b")(ctx, "tiny")
		Expect(err).ToNot(HaveOccurred())
		_, err = u.RemoveOld(ctx, "tiny")
		Expect(err).To(MatchError(modelupdates.ErrBusy))
		Expect(sidecar().Upgrade.RetainedFiles).ToNot(BeEmpty())
		release()
		removed, err := u.RemoveOld(ctx, "tiny")
		Expect(err).ToNot(HaveOccurred())
		Expect(removed).ToNot(BeEmpty())
	})

	It("does not let a hold through while another frontend holds the model lock", func() {
		locks := newClusterLocks()
		u.Lock = locks.lockFor("a")
		release, err := locks.lockFor("b")(ctx, "tiny")
		Expect(err).ToNot(HaveOccurred())
		Expect(u.SetHold(ctx, "tiny", true)).To(MatchError(modelupdates.ErrBusy))
		Expect(sidecar().Hold).To(BeFalse())
		release()
		Expect(u.SetHold(ctx, "tiny", true)).To(Succeed())
		Expect(locks.heldBy("tiny")).To(BeEmpty())
	})

	It("refuses a hold on a model without an origin, and while an upgrade runs", func() {
		Expect(os.WriteFile(filepath.Join(f.dir, "old.yaml"), []byte("name: old\n"), 0600)).To(Succeed())
		Expect(os.WriteFile(gallery.SidecarPath(f.dir, "old"), []byte("name: old\n"), 0600)).To(Succeed())
		Expect(u.SetHold(ctx, "old", true)).To(MatchError(modelupdates.ErrNotUpgradable))
		Expect(u.SetHold(ctx, "none", true)).To(MatchError(modelupdates.ErrNotFound))
		setJournal(&gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseStaging})
		Expect(u.SetHold(ctx, "tiny", true)).To(MatchError(modelupdates.ErrBusy))
	})
})
