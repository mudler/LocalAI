// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	managerpkg "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/LocalAI/core/services/upgraderun/upgraderuntest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("add-on items of upgrade runs", Label("updates"), func() {
	var (
		s    *updatesStack
		gs   *galleryop.GalleryService
		up   *QueuedUpgrader
		h    *upgraderuntest.Harness
		e    *echo.Echo
		who  string
		path = "/api/addons/notes/upgrade"
	)
	upgradeBody := func(op, expected, candidate string) string {
		return fmt.Sprintf(`{"operationId":%q,"expectedDigest":%q,"candidateDigest":%q}`, op, expected, candidate)
	}
	alice := func(op string) func(*http.Request) { return humanTab(s.cookies["alice"], op) }

	BeforeEach(func() {
		s = newUpdatesStack()
		gs = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		qctx, stop := context.WithCancel(context.Background())
		DeferCleanup(stop)
		Expect(gs.Start(qctx, nil, nil)).To(Succeed())
		up = &QueuedUpgrader{
			Gateway: func() *gateway.Gateway { return s.gateway },
			Queue:   func() *galleryop.GalleryService { return gs },
		}
		var err error
		h, err = upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{
			Jobs:     gs,
			Starters: map[upgraderun.Kind]upgraderun.Starter{upgraderun.KindAddon: upgraderun.AddonStarter{Upgrader: up}},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Service.Close)

		e = echo.New()
		e.Use(auth.Middleware(s.db, s.cfg))
		// The principal of a run request, as the run routes compute it.
		e.POST("/principal", func(c echo.Context) error {
			who = RunPrincipal(c, s.db, s.mode)
			return c.NoContent(http.StatusNoContent)
		})
		group := e.Group("/api/addons", auth.RequireFeature(s.db, auth.FeatureAddons), auth.CSRFMiddleware(), BrowserProtection())
		mode := WithOwnership(s.mode)
		group.POST("/dispatch", Dispatch(s.gateway, s.db, mode))
		group.POST("/:id/upgrade", UpgradeAddon(s.gateway, s.db, mode, WithOperationQueue(gs, nil),
			WithUpgradeRuns(func() *upgraderun.Service { return h.Service }, up)))
	})

	principal := func(headers func(*http.Request)) string {
		GinkgoHelper()
		who = ""
		Expect(send(e, "POST", "/principal", "", headers).code).To(Equal(http.StatusNoContent))
		return who
	}

	// offer lists a new build of notes in the run's check and returns its
	// digest and the installed one.
	offer := func(requires catalog.Requirements) (string, string) {
		GinkgoHelper()
		before := s.installedDigest()
		next := s.publish("build-2", requires)
		Expect(send(s.frontends["owner"], "POST", "/api/addons/notes/update/check", `{"operationId":"c"}`, alice("c")).code).To(Equal(200))
		h.Addons.Add(upgraderun.Candidate{Kind: upgraderun.KindAddon, Name: "notes", From: before, To: next.Digest,
			Extra: map[string]string{upgraderun.ExtraExpectedDigest: before, upgraderun.ExtraCandidateDigest: next.Digest}})
		return before, next.Digest
	}

	waitItem := func(id string) upgraderun.Item {
		GinkgoHelper()
		var it upgraderun.Item
		Eventually(func() bool {
			run, err := h.Service.Get(context.Background(), id)
			Expect(err).NotTo(HaveOccurred())
			it = *run.Item("addon:notes")
			return it.JobID != "" || it.Phase.Terminal()
		}, 10*time.Second, 20*time.Millisecond).Should(BeTrue())
		return it
	}

	It("takes the actor of a run from the administrator's own browser session, and nothing else", func() {
		human := principal(alice(RunConfirmID))
		a, ok, decoded := DecodeRunActor(human)
		Expect(decoded).To(BeTrue())
		Expect(ok).To(BeTrue())
		Expect(a.UserID).To(Equal("alice"))
		Expect(RunActorUser(human)).To(Equal("alice"))

		// The same session without the confirmation header, or with another
		// value, is not the browser surface.
		_, ok, decoded = DecodeRunActor(principal(with(alice(RunConfirmID), ConfirmHeader, "other")))
		Expect(decoded && !ok).To(BeTrue())
		// A key is never the browser surface.
		_, ok, decoded = DecodeRunActor(principal(bearerOf(s.tokens["alice"])))
		Expect(decoded && !ok).To(BeTrue())
		// A user who is not an add-on administrator has no add-on identity.
		_, _, decoded = DecodeRunActor(principal(bearerOf(s.tokens["carol"])))
		Expect(decoded).To(BeFalse())
	})

	It("upgrades an add-on item as the actor of the run, through the owner's queue", func() {
		_, candidate := offer(catalog.Requirements{})
		run, err := h.Service.Start(context.Background(), principal(alice(RunConfirmID)),
			upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindAddon, Name: "notes", ExpectedTo: candidate}}})
		Expect(err).NotTo(HaveOccurred())
		it := waitItem(run.ID)
		Expect(it.JobID).NotTo(BeEmpty(), it.Error)
		Eventually(func() bool {
			st := gs.GetStatus(it.JobID)
			return st != nil && st.Processed && st.Error == nil && st.Upgrade
		}, 10*time.Second).Should(BeTrue())
		Expect(s.generation()).To(Equal(uint64(2)))
		Expect(s.installedDigest()).To(Equal(candidate))
	})

	It("skips an add-on item of a run that did not come from the browser surface, and records nothing", func() {
		_, candidate := offer(catalog.Requirements{})
		run, err := h.Service.Start(context.Background(), principal(bearerOf(s.tokens["alice"])),
			upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindAddon, Name: "notes", ExpectedTo: candidate}}})
		Expect(err).NotTo(HaveOccurred())
		it := waitItem(run.ID)
		Expect(it.Phase).To(Equal(upgraderun.PhaseSkipped))
		Expect(it.ErrorKey).To(Equal(RunErrNotHumanSurface))
		Expect(it.JobID).To(BeEmpty())
		Expect(s.generation()).To(Equal(uint64(1)))
	})

	It("skips an add-on item of a run whose actor has no add-on identity", func() {
		_, candidate := offer(catalog.Requirements{})
		run, err := h.Service.Start(context.Background(), "carol",
			upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindAddon, Name: "notes", ExpectedTo: candidate}}})
		Expect(err).NotTo(HaveOccurred())
		it := waitItem(run.ID)
		Expect(it.Phase).To(Equal(upgraderun.PhaseSkipped))
		Expect(it.ErrorKey).To(Equal(RunErrForbidden))
		Expect(s.generation()).To(Equal(uint64(1)))
	})

	It("never sends the system actor of the automatic update through the gateway", func() {
		_, candidate := offer(catalog.Requirements{})
		run, err := h.Service.Start(context.Background(), managerpkg.AutoUpgradeActor.UserID,
			upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindAddon, Name: "notes", ExpectedTo: candidate}}})
		Expect(err).NotTo(HaveOccurred())
		it := waitItem(run.ID)
		Expect(it.Phase).To(Equal(upgraderun.PhaseSkipped))
		Expect(it.ErrorKey).To(Equal(RunErrForbidden))
		Expect(s.generation()).To(Equal(uint64(1)))

		_, err = GatewayUpdateChecker{Gateway: func() *gateway.Gateway { return s.gateway }}.
			List(upgraderun.WithActor(context.Background(), managerpkg.AutoUpgradeActor.UserID), false)
		Expect(err).To(MatchError(ContainSubstring("no add-on identity")))
	})

	It("fails an add-on item the owner refuses with the owner's type, as the explicit upgrade does", func() {
		_, candidate := offer(catalog.Requirements{SDK: contract.VersionRange{Min: 4, Max: 4}})
		run, err := h.Service.Start(context.Background(), principal(alice(RunConfirmID)),
			upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindAddon, Name: "notes", ExpectedTo: candidate}}})
		Expect(err).NotTo(HaveOccurred())
		it := waitItem(run.ID)
		Expect(it.Phase).To(Equal(upgraderun.PhaseFailed))
		Expect(it.ErrorKey).To(Equal("incompatible"))
		Expect(it.JobID).To(BeEmpty())
		Expect(s.generation()).To(Equal(uint64(1)))
	})

	It("lets the upgrade route start a run of one item with the caller's operation, and refuses it while another run runs", func() {
		before, candidate := offer(catalog.Requirements{})
		// Another run holds the server.
		h.Backends.Add(upgraderun.Candidate{Kind: upgraderun.KindGallery, Name: "kokoro"})
		other, err := h.Service.Start(context.Background(), "alice", upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindGallery, Name: "kokoro"}}})
		Expect(err).NotTo(HaveOccurred())
		busy := send(e, "POST", path, upgradeBody("r-1", before, candidate), alice("r-1"))
		Expect(busy.code).To(Equal(http.StatusConflict), fmt.Sprint(busy.body))
		Expect(busy.errType()).To(Equal(upgraderun.CodeRunActive))
		// The refused upgrade is dropped on the owner, so the add-on is free.
		Expect(h.Service.Cancel(context.Background(), other.ID)).To(Succeed())
		Eventually(func() bool {
			r, _ := h.Service.Get(context.Background(), other.ID)
			if it := r.Item("gallery:kokoro"); it != nil && it.JobID != "" {
				h.Jobs.Complete(it.JobID)
			}
			return r.Active()
		}, 10*time.Second, 20*time.Millisecond).Should(BeFalse())

		done := send(e, "POST", path, upgradeBody("r-2", before, candidate), alice("r-2"))
		Expect(done.code).To(Equal(http.StatusOK), fmt.Sprint(done.body))
		payload := done.body["payload"].(map[string]any)
		Expect(payload).To(HaveKeyWithValue("Phase", "accepted"))
		Expect(payload).To(HaveKeyWithValue("OperationID", "r-2"))
		job, _ := payload["jobId"].(string)
		Expect(job).NotTo(BeEmpty())
		runView := payload["run"].(map[string]any)
		Expect(runView["actor"]).NotTo(ContainSubstring("session"), "the credential reference stays on the server")
		// The run ran the caller's recorded operation: its status reads under
		// the caller's operation ID.
		Expect(awaitLifecycle(e, "notes", "r-2", alice("r-2"))).To(HaveKeyWithValue("Phase", "completed"))
		Expect(s.generation()).To(Equal(uint64(2)))
	})
})

