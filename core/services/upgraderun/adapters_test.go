package upgraderun_test

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	. "github.com/mudler/LocalAI/core/services/upgraderun"
)

type fakeBackendSnaps struct {
	cached, fresh backendupdates.Snapshot
}

func (f fakeBackendSnaps) UpdatesSnapshot(context.Context) backendupdates.Snapshot { return f.cached }
func (f fakeBackendSnaps) CheckNow(context.Context) backendupdates.Snapshot        { return f.fresh }

type fakeModelSnaps struct{ snap modelupdates.Snapshot }

func (f fakeModelSnaps) Snapshot(context.Context) modelupdates.Snapshot { return f.snap }
func (f fakeModelSnaps) CheckNow(context.Context) modelupdates.Snapshot { return f.snap }

type fakeEnqueuer struct {
	ops     []galleryop.ManagementOp[gallery.GalleryBackend, any]
	cancels []string
	cache   map[string]string
}

func (f *fakeEnqueuer) EnqueueBackendOp(op galleryop.ManagementOp[gallery.GalleryBackend, any]) {
	f.ops = append(f.ops, op)
}
func (f *fakeEnqueuer) StoreCancellationActions(id string, _, _ context.CancelFunc) {
	f.cancels = append(f.cancels, id)
}
func (f *fakeEnqueuer) SetBackend(key, value string) {
	if f.cache == nil {
		f.cache = map[string]string{}
	}
	f.cache[key] = value
}

type fakeModelEnqueuer struct {
	names []string
	opts  []galleryop.ModelUpgradeOptions
}

func (f *fakeModelEnqueuer) EnqueueModelUpgrade(name string, opts galleryop.ModelUpgradeOptions) (string, error) {
	f.names, f.opts = append(f.names, name), append(f.opts, opts)
	return "model-job", nil
}

type fakeAddonUpgrader struct{ args []string }

func (f *fakeAddonUpgrader) StartAddonUpgrade(_ context.Context, actor, id, expected, candidate, operationID string) (string, error) {
	f.args = []string{actor, id, expected, candidate, operationID}
	return "addon-job", nil
}

type fakeInstaller struct {
	release chan struct{}
	err     error
}

func (f *fakeInstaller) Install(ctx context.Context, sourceID, backend, version string, p backendlib.Progress) error {
	p.Step(backendlib.StepFetching)
	p.Bytes("image", "1MB", "2MB", 50)
	select {
	case <-f.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	p.Step(backendlib.StepWriting)
	return f.err
}

type fakeStatusWriter struct {
	mu       sync.Mutex
	statuses map[string]galleryop.OpStatus
	cancel   map[string]context.CancelFunc
	gone     []string
}

func (f *fakeStatusWriter) UpdateStatus(id string, op *galleryop.OpStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[id] = *op
}
func (f *fakeStatusWriter) StoreCancellation(id string, c context.CancelFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancel[id] = c
}
func (f *fakeStatusWriter) RemoveCancellation(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cancel, id)
	f.gone = append(f.gone, id)
}

func (f *fakeStatusWriter) removed(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, g := range f.gone {
		if g == id {
			return true
		}
	}
	return false
}

func (f *fakeStatusWriter) get(id string) galleryop.OpStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses[id]
}

