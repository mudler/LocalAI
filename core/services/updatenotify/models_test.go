package updatenotify_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/notify"
	. "github.com/mudler/LocalAI/core/services/updatenotify"
)

var _ = Describe("the model notifications", func() {
	ctx := context.Background()

	It("sets the model digest in place and removes it when nothing is left", func() {
		f := newFrontend(memStore(), nil, "admin")
		Expect(f.em.ModelDigest(ctx, []ModelUpdate{{Name: "b", State: "update-available"}, {Name: "a", State: "edited"}})).To(Succeed())
		got := list(f, "admin")
		Expect(got).To(HaveLen(1))
		Expect(got[0].Code).To(Equal(CodeModelsUpdateAvailable))
		Expect(got[0].Key).To(Equal(KeyModelsDigest))
		Expect(got[0].Title).To(Equal("2 model updates available"))
		Expect(got[0].Body).To(Equal("a, b"))
		Expect(got[0].Params).To(HaveKeyWithValue("family", FamilyModels))

		Expect(f.em.ModelDigest(ctx, []ModelUpdate{{Name: "a", State: "edited"}})).To(Succeed())
		got = list(f, "admin")
		Expect(got).To(HaveLen(1), "one row, edited in place")
		Expect(got[0].Title).To(Equal("1 model update available"))

		Expect(f.em.ModelDigest(ctx, nil)).To(Succeed())
		Expect(list(f, "admin")).To(BeEmpty())
	})

	It("tells an upgrade result once per operation", func() {
		f := newFrontend(memStore(), nil, "admin")
		done := ModelUpgrade{OpID: "op-1", Name: "tiny", From: "sha256:a", To: "sha256:b"}
		Expect(f.em.ModelUpgradeResult(ctx, done)).To(Succeed())
		Expect(f.em.ModelUpgradeResult(ctx, done)).To(Succeed())
		got := list(f, "admin")
		Expect(got).To(HaveLen(1))
		Expect(got[0].Code).To(Equal(CodeModelUpgradeDone))
		Expect(got[0].Status).To(Equal(notify.StatusOK))

		Expect(f.em.ModelUpgradeResult(ctx, ModelUpgrade{OpID: "op-2", Name: "tiny", Failed: true, RolledBack: true, Err: "load\nfailed"})).To(Succeed())
		got = list(f, "admin")
		Expect(got).To(HaveLen(2))
		failed := got[0]
		if failed.Code != CodeModelUpgradeFailed {
			failed = got[1]
		}
		Expect(failed.Code).To(Equal(CodeModelUpgradeFailed))
		Expect(failed.Status).To(Equal(notify.StatusFailed))
		Expect(failed.Params).To(HaveKeyWithValue("rolledBack", true))
		Expect(failed.Params).To(HaveKeyWithValue("error", "load failed"))
	})

	It("leaves the row of a completed or failed model upgrade to the upgrader", func() {
		f := newFrontend(memStore(), nil, "admin")
		Expect(f.em.Operation(ctx, galleryop.OpResult{ID: "op-9", Name: "tiny", Upgrade: true, Outcome: galleryop.OpFailed, Err: "the model is up to date"})).To(Succeed())
		Expect(f.em.Operation(ctx, galleryop.OpResult{ID: "op-10", Name: "tiny", Upgrade: true, Outcome: galleryop.OpCompleted})).To(Succeed())
		Expect(list(f, "admin")).To(BeEmpty())
	})

	It("tells a backend upgrade as its operation", func() {
		f := newFrontend(memStore(), nil, "admin")
		Expect(f.em.Operation(ctx, galleryop.OpResult{ID: "op-b", Name: "llama-cpp", Backend: true, Upgrade: true, Outcome: galleryop.OpFailed, Err: "x"})).To(Succeed())
		Expect(list(f, "admin")).To(HaveLen(1))
	})

	It("writes one row for an upgrade whichever producer is late or fails", func() {
		f := newFrontend(memStore(), nil, "admin")
		Expect(f.em.Operation(ctx, galleryop.OpResult{ID: "op-9", Name: "tiny", Upgrade: true, Outcome: galleryop.OpFailed, Err: "load failed"})).To(Succeed())
		Expect(f.em.ModelUpgradeResult(ctx, ModelUpgrade{OpID: "op-9", Name: "tiny", Failed: true, RolledBack: true, Err: "load failed"})).To(Succeed())
		got := list(f, "admin")
		Expect(got).To(HaveLen(1))
		Expect(got[0].Code).To(Equal(CodeModelUpgradeFailed))
		Expect(got[0].Params).To(HaveKeyWithValue("rolledBack", true))
	})

	It("tells a cancelled model upgrade as cancelled, not failed", func() {
		f := newFrontend(memStore(), nil, "admin")
		Expect(f.em.Operation(ctx, galleryop.OpResult{ID: "op-9", Name: "tiny", Upgrade: true, Outcome: galleryop.OpCancelled})).To(Succeed())
		got := list(f, "admin")
		Expect(got).To(HaveLen(1))
		Expect(got[0].Params).To(HaveKeyWithValue("outcome", "cancelled"))
		Expect(got[0].Status).ToNot(Equal(notify.StatusFailed))
	})

	It("names files, not server paths, in the error of an item", func() {
		f := newFrontend(memStore(), nil, "admin")
		Expect(f.em.ModelUpgradeResult(ctx, ModelUpgrade{OpID: "op-p", Name: "tiny", Failed: true,
			Err: "open /var/lib/localai/models/tiny.gguf.next: no space left; see https://example.com/a/b"})).To(Succeed())
		Expect(f.em.Operation(ctx, galleryop.OpResult{ID: "op-q", Name: "tiny", Outcome: galleryop.OpFailed,
			Err: `rename C:\models\tiny.gguf: denied`})).To(Succeed())
		var bodies []string
		for _, n := range list(f, "admin") {
			bodies = append(bodies, n.Body)
		}
		Expect(bodies).To(ConsistOf(
			"open tiny.gguf.next: no space left; see https://example.com/a/b",
			"rename tiny.gguf: denied"))
	})

	It("tells a removal of old files", func() {
		f := newFrontend(memStore(), nil, "admin")
		Expect(f.em.ModelRemovedOld(ctx, "op-1", "tiny", 2)).To(Succeed())
		got := list(f, "admin")
		Expect(got).To(HaveLen(1))
		Expect(got[0].Code).To(Equal(CodeModelRemovedOld))
	})
})
