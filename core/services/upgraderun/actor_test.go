package upgraderun_test

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/LocalAI/core/services/upgraderun/upgraderuntest"
)

// actorChecker records the actor in the context of every listing.
type actorChecker struct {
	*upgraderuntest.Checker
	mu     sync.Mutex
	actors []string
}

func (c *actorChecker) List(ctx context.Context, fresh bool) (Listing, error) {
	c.mu.Lock()
	c.actors = append(c.actors, ActorFrom(ctx))
	c.mu.Unlock()
	return c.Checker.List(ctx, fresh)
}

func (c *actorChecker) seen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.actors...)
}

var _ = Describe("The actor of a run in the context of its checks", func() {
	It("reaches every check of a preview, a start, the runner and a retry", func() {
		addons := &actorChecker{Checker: upgraderuntest.NewChecker(KindAddon)}
		addons.Add(Candidate{Kind: KindAddon, Name: "notes", To: "d2"})
		h, err := upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{Addons: addons})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Service.Close)
		ctx := context.Background()

		_, err = h.Service.Preview(ctx, "alice", nil)
		Expect(err).NotTo(HaveOccurred())
		h.Starter.SetErr(&StartError{ErrorKey: "unavailable", Retryable: true})
		run, err := h.Service.Start(ctx, "alice", StartRequest{Items: []StartItem{{Kind: KindAddon, Name: "notes"}}})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bool { r, _ := h.Service.Get(ctx, run.ID); return r.Active() }, 5*time.Second).Should(BeFalse())
		r, err := h.Service.Get(ctx, run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Item("addon:notes").RetryEligible()).To(BeTrue())

		h.Starter.SetErr(nil)
		_, err = h.Service.Retry(context.Background(), "alice", run.ID)
		Expect(err).NotTo(HaveOccurred())
		// Preview, start, the precheck of the runner, and the retry.
		Expect(len(addons.seen())).To(BeNumerically(">=", 4))
		for _, a := range addons.seen() {
			Expect(a).To(Equal("alice"))
		}
	})

	It("runs a retry as the administrator who retries, never as the actor of the old run", func() {
		addons := &actorChecker{Checker: upgraderuntest.NewChecker(KindAddon)}
		addons.Add(Candidate{Kind: KindAddon, Name: "notes", To: "d2"})
		h, err := upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{Addons: addons})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Service.Close)
		ctx := context.Background()

		h.Starter.SetErr(&StartError{ErrorKey: "unavailable", Retryable: true})
		run, err := h.Service.Start(ctx, "alice", StartRequest{Items: []StartItem{{Kind: KindAddon, Name: "notes"}}})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bool { r, _ := h.Service.Get(ctx, run.ID); return r.Active() }, 5*time.Second).Should(BeFalse())
		before := len(h.Starter.Actors())
		checksBefore := len(addons.seen())

		h.Starter.SetErr(nil)
		again, err := h.Service.Retry(ctx, "bob", run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.Actor).To(Equal("bob"))
		Eventually(func() int { return len(h.Starter.Actors()) }, 5*time.Second).Should(BeNumerically(">", before))
		Expect(h.Starter.Actors()[before:]).To(HaveEach(Equal("bob")))
		Expect(addons.seen()[checksBefore:]).To(HaveEach(Equal("bob")))
	})

	It("makes the retry of an automatic run a manual run of the administrator who retries", func() {
		h, err := upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Service.Close)
		ctx := context.Background()
		h.Addons.Add(Candidate{Kind: KindAddon, Name: "notes", To: "d2"})

		h.Starter.SetErr(&StartError{ErrorKey: "unavailable", Retryable: true})
		run, err := h.Service.Start(ctx, "system:auto-upgrade", StartRequest{Source: SourceAuto, Items: []StartItem{{Kind: KindAddon, Name: "notes"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(run.Source).To(Equal(SourceAuto))
		Eventually(func() bool { r, _ := h.Service.Get(ctx, run.ID); return r.Active() }, 5*time.Second).Should(BeFalse())

		h.Starter.SetErr(nil)
		again, err := h.Service.Retry(ctx, "bob", run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.Source).To(Equal(SourceManual))
		Expect(again.Actor).To(Equal("bob"))
		stored, err := h.Service.Get(ctx, again.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Source).To(Equal(SourceManual))
		Expect(stored.Actor).To(Equal("bob"))
	})

	It("tells a starter the source of the run in the context", func() {
		Expect(SourceFrom(WithSource(context.Background(), SourceAuto))).To(Equal(SourceAuto))
		Expect(SourceFrom(context.Background())).To(Equal(Source("")))
	})

	It("says an item that updated or was skipped is not retried", func() {
		Expect((&Item{Phase: PhaseUpdated, Retryable: true}).RetryEligible()).To(BeFalse())
		Expect((&Item{Phase: PhaseFailed, Retryable: false}).RetryEligible()).To(BeFalse())
		Expect((&Item{Phase: PhaseUnconfirmed, Retryable: true, Verifying: true}).RetryEligible()).To(BeFalse())
		Expect((&Item{Phase: PhaseRolledBack, Retryable: true}).RetryEligible()).To(BeTrue())
	})
})