// gatedStarter holds the start of an item until its gate is closed.
type gatedStarter struct {
	inner upgraderun.Starter
	gate  chan struct{}
}

func (g gatedStarter) Start(ctx context.Context, actor string, it upgraderun.Item) (string, error) {
	select {
	case <-g.gate:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return g.inner.Start(ctx, actor, it)
}

var _ = Describe("the add-on upgrade route when its run is slow to start", Label("updates"), func() {
	It("answers 202 with the run and no job, and the run later runs the caller's recorded operation", func() {
		s := newUpdatesStack()
		gs := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		qctx, stop := context.WithCancel(context.Background())
		DeferCleanup(stop)
		Expect(gs.Start(qctx, nil, nil)).To(Succeed())
		up := &QueuedUpgrader{Gateway: func() *gateway.Gateway { return s.gateway }, Queue: func() *galleryop.GalleryService { return gs }}
		gate := make(chan struct{})
		h, err := upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{
			Jobs:     gs,
			Starters: map[upgraderun.Kind]upgraderun.Starter{upgraderun.KindAddon: gatedStarter{inner: upgraderun.AddonStarter{Upgrader: up}, gate: gate}},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Service.Close)
		old := runStartWait
		runStartWait = 200 * time.Millisecond
		DeferCleanup(func() { runStartWait = old })

		e := echo.New()
		e.Use(auth.Middleware(s.db, s.cfg))
		group := e.Group("/api/addons", auth.RequireFeature(s.db, auth.FeatureAddons), auth.CSRFMiddleware(), BrowserProtection())
		group.POST("/dispatch", Dispatch(s.gateway, s.db, WithOwnership(s.mode)))
		group.POST("/:id/upgrade", UpgradeAddon(s.gateway, s.db, WithOwnership(s.mode), WithOperationQueue(gs, nil),
			WithUpgradeRuns(func() *upgraderun.Service { return h.Service }, up)))
		alice := func(op string) func(*http.Request) { return humanTab(s.cookies["alice"], op) }

		before := s.installedDigest()
		next := s.publish("build-2", catalog.Requirements{})
		Expect(send(s.frontends["owner"], "POST", "/api/addons/notes/update/check", `{"operationId":"c"}`, alice("c")).code).To(Equal(200))
		h.Addons.Add(upgraderun.Candidate{Kind: upgraderun.KindAddon, Name: "notes", From: before, To: next.Digest,
			Extra: map[string]string{upgraderun.ExtraExpectedDigest: before, upgraderun.ExtraCandidateDigest: next.Digest}})

		res := send(e, "POST", "/api/addons/notes/upgrade",
			fmt.Sprintf(`{"operationId":"p-1","expectedDigest":%q,"candidateDigest":%q}`, before, next.Digest), alice("p-1"))
		Expect(res.code).To(Equal(http.StatusAccepted), fmt.Sprint(res.body))
		payload := res.body["payload"].(map[string]any)
		Expect(payload).NotTo(HaveKey("jobId"))
		Expect(payload).To(HaveKeyWithValue("Phase", "accepted"))
		runView := payload["run"].(map[string]any)
		Expect(payload).To(HaveKeyWithValue("runId", runView["id"]))

		close(gate)
		Expect(awaitLifecycle(e, "notes", "p-1", alice("p-1"))).To(HaveKeyWithValue("Phase", "completed"))
		Expect(s.generation()).To(Equal(uint64(2)))
	})
})

// failingStarter refuses every start, after its gate opens.
type failingStarter struct{ gate chan struct{} }

func (f failingStarter) Start(ctx context.Context, _ string, _ upgraderun.Item) (string, error) {
	select {
	case <-f.gate:
	case <-ctx.Done():
	}
	return "", &upgraderun.StartError{ErrorKey: upgraderun.ErrKeyUnavailable, Retryable: true, Err: fmt.Errorf("the owner did not answer")}
}

var _ = Describe("the operation an add-on upgrade route recorded for its run", Label("updates"), func() {
	var (
		s     *updatesStack
		gs    *galleryop.GalleryService
		e     *echo.Echo
		route *QueuedUpgrader
		alice func(string) func(*http.Request)
	)

	// stack serves the upgrade route with route as its upgrader, and runs the
	// add-on items with starter.
	stack := func(starter upgraderun.Starter) *upgraderuntest.Harness {
		h, err := upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{
			Jobs: gs, Starters: map[upgraderun.Kind]upgraderun.Starter{upgraderun.KindAddon: starter},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Service.Close)
		h.Service.AddObserver(route)
		e = echo.New()
		e.Use(auth.Middleware(s.db, s.cfg))
		group := e.Group("/api/addons", auth.RequireFeature(s.db, auth.FeatureAddons), auth.CSRFMiddleware(), BrowserProtection())
		group.POST("/dispatch", Dispatch(s.gateway, s.db, WithOwnership(s.mode)))
		group.POST("/:id/upgrade", UpgradeAddon(s.gateway, s.db, WithOwnership(s.mode), WithOperationQueue(gs, nil),
			WithUpgradeRuns(func() *upgraderun.Service { return h.Service }, route)))
		return h
	}
	offer := func(h *upgraderuntest.Harness) (string, string) {
		before := s.installedDigest()
		next := s.publish("build-2", catalog.Requirements{})
		Expect(send(s.frontends["owner"], "POST", "/api/addons/notes/update/check", `{"operationId":"c"}`, alice("c")).code).To(Equal(200))
		h.Addons.Add(upgraderun.Candidate{Kind: upgraderun.KindAddon, Name: "notes", From: before, To: next.Digest,
			Extra: map[string]string{upgraderun.ExtraExpectedDigest: before, upgraderun.ExtraCandidateDigest: next.Digest}})
		return before, next.Digest
	}
	body := func(op, before, candidate string) string {
		return fmt.Sprintf(`{"operationId":%q,"expectedDigest":%q,"candidateDigest":%q}`, op, before, candidate)
	}

	BeforeEach(func() {
		s = newUpdatesStack()
		gs = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		qctx, stop := context.WithCancel(context.Background())
		DeferCleanup(stop)
		Expect(gs.Start(qctx, nil, nil)).To(Succeed())
		route = &QueuedUpgrader{Gateway: func() *gateway.Gateway { return s.gateway }, Queue: func() *galleryop.GalleryService { return gs }}
		alice = func(op string) func(*http.Request) { return humanTab(s.cookies["alice"], op) }
		old := runStartWait
		runStartWait = 200 * time.Millisecond
		DeferCleanup(func() { runStartWait = old })
	})

	It("is the one another frontend runs when it takes the run over", func() {
		// The item starts on another process: its upgrader is not the one
		// the route handed the operation to.
		other := &QueuedUpgrader{Gateway: func() *gateway.Gateway { return s.gateway }, Queue: func() *galleryop.GalleryService { return gs }}
		gate := make(chan struct{})
		h := stack(gatedStarter{inner: upgraderun.AddonStarter{Upgrader: other}, gate: gate})
		before, candidate := offer(h)
		res := send(e, "POST", "/api/addons/notes/upgrade", body("a-1", before, candidate), alice("a-1"))
		Expect(res.code).To(Equal(http.StatusAccepted), fmt.Sprint(res.body))
		runID := res.body["payload"].(map[string]any)["runId"].(string)
		close(gate)
		Eventually(func() string {
			r, err := h.Service.Get(context.Background(), runID)
			Expect(err).NotTo(HaveOccurred())
			it := r.Item("addon:notes")
			if it.JobID == "" {
				Expect(it.Phase.Terminal()).To(BeFalse(), it.Error)
			}
			return it.JobID
		}, 10*time.Second, 20*time.Millisecond).ShouldNot(BeEmpty())
		Expect(awaitLifecycle(e, "notes", "a-1", alice("a-1"))).To(HaveKeyWithValue("Phase", "completed"))
		Expect(s.generation()).To(Equal(uint64(2)))
	})

	It("is dropped on the owner when the item ends without starting it, so the add-on is free again", func() {
		gate := make(chan struct{})
		h := stack(failingStarter{gate: gate})
		before, candidate := offer(h)
		res := send(e, "POST", "/api/addons/notes/upgrade", body("f-1", before, candidate), alice("f-1"))
		Expect(res.code).To(Equal(http.StatusAccepted), fmt.Sprint(res.body))
		close(gate)
		Expect(awaitLifecycle(e, "notes", "f-1", alice("f-1"))).To(HaveKeyWithValue("Phase", "failed"))
		// A new upgrade of the same transition is accepted, not refused as a
		// conflict with the dropped one.
		Eventually(func() int {
			r, err := h.Service.List(context.Background(), upgraderun.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
			for _, run := range r {
				if run.Active() {
					return 1
				}
			}
			return 0
		}, 5*time.Second).Should(BeZero())
		again := send(e, "POST", "/api/addons/notes/upgrade", body("f-2", before, candidate), alice("f-2"))
		Expect(again.errType()).NotTo(Equal("upgrade_conflict"), fmt.Sprint(again.body))
	})
})

var _ = Describe("add-on items of upgrade runs on a frontend that is not the owner", Label("updates"), func() {
	var (
		s     *updatesStack
		gs    *galleryop.GalleryService
		front *gateway.Gateway
		owner *httptest.Server
		h     *upgraderuntest.Harness
		actor string
	)
	alice := func(op string) func(*http.Request) { return humanTab(s.cookies["alice"], op) }

	BeforeEach(func() {
		s = newUpdatesStack()
		front, _, owner = s.forwardingGateways(s.gateway)
		gs = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		qctx, stop := context.WithCancel(context.Background())
		DeferCleanup(stop)
		Expect(gs.Start(qctx, nil, nil)).To(Succeed())
		// The frontend's own run service: its add-on check and add-on job path
		// both reach the owner through the frontend's gateway.
		up := &QueuedUpgrader{Gateway: func() *gateway.Gateway { return front }, Queue: func() *galleryop.GalleryService { return gs }}
		var err error
		h, err = upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{
			Jobs:     gs,
			Addons:   GatewayUpdateChecker{Gateway: func() *gateway.Gateway { return front }},
			Starters: map[upgraderun.Kind]upgraderun.Starter{upgraderun.KindAddon: upgraderun.AddonStarter{Upgrader: up}},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Service.Close)

		e := echo.New()
		e.Use(auth.Middleware(s.db, s.cfg))
		e.POST("/principal", func(c echo.Context) error {
			actor = RunPrincipal(c, s.db, s.mode)
			return c.NoContent(http.StatusNoContent)
		})
		Expect(send(e, "POST", "/principal", "", alice(RunConfirmID)).code).To(Equal(http.StatusNoContent))
	})

	It("lists the add-on updates, previews and starts an add-on item as the actor of the run", func() {
		ctx := context.Background()
		// Nothing to update yet: the add-on is up to date, and the listing
		// is complete.
		plan, err := h.Service.Preview(ctx, actor, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Complete).To(BeTrue())
		Expect(plan.Items).To(BeEmpty())

		before := s.installedDigest()
		next := s.publish("build-2", catalog.Requirements{})
		Expect(send(s.frontends["owner"], "POST", "/api/addons/notes/update/check", `{"operationId":"c"}`, alice("c")).code).To(Equal(200))

		plan, err = h.Service.Preview(ctx, actor, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Items).To(HaveLen(1))
		item := plan.Items[0]
		Expect(item.Key).To(Equal("addon:notes"))
		// A person reads versions; the request pins the digest.
		Expect(item.From).To(Equal("1.0.0-dev"))
		Expect(item.To).To(Equal(next.Version))
		Expect(item.Title).NotTo(BeEmpty())
		Expect(item.ExpectedTo).To(Equal(next.Digest))
		Expect(item.ExpectedTo).NotTo(Equal(before))
		Expect(item.Startable).To(BeTrue())

		// A run with no add-on identity cannot list add-ons: the kind is
		// unchecked, never empty.
		_, err = h.Service.Start(ctx, "carol", upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindAddon, Name: "notes", ExpectedTo: next.Digest}}})
		Expect(upgraderun.CodeOf(err)).To(Equal(upgraderun.CodeSourceUnchecked))

		run, err := h.Service.Start(ctx, actor, upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindAddon, Name: "notes", ExpectedTo: next.Digest}}})
		Expect(err).NotTo(HaveOccurred())
		var it upgraderun.Item
		Eventually(func() string {
			r, err := h.Service.Get(ctx, run.ID)
			Expect(err).NotTo(HaveOccurred())
			it = *r.Item("addon:notes")
			return it.JobID
		}, 10*time.Second, 20*time.Millisecond).ShouldNot(BeEmpty(), it.Error)
		Eventually(func() bool {
			st := gs.GetStatus(it.JobID)
			return st != nil && st.Processed && st.Error == nil
		}, 10*time.Second).Should(BeTrue())
		Expect(s.generation()).To(Equal(uint64(2)))
		Expect(s.installedDigest()).To(Equal(next.Digest))
	})

	It("fails an add-on item with a typed reason when the owner cannot be reached", func() {
		ctx := context.Background()
		next := s.publish("build-2", catalog.Requirements{})
		Expect(send(s.frontends["owner"], "POST", "/api/addons/notes/update/check", `{"operationId":"c"}`, alice("c")).code).To(Equal(200))
		// A backend item keeps the run in its first stage while the owner
		// goes away; the add-on item starts after it.
		h.Backends.Add(upgraderun.Candidate{Kind: upgraderun.KindGallery, Name: "kokoro"})
		run, err := h.Service.Start(ctx, actor, upgraderun.StartRequest{Items: []upgraderun.StartItem{
			{Kind: upgraderun.KindGallery, Name: "kokoro"},
			{Kind: upgraderun.KindAddon, Name: "notes", ExpectedTo: next.Digest},
		}})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() string { return h.Starter.Job("gallery:kokoro") }, 5*time.Second).ShouldNot(BeEmpty())
		owner.Close()
		// The backend job is in the fake table, which the run does not read
		// (it reads the gallery service): its outcome is lost, and a check
		// decides it. Either way the stage ends and the add-on item starts.
		var it upgraderun.Item
		Eventually(func() bool {
			r, err := h.Service.Get(ctx, run.ID)
			Expect(err).NotTo(HaveOccurred())
			it = *r.Item("addon:notes")
			return it.Phase.Terminal()
		}, 20*time.Second, 50*time.Millisecond).Should(BeTrue())
		Expect(it.Phase).To(Equal(upgraderun.PhaseFailed))
		Expect(it.ErrorKey).To(Equal(RunErrUnavailable))
		Expect(it.Retryable).To(BeTrue())
		Expect(it.JobID).To(BeEmpty())
	})
})

