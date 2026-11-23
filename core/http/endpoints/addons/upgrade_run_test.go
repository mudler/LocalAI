// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
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