var _ = Describe("Checkers", func() {
	ctx := context.Background()

	It("builds an add-on candidate that shows versions and pins the candidate digest", func() {
		c := AddonCandidate(AddonVersions{ID: "notes", Title: "Notes sync",
			InstalledVersion: "0.8.1", InstalledDigest: "sha256:aa", CandidateVersion: "0.8.2", CandidateDigest: "sha256:bb"})
		Expect(c.Key()).To(Equal("addon:notes"))
		Expect(c.Title).To(Equal("Notes sync"))
		Expect([]string{c.From, c.To, c.Pin}).To(Equal([]string{"0.8.1", "0.8.2", "sha256:bb"}))
		Expect(c.Extra).To(Equal(map[string]string{ExtraExpectedDigest: "sha256:aa", ExtraCandidateDigest: "sha256:bb"}))
		// Every add-on upgrade takes a backup and lets the new version
		// migrate the data.
		Expect(c.Risks).To(ConsistOf(RiskMigratesData))

		// Without versions the digests are all there is to show.
		c = AddonCandidate(AddonVersions{ID: "notes", InstalledDigest: "sha256:aa", CandidateDigest: "sha256:bb"})
		Expect([]string{c.From, c.To, c.Pin}).To(Equal([]string{"sha256:aa", "sha256:bb", "sha256:bb"}))
	})

	It("lists gallery and library backends with their target, risks and the freshness of the sources", func() {
		snap := backendupdates.Snapshot{
			Sources: map[string]backendupdates.Source{"gallery": {State: backendupdates.StateOK}, "library": {State: backendupdates.StateDisabled}},
			Items: []backendupdates.Item{
				{Kind: backendupdates.KindGallery, Name: "kokoro", InstalledVersion: "1", AvailableVersion: "2",
					Nodes: []backendupdates.Node{{NodeID: "n1", Models: []string{"tts"}}}, DriftNodes: 1},
				{Kind: backendupdates.KindGallery, Name: "digest-only", InstalledDigest: "sha256:a", AvailableDigest: "sha256:b"},
				{Kind: backendupdates.KindGallery, Name: "llama-cpp", InstalledVersion: "0.9.4", AvailableVersion: "0.9.7", Nodes: []backendupdates.Node{
					{NodeID: "a", Models: []string{"qwen3-8b"}}, {NodeID: "b", Models: []string{"llama-3.2-3b"}}, {NodeID: "c"}}},
				{Kind: backendupdates.KindGallery, Name: "piper", InstalledVersion: "1.2.0", AvailableVersion: "1.3.1", Nodes: []backendupdates.Node{
					{NodeID: "a"}, {NodeID: "b"}}},
				{Kind: backendupdates.KindLibrary, Name: "whisper", SourceID: "lib", InstalledVersion: "0.1", AvailableVersion: "0.2"},
			},
		}
		stale := snap
		stale.Sources = map[string]backendupdates.Source{"gallery": {State: backendupdates.StateFailed}}
		stale.Throttled = true
		c := BackendChecker{Snapshots: fakeBackendSnaps{cached: snap, fresh: stale}}
		Expect(c.Kinds()).To(ConsistOf(KindGallery, KindLibrary))

		l, err := c.List(ctx, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(l.Complete).To(BeTrue())
		byKey := map[string]Candidate{}
		for _, cand := range l.Candidates {
			byKey[cand.Key()] = cand
		}
		Expect(byKey["gallery:kokoro"].To).To(Equal("2"))
		// The cluster knows the models that stop and how many nodes they
		// run on; the review says so with these numbers.
		Expect(byKey["gallery:kokoro"].Risks).To(ConsistOf(RiskStopsModels, RiskDrift))
		Expect(byKey["gallery:kokoro"].RiskParams).To(Equal(map[string]map[string]string{
			RiskStopsModels: {"count": "1", "nodes": "1"}, RiskDrift: {"count": "1"},
		}))
		Expect(byKey["gallery:llama-cpp"].Risks).To(ConsistOf(RiskStopsModels))
		Expect(byKey["gallery:llama-cpp"].RiskParams[RiskStopsModels]).To(Equal(map[string]string{"count": "2", "nodes": "2"}))
		// No model is known to stop: the line says the nodes it is replaced on.
		Expect(byKey["gallery:piper"].Risks).To(ConsistOf(RiskCluster))
		Expect(byKey["gallery:piper"].RiskParams[RiskCluster]).To(Equal(map[string]string{"nodes": "2"}))
		// A standalone server reports no nodes, so it claims nothing.
		Expect(byKey["gallery:digest-only"].Risks).To(BeEmpty())
		Expect(byKey["gallery:digest-only"].To).To(Equal("sha256:b"))
		Expect(byKey["gallery:digest-only"].From).To(Equal("sha256:a"))
		lib := byKey["library:lib/whisper"]
		Expect(lib.Extra).To(Equal(map[string]string{ExtraSourceID: "lib", ExtraBackend: "whisper", ExtraVersion: "0.2"}))

		l, err = c.List(ctx, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(l.Complete).To(BeFalse())
		Expect(l.Throttled).To(BeTrue())

		snap.InstallsDisabled = true
		l, _ = BackendChecker{Snapshots: fakeBackendSnaps{cached: snap}}.List(ctx, false)
		Expect(l.Candidates[0].Blocked).To(Equal("installs_disabled"))

		l, _ = BackendChecker{}.List(ctx, false)
		Expect(l.Complete).To(BeTrue())
	})

	It("lists models with an update, held ones with a moved entry, edited ones with their conflicting keys, and failed ones as retries", func() {
		snap := modelupdates.Snapshot{
			Sources: map[string]backendupdates.Source{"gallery": {State: backendupdates.StateOK}},
			Items: []modelupdates.Item{
				{Name: "up", State: modelupdates.StateUpdateAvailable, InstalledDigest: "a", AvailableDigest: "b", ConfigOnly: true},
				{Name: "same", State: modelupdates.StateUpToDate},
				{Name: "held-moved", State: modelupdates.StateHeld, Held: true, InstalledDigest: "a", AvailableDigest: "b"},
				{Name: "held-still", State: modelupdates.StateHeld, Held: true, InstalledDigest: "a", AvailableDigest: "a"},
				{Name: "edited", State: modelupdates.StateEdited, AvailableDigest: "b", EditedKeys: []modelupdates.KeyEdit{{Key: "x", Conflict: true}, {Key: "y"}}},
				{Name: "failed", State: modelupdates.StateFailed, AvailableDigest: "b"},
				{Name: "unknown", State: modelupdates.StateUnknown},
			},
		}
		l, err := ModelChecker{Snapshots: fakeModelSnaps{snap}}.List(ctx, true)
		Expect(err).NotTo(HaveOccurred())
		byKey := map[string]Candidate{}
		for _, cand := range l.Candidates {
			byKey[cand.Key()] = cand
		}
		Expect(byKey).To(HaveLen(4))
		// Every model update reloads the model and downloads its files: only
		// a config-only update has a line of its own.
		Expect(byKey["model:up"].Risks).To(ConsistOf(RiskConfigOnly))
		Expect(byKey["model:held-moved"].Held).To(BeTrue())
		Expect(byKey["model:edited"].EditedKeys).To(Equal([]string{"x"}))
		Expect(byKey["model:failed"].Extra[ExtraRetry]).To(Equal("true"))
		Expect(byKey["model:failed"].Risks).To(BeEmpty())
	})
})

var _ = Describe("Starters", func() {
	ctx := context.Background()

	It("queues a gallery backend upgrade like the upgrade route", func() {
		q := &fakeEnqueuer{}
		s := GalleryBackendStarter{Gallery: q, OpCache: q, Galleries: func() []config.Gallery { return []config.Gallery{{Name: "main"}} }}
		id, err := s.Start(ctx, "admin", Item{Kind: KindGallery, Name: "kokoro"})
		Expect(err).NotTo(HaveOccurred())
		Expect(q.ops).To(HaveLen(1))
		Expect(q.ops[0].ID).To(Equal(id))
		Expect(q.ops[0].Upgrade).To(BeTrue())
		Expect(q.ops[0].InRun).To(BeTrue(), "the run reports the end of its operations")
		Expect(q.ops[0].GalleryElementName).To(Equal("kokoro"))
		Expect(q.ops[0].Galleries).To(HaveLen(1))
		Expect(q.cancels).To(Equal([]string{id}))
		Expect(q.cache).To(HaveKeyWithValue("kokoro", id))
	})

	It("queues a model upgrade after the checks and maps their refusals", func() {
		q := &fakeModelEnqueuer{}
		var validated []string
		s := ModelStarter{Gallery: q, Validate: func(name string, retry bool) error {
			validated = append(validated, name)
			switch name {
			case "held":
				return modelupdates.ErrHeld
			case "edited":
				return &modelupdates.ErrNeedsChoices{Keys: []string{"k"}}
			case "busy":
				return modelupdates.ErrBusy
			case "current":
				return modelupdates.ErrUpToDate
			case "gone":
				return modelupdates.ErrNotFound
			}
			return nil
		}}
		id, err := s.Start(ctx, "admin", Item{Kind: KindModel, Name: "m", Extra: map[string]string{ExtraRetry: "true"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal("model-job"))
		Expect(q.opts[0].Retry).To(BeTrue())
		Expect(q.opts[0].InRun).To(BeTrue())

		check := func(name, key string, skip, retry bool) {
			_, err := s.Start(ctx, "admin", Item{Kind: KindModel, Name: name})
			var se *StartError
			ExpectWithOffset(1, errors.As(err, &se)).To(BeTrue())
			ExpectWithOffset(1, se.ErrorKey).To(Equal(key))
			ExpectWithOffset(1, se.Skip).To(Equal(skip))
			ExpectWithOffset(1, se.Retryable).To(Equal(retry))
		}
		check("held", ErrKeyHeld, true, false)
		check("edited", ErrKeyEditedKeys, true, false)
		check("busy", ErrKeyUnknown, false, true)
		check("current", ErrKeyStale, false, false)
		check("gone", ErrKeyUnknown, false, false)
		Expect(q.names).To(Equal([]string{"m"}))
	})

	It("hands the digests of an add-on to the add-on upgrader", func() {
		u := &fakeAddonUpgrader{}
		id, err := AddonStarter{Upgrader: u}.Start(ctx, "admin", Item{Kind: KindAddon, Name: "notes", From: "sha256:old", ExpectedTo: "sha256:new"})
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal("addon-job"))
		Expect(u.args).To(Equal([]string{"admin", "notes", "sha256:old", "sha256:new", ""}))
		// The operation the upgrade route recorded travels with the item.
		_, err = AddonStarter{Upgrader: u}.Start(ctx, "admin", Item{Kind: KindAddon, Name: "notes", From: "sha256:old", ExpectedTo: "sha256:new",
			Extra: map[string]string{ExtraOperationID: "op-1"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(u.args).To(Equal([]string{"admin", "notes", "sha256:old", "sha256:new", "op-1"}))
		_, err = AddonStarter{}.Start(ctx, "admin", Item{Kind: KindAddon, Name: "notes"})
		Expect(err).To(HaveOccurred())
	})

	It("installs a library backend with the status the install route writes, under the shared guard", func() {
		inst := &fakeInstaller{release: make(chan struct{})}
		jobs := &backendlib.JobRunner{Install: inst.Install}
		w := &fakeStatusWriter{statuses: map[string]galleryop.OpStatus{}, cancel: map[string]context.CancelFunc{}}
		start := LibraryStarter{Jobs: jobs, Status: w}
		id, err := start.Start(ctx, "admin", Item{Kind: KindLibrary, Name: "lib/whisper", ExpectedTo: "0.2"})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() float64 { return w.get(id).Progress }).Should(Equal(50.0))
		Expect(w.get(id).Cancellable).To(BeTrue())
		Expect(w.get(id).Upgrade).To(BeTrue())
		Expect(w.get(id).Message).To(Equal(backendlib.StepMessage(backendlib.StepFetching)))
		// An install of the same backend name from the library route, through
		// the same runner, is refused while the run's install runs.
		_, err = jobs.Start(ctx, w, backendlib.Job{SourceID: "other", Backend: "whisper", Version: "1"})
		Expect(err).To(MatchError(backendlib.ErrInstalling))
		close(inst.release)
		Eventually(func() bool { return w.get(id).Processed }).Should(BeTrue())
		Expect(w.get(id).Message).To(Equal(galleryop.MessageCompleted))
		Eventually(func() bool { return w.removed(id) }).Should(BeTrue(), "the cancel of an ended job is dropped")

		inst = &fakeInstaller{release: make(chan struct{})}
		jobs.Install = inst.Install
		id, err = start.Start(ctx, "admin", Item{Kind: KindLibrary, Name: "lib/whisper", ExpectedTo: "0.2"})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() float64 { return w.get(id).Progress }).Should(Equal(50.0))
		// A second item of the same backend while the first runs is a
		// retryable refusal.
		_, err = start.Start(ctx, "admin", Item{Kind: KindLibrary, Name: "lib2/whisper", ExpectedTo: "0.3"})
		var se *StartError
		Expect(errors.As(err, &se)).To(BeTrue())
		Expect(se.Retryable).To(BeTrue())
		w.mu.Lock()
		cancel := w.cancel[id]
		w.mu.Unlock()
		cancel()
		Eventually(func() bool { return w.get(id).Cancelled }).Should(BeTrue())

		_, err = LibraryStarter{}.Start(ctx, "admin", Item{Kind: KindLibrary, Name: "lib/whisper"})
		Expect(err).To(HaveOccurred())
	})
})
