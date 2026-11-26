package inproc

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/LocalAI/core/services/upgraderun/upgraderuntest"
)

var _ = Describe("Upgrade runs through the in-process client", func() {
	var (
		h *upgraderuntest.Harness
		c *Client
	)

	BeforeEach(func() {
		var err error
		h, err = upgraderuntest.NewHarness(GinkgoT().TempDir(), upgraderuntest.Options{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(h.Service.Close)
		c = &Client{AppConfig: &config.ApplicationConfig{}, UpgradeRunService: func() *upgraderun.Service { return h.Service }}
		h.Backends.Add(upgraderun.Candidate{Kind: upgraderun.KindGallery, Name: "kokoro", To: "v2"})
		h.Addons.Add(upgraderun.Candidate{Kind: upgraderun.KindAddon, Name: "notes", To: "sha256:b"})
	})

	It("starts a run as the acting user, reads it and cancels it", func() {
		ctx := localaitools.WithUser(context.Background(), &auth.User{ID: "admin-1", Role: auth.RoleAdmin})
		run, err := c.StartUpgradeRun(ctx, localaitools.UpgradeRunStartRequest{Items: []localaitools.UpgradeRunItemRef{{Kind: "gallery", Name: "kokoro", ExpectedTo: "v2"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(run.State).To(Equal("running"))
		Expect(run.Items).To(HaveLen(1))
		Eventually(h.Starter.Actors).Should(Equal([]string{"admin-1"}))

		runs, err := c.UpgradeRuns(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(1))
		runs, err = c.UpgradeRuns(ctx, run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(runs[0].ID).To(Equal(run.ID))

		Eventually(func() bool {
			r, _ := h.Service.Get(ctx, run.ID)
			return r.Items[0].Cancellable
		}, 5*time.Second, 10*time.Millisecond).Should(BeTrue())
		_, err = c.CancelUpgradeRun(ctx, run.ID, "gallery:kokoro")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() string {
			r, _ := c.UpgradeRuns(ctx, run.ID)
			return r[0].Items[0].Phase
		}, 5*time.Second, 10*time.Millisecond).Should(Equal("cancelled"))
	})

	It("refuses an add-on item and starts nothing", func() {
		_, err := c.StartUpgradeRun(context.Background(), localaitools.UpgradeRunStartRequest{Items: []localaitools.UpgradeRunItemRef{
			{Kind: "gallery", Name: "kokoro"}, {Kind: "addon", Name: "notes"},
		}})
		Expect(err).To(MatchError(ContainSubstring("add-ons are not updated by the assistant")))
		runs, err := h.Service.List(context.Background(), upgraderun.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(BeEmpty())
	})

	It("upgrades one backend as a run of one item, and refuses it while another run runs", func() {
		job, err := c.UpgradeBackend(context.Background(), "kokoro")
		Expect(err).NotTo(HaveOccurred())
		Expect(job).To(Equal(h.Starter.Job("gallery:kokoro")))
		_, err = c.UpgradeBackend(context.Background(), "kokoro")
		Expect(upgraderun.CodeOf(err)).To(Equal(upgraderun.CodeRunActive))
	})

	It("never says started without a job: a start that waits out answers with the run", func() {
		gate := make(chan struct{})
		DeferCleanup(func() { close(gate) })
		h.Starter.Block(gate)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		job, err := c.UpgradeBackend(ctx, "kokoro")
		Expect(job).To(BeEmpty())
		var pending *upgraderun.PendingError
		Expect(errors.As(err, &pending)).To(BeTrue(), "%v", err)
		Expect(pending.RunID).NotTo(BeEmpty())
		Expect(err.Error()).To(ContainSubstring(pending.RunID))
	})

	It("never upgrades directly beside a run: a backend the run does not list, a model with options, a model retry", func() {
		_, err := c.UpgradeBackend(context.Background(), "kokoro")
		Expect(err).NotTo(HaveOccurred())
		_, err = c.UpgradeBackend(context.Background(), "not-listed")
		Expect(upgraderun.CodeOf(err)).To(Equal(upgraderun.CodeRunActive))
		_, err = c.RetryModelUpgrade(context.Background(), "some-model")
		Expect(upgraderun.CodeOf(err)).To(Equal(upgraderun.CodeRunActive))
		_, err = c.UpgradeModel(context.Background(), "some-model", true, nil)
		Expect(upgraderun.CodeOf(err)).To(Equal(upgraderun.CodeRunActive))
	})

	It("refuses a backend upgrade with the guard of the REST routes before a run starts", func() {
		c.BackendUpgradeGuard = func() *backendupdates.UpgradeGuard {
			return backendupdates.NewUpgradeGuard(func() (gallery.SystemBackends, error) {
				return gallery.SystemBackends{"kokoro": {Name: "kokoro", Metadata: &gallery.BackendMetadata{Name: "kokoro"}}}, nil
			}, nil)
		}
		_, err := c.UpgradeBackend(context.Background(), "missing")
		var refusal *backendupdates.UpgradeRefusal
		Expect(errors.As(err, &refusal)).To(BeTrue(), "%v", err)
		Expect(refusal.Code).To(Equal(gallery.UpgradeFailureNotInstalled))
		runs, err := h.Service.List(context.Background(), upgraderun.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(BeEmpty())
		// An installed backend passes the guard and starts a run of one.
		job, err := c.UpgradeBackend(context.Background(), "kokoro")
		Expect(err).NotTo(HaveOccurred())
		Expect(job).To(Equal(h.Starter.Job("gallery:kokoro")))
	})

	It("never gives the model a credential, a path or raw output that a failing operation carried", func() {
		h.Starter.SetErr(errors.New("pull https://user:pass@host/v2/x failed: Bearer abc\nopen /home/alice/.cache/x/key.pem"))
		ctx := localaitools.WithUser(context.Background(), &auth.User{ID: "admin-1", Role: auth.RoleAdmin})
		run, err := c.StartUpgradeRun(ctx, localaitools.UpgradeRunStartRequest{Items: []localaitools.UpgradeRunItemRef{{Kind: "gallery", Name: "kokoro", ExpectedTo: "v2"}}})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() string {
			r, _ := c.UpgradeRuns(ctx, run.ID)
			return r[0].State
		}, 5*time.Second, 10*time.Millisecond).ShouldNot(Equal("running"))
		runs, err := c.UpgradeRuns(ctx, run.ID)
		Expect(err).NotTo(HaveOccurred())
		raw, err := json.Marshal(runs)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring("key.pem"))
		for _, secret := range []string{"user:pass", "https://user", "Bearer abc", "/home/alice", `\n`} {
			Expect(string(raw)).NotTo(ContainSubstring(secret))
		}
	})

	It("refuses a library install while a run runs, as the REST route does", func() {
		lib := &countingLibrary{}
		c.BackendLibrary = lib
		_, err := c.UpgradeBackend(context.Background(), "kokoro")
		Expect(err).NotTo(HaveOccurred())
		_, err = c.InstallLibraryBackend(context.Background(), localaitools.InstallLibraryBackendRequest{SourceID: "src", Name: "kokoro", Version: "v2"})
		Expect(upgraderun.CodeOf(err)).To(Equal(upgraderun.CodeRunActive))
		Expect(lib.installs).To(BeZero())
	})

	It("answers that runs are unavailable when the server keeps none", func() {
		c.UpgradeRunService = nil
		_, err := c.UpgradeRuns(context.Background(), "")
		Expect(err).To(MatchError(errUpgradeRunsOff))
	})
})

// countingLibrary counts the installs it was asked for.
type countingLibrary struct{ installs int }

func (l *countingLibrary) Install(context.Context, string, string, string) error {
	l.installs++
	return nil
}

func (l *countingLibrary) Updates(context.Context) ([]localaitools.BackendLibraryUpdate, error) {
	return nil, nil
}