// refusingStarter starts no job: it returns the error the add-on starter
// gives for a typed refusal of the owner.
type refusingStarter struct{ failure gateway.UpdateFailure }

func (r refusingStarter) Start(context.Context, string, upgraderun.Item) (string, error) {
	return "", ownerRefusal(&r.failure)
}

var _ = Describe("an add-on item the owner refuses", Label("updates"), func() {
	DescribeTable("is retryable exactly when the retry table says so",
		func(kind string, retryable bool) {
			h, err := upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{
				Starters: map[upgraderun.Kind]upgraderun.Starter{
					upgraderun.KindAddon: refusingStarter{gateway.UpdateFailure{Type: kind, Message: "refused"}},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(h.Service.Close)
			h.Addons.Add(upgraderun.Candidate{Kind: upgraderun.KindAddon, Name: "notes", From: "1", To: "2"})
			run, err := h.Service.Start(context.Background(), "admin",
				upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindAddon, Name: "notes", ExpectedTo: "2"}}})
			Expect(err).NotTo(HaveOccurred())
			var it upgraderun.Item
			Eventually(func() bool {
				r, err := h.Service.Get(context.Background(), run.ID)
				Expect(err).NotTo(HaveOccurred())
				it = *r.Item("addon:notes")
				return it.Phase.Terminal()
			}, 10*time.Second, 20*time.Millisecond).Should(BeTrue())
			Expect(it.Phase).To(Equal(upgraderun.PhaseFailed))
			want := kind
			if want == "" {
				want = upgraderun.ErrKeyUnknown
			}
			Expect(it.ErrorKey).To(Equal(want))
			Expect(it.Retryable).To(Equal(retryable))
			Expect(it.Retryable).To(Equal(upgraderun.RetryableKey(kind)), "the item agrees with the retry table")
		},
		Entry("the owner is unavailable", upgraderun.ErrKeyUnavailable, true),
		Entry("the upgrade failed and the installed version is kept", upgraderun.ErrKeyUpgradeFailed, true),
		Entry("the upgrade was interrupted", upgraderun.ErrKeyInterrupted, true),
		Entry("a refusal with no type", "", true),
		Entry("the installed version moved", upgraderun.ErrKeyStaleInstalled, true),
		Entry("the check failed", upgraderun.ErrKeyCheckFailed, true),
		Entry("an incompatible release", "incompatible", false),
		Entry("the add-on needs a recovery", "recovery_required", false),
		Entry("an integrity error", "integrity_error", false),
		Entry("an upgrade conflict", "upgrade_conflict", false),
		Entry("an approval is required", "approval_required", false),
		Entry("the account is forbidden", upgraderun.ErrKeyForbidden, false),
	)
})
