package upgraderun_test

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	. "github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/LocalAI/core/services/upgraderun/upgraderuntest"
)

// plantedSecrets are what a failing operation may carry and no reader of a
// run may see.
var plantedSecrets = []string{"user:pass", "abcdef0123456789", "abc-short", "/home/alice", "\n", "\x1b", "<script"}

// leakyError is the error of an operation that leaks a credential, a home
// path, terminal escapes, markup and many lines of output.
const leakyError = "pull https://user:pass@registry.example/v2/x failed: Bearer abcdef0123456789\n" +
	"retry with Bearer abc-short\n\x1b[31mopen /home/alice/.config/app/key.pem\x1b[0m <script>alert(1)</script>"

func expectSafe(s string) {
	GinkgoHelper()
	for _, secret := range plantedSecrets {
		Expect(s).NotTo(ContainSubstring(secret))
	}
	Expect(utf8.RuneCountInString(s)).To(BeNumerically("<=", MaxItemText))
}

func expectSafeItem(it *Item) {
	GinkgoHelper()
	Expect(it).NotTo(BeNil())
	expectSafe(it.Error)
	expectSafe(it.Message)
	for _, n := range it.Nodes {
		expectSafe(n.Message)
	}
}

var _ = Describe("The error and message of an item", func() {
	var (
		h   *upgraderuntest.Harness
		ctx context.Context
	)

	BeforeEach(func() {
		var err error
		h, err = upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Service.Close)
		ctx = context.Background()
		h.Backends.Add(Candidate{Kind: KindGallery, Name: "kokoro", To: "v2"})
	})

	ended := func(id string) Run {
		GinkgoHelper()
		Eventually(func() bool { r, _ := h.Service.Get(ctx, id); return r.Active() }, 5*time.Second).Should(BeFalse())
		r, err := h.Service.Get(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		return r
	}

	It("is safe when the start of the operation fails", func() {
		h.Starter.SetErr(errors.New(leakyError + strings.Repeat(" output", 400)))
		run, err := h.Service.Start(ctx, "admin", StartRequest{Items: []StartItem{{Kind: KindGallery, Name: "kokoro"}}})
		Expect(err).NotTo(HaveOccurred())
		r := ended(run.ID)
		expectSafeItem(r.Item("gallery:kokoro"))
		Expect(r.Item("gallery:kokoro").Error).To(ContainSubstring("key.pem"))
	})

	It("is safe while the operation runs and when it fails, per node too", func() {
		run, err := h.Service.Start(ctx, "admin", StartRequest{Items: []StartItem{{Kind: KindGallery, Name: "kokoro"}}})
		Expect(err).NotTo(HaveOccurred())
		var job string
		Eventually(func() string { job = h.Starter.Job("gallery:kokoro"); return job }, 5*time.Second).ShouldNot(BeEmpty())
		nodes := []galleryop.NodeProgress{{NodeID: "n1", NodeName: "worker-1", Status: "error", Error: leakyError}}
		h.Jobs.Set(job, galleryop.OpStatus{Phase: "fetching", Message: leakyError, Progress: 10, Cancellable: true, Nodes: nodes})
		Eventually(func() string { r, _ := h.Service.Get(ctx, run.ID); return r.Item("gallery:kokoro").Message }, 5*time.Second).Should(ContainSubstring("key.pem"))
		r, err := h.Service.Get(ctx, run.ID)
		Expect(err).NotTo(HaveOccurred())
		expectSafeItem(r.Item("gallery:kokoro"))

		h.Jobs.Set(job, galleryop.OpStatus{Phase: "fetching", Processed: true, Error: errors.New(leakyError + strings.Repeat(" output", 400)), Nodes: nodes})
		r = ended(run.ID)
		expectSafeItem(r.Item("gallery:kokoro"))
		Expect(r.Item("gallery:kokoro").Phase).To(Equal(PhaseFailed))
	})

	It("is safe when a stored row carries a raw error", func() {
		db, err := upgraderuntest.OpenSQLite(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		store, err := distributed.NewUpgradeRunStore(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(store.Create(&distributed.UpgradeRunRecord{ID: "old", Source: "manual", State: "finished", CreatedAt: time.Now()},
			[]distributed.UpgradeRunItemRecord{{Key: "gallery:a", Kind: "gallery", Name: "a", Phase: "failed",
				ErrorKey: "download", Error: leakyError, Message: leakyError,
				Nodes: `[{"id":"n1","phase":"error","message":"` + strings.ReplaceAll(strings.ReplaceAll(leakyError, "\n", `\n`), "\x1b", `\u001b`) + `"}]`}})).To(Succeed())
		svc, err := New(Options{Store: store, Jobs: upgraderuntest.NewJobs(), Instance: "t"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(svc.Close)
		r, err := svc.Get(ctx, "old")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Item("gallery:a").Nodes).To(HaveLen(1))
		expectSafeItem(r.Item("gallery:a"))
	})
})
