package upgraderun_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/galleryop"
	. "github.com/mudler/LocalAI/core/services/upgraderun"
)

var _ = Describe("A release that did not start", func() {
	var h *harness
	ctx := context.Background()
	auto := func(items ...StartItem) (Run, error) {
		return h.svc.Start(ctx, "system", StartRequest{Source: SourceAuto, Items: items})
	}
	// ends runs one manual run of kokoro at target 2 and ends its operation
	// with st.
	ends := func(st galleryop.OpStatus) {
		GinkgoHelper()
		r := h.start(si(KindGallery, "kokoro"))
		h.jobs.set(h.jobOf("gallery:kokoro"), st)
		Eventually(func() RunState { return h.state(r.ID) }).ShouldNot(Equal(RunRunning))
	}
	rolledBack := galleryop.OpStatus{Processed: true, Error: errors.New("exit code 3"), ErrorKey: "start", RolledBack: true}

	BeforeEach(func() {
		h = newHarness(harnessOpts{})
		h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro", To: "2"})
	})

	It("is not tried again by an automatic run while the gallery lists the same release", func() {
		ends(rolledBack)
		_, err := auto(StartItem{Kind: KindGallery, Name: "kokoro"})
		Expect(CodeOf(err)).To(Equal(CodeHeldBack))
		held, err := h.svc.HeldBack(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held.Holds("gallery:kokoro", "2")).To(BeTrue())
		Expect(held.Holds("gallery:kokoro", "3")).To(BeFalse())
	})

	It("leaves it out of an automatic run that has other items", func() {
		ends(rolledBack)
		h.backends.add(Candidate{Kind: KindGallery, Name: "piper", To: "2"})
		r, err := auto(StartItem{Kind: KindGallery, Name: "kokoro"}, StartItem{Kind: KindGallery, Name: "piper"})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Item("gallery:kokoro")).To(BeNil())
		Expect(r.Item("gallery:piper")).NotTo(BeNil())
	})

	It("is tried again once the gallery lists another release", func() {
		ends(rolledBack)
		h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro", To: "3"})
		r, err := auto(StartItem{Kind: KindGallery, Name: "kokoro"})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Item("gallery:kokoro")).NotTo(BeNil())
	})

	It("never blocks a manual run", func() {
		ends(rolledBack)
		r := h.start(si(KindGallery, "kokoro"))
		Expect(r.Item("gallery:kokoro")).NotTo(BeNil())
	})

	It("holds back a release that failed its integrity check too", func() {
		ends(galleryop.OpStatus{Processed: true, Error: errors.New("bad signature"), ErrorKey: "verify"})
		_, err := auto(StartItem{Kind: KindGallery, Name: "kokoro"})
		Expect(CodeOf(err)).To(Equal(CodeHeldBack))
	})

	It("tries again after a failure that a new try can fix", func() {
		for _, key := range []string{"download", "unavailable", "unknown", "interrupted"} {
			By(key)
			h = newHarness(harnessOpts{})
			h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro", To: "2"})
			ends(galleryop.OpStatus{Processed: true, Error: errors.New("connection reset"), ErrorKey: key})
			r, err := auto(StartItem{Kind: KindGallery, Name: "kokoro"})
			Expect(err).NotTo(HaveOccurred(), key)
			Expect(r.Item("gallery:kokoro")).NotTo(BeNil(), key)
		}
	})

	It("marks the held-back items of the backend read model", func() {
		ends(rolledBack)
		held, err := h.svc.HeldBack(ctx)
		Expect(err).NotTo(HaveOccurred())
		snap := backendupdates.Snapshot{Items: []backendupdates.Item{
			{ID: "gallery:kokoro", Kind: backendupdates.KindGallery, Name: "kokoro", AvailableVersion: "2"},
			{ID: "gallery:piper", Kind: backendupdates.KindGallery, Name: "piper", AvailableVersion: "2"},
		}}
		MarkHeldBack(&snap, held)
		Expect(snap.Items[0].HeldBack).To(BeTrue())
		Expect(snap.Items[1].HeldBack).To(BeFalse())
	})
})
