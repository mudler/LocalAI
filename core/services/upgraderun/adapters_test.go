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

func (f *fakeAddonUpgrader) StartAddonUpgrade(_ context.Context, actor, id, expected, candidate string) (string, error) {
	f.args = []string{actor, id, expected, candidate}
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
func (f *fakeStatusWriter) get(id string) galleryop.OpStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses[id]
}

var _ = Describe("Checkers", func() {
	ctx := context.Background()

	It("lists gallery and library backends with their target, risks and the freshness of the sources", func() {
		snap := backendupdates.Snapshot{
			Sources: map[string]backendupdates.Source{"gallery": {State: backendupdates.StateOK}, "library": {State: backendupdates.StateDisabled}},
			Items: []backendupdates.Item{
				{Kind: backendupdates.KindGallery, Name: "kokoro", InstalledVersion: "1", AvailableVersion: "2",
					Nodes: []backendupdates.Node{{NodeID: "n1", Models: []string{"tts"}}}, DriftNodes: 1},
				{Kind: backendupdates.KindGallery, Name: "digest-only", InstalledDigest: "sha256:a", AvailableDigest: "sha256:b"},
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
		Expect(byKey["gallery:kokoro"].Risks).To(ConsistOf(RiskCluster, RiskStopsModels, RiskDrift))
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
		Expect(byKey["model:up"].Risks).To(ConsistOf(RiskStopsModel, RiskConfigOnly))
		Expect(byKey["model:held-moved"].Held).To(BeTrue())
		Expect(byKey["model:edited"].EditedKeys).To(Equal([]string{"x"}))
		Expect(byKey["model:failed"].Extra[ExtraRetry]).To(Equal("true"))
		Expect(byKey["model:failed"].Risks).To(ContainElement(RiskDownloads))
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
		Expect(u.args).To(Equal([]string{"admin", "notes", "sha256:old", "sha256:new"}))
		_, err = AddonStarter{}.Start(ctx, "admin", Item{Kind: KindAddon, Name: "notes"})
		Expect(err).To(HaveOccurred())
	})

	It("installs a library backend with the status the install route writes", func() {
		inst := &fakeInstaller{release: make(chan struct{})}
		w := &fakeStatusWriter{statuses: map[string]galleryop.OpStatus{}, cancel: map[string]context.CancelFunc{}}
		id, err := LibraryStarter{Installer: inst, Status: w}.Start(ctx, "admin", Item{Kind: KindLibrary, Name: "lib/whisper", ExpectedTo: "0.2"})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() float64 { return w.get(id).Progress }).Should(Equal(50.0))
		Expect(w.get(id).Cancellable).To(BeTrue())
		close(inst.release)
		Eventually(func() bool { return w.get(id).Processed }).Should(BeTrue())
		Expect(w.get(id).Message).To(Equal(galleryop.MessageCompleted))

		inst = &fakeInstaller{release: make(chan struct{})}
		id, err = LibraryStarter{Installer: inst, Status: w}.Start(ctx, "admin", Item{Kind: KindLibrary, Name: "lib/whisper", ExpectedTo: "0.2"})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() float64 { return w.get(id).Progress }).Should(Equal(50.0))
		w.mu.Lock()
		cancel := w.cancel[id]
		w.mu.Unlock()
		cancel()
		Eventually(func() bool { return w.get(id).Cancelled }).Should(BeTrue())

		_, err = LibraryStarter{}.Start(ctx, "admin", Item{Kind: KindLibrary, Name: "lib/whisper"})
		Expect(err).To(HaveOccurred())
	})
})
