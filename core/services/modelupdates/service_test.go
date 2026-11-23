package modelupdates_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/pkg/system"
)

// fixture is a models directory and a local gallery. Nothing reaches the
// network: the gallery, the base configs and the weights are local files.
type fixture struct {
	dir, src    string
	systemState *system.SystemState
	galleries   []config.Gallery
	entries     []gallery.GalleryModel
}

var galleryRevision atomic.Int32

func newFixture() *fixture {
	dir, err := os.MkdirTemp("", "modelupdates")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { Expect(os.RemoveAll(dir)).To(Succeed()) })
	f := &fixture{dir: dir, src: filepath.Join(dir, "src")}
	Expect(os.MkdirAll(f.src, 0750)).To(Succeed())
	f.systemState, err = system.GetSystemState(system.WithModelPath(dir))
	Expect(err).ToNot(HaveOccurred())
	// One gallery name for the whole spec: the origin records it.
	name := fmt.Sprintf("updates-%d", galleryRevision.Add(1))
	f.galleries = []config.Gallery{{Name: name, URL: "file://" + filepath.Join(f.src, "index.yaml")}}
	return f
}

func (f *fixture) weights(name, content string) gallery.File {
	path := filepath.Join(f.src, name)
	Expect(os.WriteFile(path, []byte(content), 0600)).To(Succeed())
	return gallery.File{Filename: name, URI: "file://" + path, SHA256: gallery.SHA256Hex([]byte(content))}
}

func (f *fixture) publish(entries ...gallery.GalleryModel) {
	f.entries = entries
	out, err := yaml.Marshal(entries)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(f.src, "index.yaml"), out, 0600)).To(Succeed())
	gallery.ForgetGalleryIndexes()
}

func (f *fixture) install(entry string) {
	Expect(gallery.InstallModelFromGallery(context.TODO(), f.galleries, nil, f.systemState, nil, f.galleries[0].Name+"@"+entry,
		gallery.GalleryModel{}, func(string, string, string, float64) {}, false, false, false)).To(Succeed())
}

func (f *fixture) list(_ context.Context, fresh bool) ([]*gallery.GalleryModel, error) {
	if fresh {
		gallery.ForgetGalleryIndexes()
	}
	return gallery.AvailableGalleryModels(f.galleries, f.systemState)
}

type recordingNotifier struct {
	mu    sync.Mutex
	calls [][]modelupdates.Item
}

func (n *recordingNotifier) ModelUpdates(_ context.Context, items []modelupdates.Item) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, items)
	return nil
}

func (n *recordingNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.calls)
}

func (f *fixture) service(opts modelupdates.Options) *modelupdates.Service {
	opts.SystemState = f.systemState
	if opts.ListGallery == nil {
		opts.ListGallery = f.list
	}
	if opts.Cooldown == 0 {
		opts.Cooldown = time.Nanosecond
	}
	return modelupdates.New(opts)
}

func itemOf(snap modelupdates.Snapshot, name string) modelupdates.Item {
	for _, it := range snap.Items {
		if it.Name == name {
			return it
		}
	}
	Fail("no item for " + name)
	return modelupdates.Item{}
}

func tinyEntry(f *fixture, w gallery.File) gallery.GalleryModel {
	return gallery.GalleryModel{
		Metadata:  gallery.Metadata{Name: "tiny", Description: "v1", AdditionalFiles: []gallery.File{w}},
		Overrides: map[string]any{"backend": "llama-cpp", "context_size": 2048, "parameters": map[string]any{"model": w.Filename}},
	}
}

var _ = Describe("The model update check", func() {
	var (
		f   *fixture
		svc *modelupdates.Service
		ctx context.Context
	)

	BeforeEach(func() {
		f = newFixture()
		ctx = context.Background()
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v1")))
		f.install("tiny")
		svc = f.service(modelupdates.Options{})
	})

	It("reports a model as not checked before the first check", func() {
		snap := svc.Snapshot(ctx)
		Expect(snap.Sources[modelupdates.SourceGallery].State).To(Equal(backendupdates.StateNever))
		it := itemOf(snap, "tiny")
		Expect(it.State).To(Equal(modelupdates.StateUnknown))
		Expect(it.Reason).To(Equal(modelupdates.ReasonNotChecked))
		Expect(it.Kind).To(Equal(modelupdates.KindModel))
	})

	It("reports an unchanged entry as up to date", func() {
		snap := svc.CheckNow(ctx)
		Expect(snap.Sources[modelupdates.SourceGallery].State).To(Equal(backendupdates.StateOK))
		it := itemOf(snap, "tiny")
		Expect(it.State).To(Equal(modelupdates.StateUpToDate))
		Expect(it.NeedsPerson).To(BeFalse())
		Expect(it.AvailableDigest).To(Equal(it.InstalledDigest))
	})

	It("does not count a description change", func() {
		e := tinyEntry(f, f.weights("tiny.gguf", "v1"))
		e.Description = "a better description"
		f.publish(e)
		Expect(itemOf(svc.CheckNow(ctx), "tiny").State).To(Equal(modelupdates.StateUpToDate))
	})

	It("reports a new checksum as an update that needs a person", func() {
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		it := itemOf(svc.CheckNow(ctx), "tiny")
		Expect(it.State).To(Equal(modelupdates.StateUpdateAvailable))
		Expect(it.NeedsPerson).To(BeTrue())
		Expect(it.ChangedFiles).To(ConsistOf(modelupdates.FileChange{Path: "tiny.gguf", Role: gallery.RoleWeights, Change: modelupdates.ChangeChanged, Checksum: true}))
		Expect(it.ConfigOnly).To(BeFalse())
	})

	It("reports a config-only change", func() {
		e := tinyEntry(f, f.weights("tiny.gguf", "v1"))
		e.Overrides["context_size"] = 4096
		f.publish(e)
		it := itemOf(svc.CheckNow(ctx), "tiny")
		Expect(it.State).To(Equal(modelupdates.StateUpdateAvailable))
		Expect(it.ConfigOnly).To(BeTrue())
	})

	It("reports an edited key the update also changes", func() {
		patchDisk(f, "tiny", func(m map[string]any) { m["context_size"] = 8192; m["pinned"] = true })
		e := tinyEntry(f, f.weights("tiny.gguf", "v1"))
		e.Overrides["context_size"] = 4096
		f.publish(e)
		it := itemOf(svc.CheckNow(ctx), "tiny")
		Expect(it.State).To(Equal(modelupdates.StateEdited))
		Expect(it.NeedsPerson).To(BeTrue())
		Expect(it.EditedKeys).To(ConsistOf(modelupdates.KeyEdit{Key: "context_size", Installed: 2048, Mine: 8192, Gallery: 4096, Conflict: true}))
		Expect(svc.MissingChoices("tiny", nil)).To(Equal([]string{"context_size"}))
		Expect(svc.MissingChoices("tiny", map[string]string{"context_size": modelupdates.ChoiceGallery})).To(BeEmpty())
	})

	It("keeps an edit the update does not touch out of the conflicts", func() {
		patchDisk(f, "tiny", func(m map[string]any) { m["f16"] = true })
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		it := itemOf(svc.CheckNow(ctx), "tiny")
		Expect(it.State).To(Equal(modelupdates.StateUpdateAvailable))
		Expect(it.EditedKeys).To(HaveLen(1))
		Expect(it.EditedKeys[0].Conflict).To(BeFalse())
	})

	It("reports an entry the gallery removed as gone", func() {
		f.publish(gallery.GalleryModel{Metadata: gallery.Metadata{Name: "other"}, Overrides: map[string]any{"backend": "x"}})
		it := itemOf(svc.CheckNow(ctx), "tiny")
		Expect(it.State).To(Equal(modelupdates.StateGone))
		Expect(it.NeedsPerson).To(BeFalse())
	})

	It("reports a held model as held, with the available digest", func() {
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		sc.Hold = true
		Expect(gallery.WriteSidecar(f.dir, "tiny", sc)).To(Succeed())
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		it := itemOf(svc.CheckNow(ctx), "tiny")
		Expect(it.State).To(Equal(modelupdates.StateHeld))
		Expect(it.Held).To(BeTrue())
		Expect(it.NeedsPerson).To(BeFalse())
		Expect(it.AvailableDigest).ToNot(Equal(it.InstalledDigest))
	})

	It("reports a rolled back upgrade as failed with its error", func() {
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		sc.Upgrade = &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseRolledBack, Error: "load failed", Attempts: 1}
		Expect(gallery.WriteSidecar(f.dir, "tiny", sc)).To(Succeed())
		it := itemOf(svc.CheckNow(ctx), "tiny")
		Expect(it.State).To(Equal(modelupdates.StateFailed))
		Expect(it.NeedsPerson).To(BeTrue())
		Expect(it.Upgrade.Error).To(Equal("load failed"))
		Expect(it.Upgrade.Running).To(BeFalse())
	})

	It("reports a model installed by an older release as unknown", func() {
		Expect(os.WriteFile(gallery.SidecarPath(f.dir, "legacy"), []byte("name: legacy\nentry_name: legacy\n"), 0600)).To(Succeed())
		it := itemOf(svc.CheckNow(ctx), "legacy")
		Expect(it.State).To(Equal(modelupdates.StateUnknown))
		Expect(it.Reason).To(Equal(modelupdates.ReasonLegacySidecar))
	})

	It("reports an entry whose base config cannot be read", func() {
		e := tinyEntry(f, f.weights("tiny.gguf", "v1"))
		e.URL = "file://" + filepath.Join(f.src, "missing.yaml")
		f.publish(e)
		it := itemOf(svc.CheckNow(ctx), "tiny")
		Expect(it.State).To(Equal(modelupdates.StateUnknown))
		Expect(it.Reason).To(Equal(modelupdates.ReasonCheckFailed))
		Expect(it.Error).ToNot(BeEmpty())
	})

	It("keeps the last answer and marks the source failed when the gallery cannot be read", func() {
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		Expect(itemOf(svc.CheckNow(ctx), "tiny").State).To(Equal(modelupdates.StateUpdateAvailable))
		failing := f.service(modelupdates.Options{ListGallery: func(context.Context, bool) ([]*gallery.GalleryModel, error) {
			return nil, errors.New("gallery down")
		}})
		snap := failing.CheckNow(ctx)
		src := snap.Sources[modelupdates.SourceGallery]
		Expect(src.State).To(Equal(backendupdates.StateFailed))
		Expect(src.Stale).To(BeTrue())
		Expect(src.Error).To(ContainSubstring("gallery down"))
		Expect(itemOf(snap, "tiny").State).To(Equal(modelupdates.StateUpdateAvailable))
	})

	It("tells the notifier which models need a person, and not when the gallery failed", func() {
		n := &recordingNotifier{}
		svc = f.service(modelupdates.Options{Notifier: n})
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		svc.CheckNow(ctx)
		Expect(n.count()).To(Equal(1))
		Expect(n.calls[0]).To(HaveLen(1))
		Expect(n.calls[0][0].Name).To(Equal("tiny"))

		failing := f.service(modelupdates.Options{Notifier: n, ListGallery: func(context.Context, bool) ([]*gallery.GalleryModel, error) {
			return nil, errors.New("down")
		}})
		failing.CheckNow(ctx)
		Expect(n.count()).To(Equal(1), "an unanswered gallery must not clear the digest")
	})

	It("answers a call right after a check from that check", func() {
		svc = f.service(modelupdates.Options{Cooldown: time.Hour})
		Expect(svc.CheckNow(ctx).Throttled).To(BeFalse())
		Expect(svc.CheckNow(ctx).Throttled).To(BeTrue())
	})

	It("lists no item for a model installed from a config rather than a gallery entry", func() {
		Expect(gallery.WriteSidecar(f.dir, "imported", &gallery.ModelConfig{Name: "imported", Version: gallery.SidecarVersion})).To(Succeed())
		for _, it := range svc.CheckNow(ctx).Items {
			Expect(it.Name).ToNot(Equal("imported"))
		}
	})
})

// Two frontends share one models directory and one lock. A check asked of
// both at once runs once, and both answer with what it found.
var _ = Describe("The model update check on two frontends", func() {
	It("runs one check and both frontends agree", func() {
		f := newFixture()
		ctx := context.Background()
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v1")))
		f.install("tiny")
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))

		var lock sync.Mutex
		shared := func(_ context.Context, fn func() error) (bool, error) {
			if !lock.TryLock() {
				return false, nil
			}
			defer lock.Unlock()
			return true, fn()
		}
		var lists atomic.Int32
		entered := make(chan struct{})
		release := make(chan struct{})
		list := func(ctx context.Context, fresh bool) ([]*gallery.GalleryModel, error) {
			if lists.Add(1) == 1 {
				close(entered)
				<-release
			}
			return f.list(ctx, fresh)
		}
		n := &recordingNotifier{}
		a := f.service(modelupdates.Options{Lock: shared, ListGallery: list, Notifier: n, WaitPoll: 5 * time.Millisecond})
		b := f.service(modelupdates.Options{Lock: shared, ListGallery: list, Notifier: n, WaitPoll: 5 * time.Millisecond})

		var snapA, snapB modelupdates.Snapshot
		var wg sync.WaitGroup
		wg.Go(func() {
			defer GinkgoRecover()
			snapA = a.CheckNow(ctx)
		})
		<-entered
		wg.Go(func() {
			defer GinkgoRecover()
			snapB = b.CheckNow(ctx)
		})
		// b found the lock taken and waits for a's check file.
		Consistently(lists.Load, 50*time.Millisecond).Should(Equal(int32(1)))
		close(release)
		wg.Wait()

		Expect(lists.Load()).To(Equal(int32(1)), "one check for two requests")
		Expect(n.count()).To(Equal(1), "one notification for one check")
		Expect(snapB).To(Equal(snapA))
		Expect(itemOf(snapB, "tiny").State).To(Equal(modelupdates.StateUpdateAvailable))
	})
})

func patchDisk(f *fixture, name string, change func(map[string]any)) {
	path := filepath.Join(f.dir, name+".yaml")
	data, err := os.ReadFile(path)
	Expect(err).ToNot(HaveOccurred())
	m := map[string]any{}
	Expect(yaml.Unmarshal(data, &m)).To(Succeed())
	change(m)
	out, err := yaml.Marshal(m)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(path, out, 0600)).To(Succeed())
}

var _ = Describe("The periodic model update check", func() {
	It("reads the galleries fresh at each tick", func() {
		f := newFixture()
		fresh := make(chan bool, 4)
		svc := f.service(modelupdates.Options{
			Interval: time.Hour, InitialDelay: time.Millisecond,
			ListGallery: func(c context.Context, wantFresh bool) ([]*gallery.GalleryModel, error) {
				fresh <- wantFresh
				return f.list(c, wantFresh)
			},
		})
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		go svc.Run(ctx)
		Eventually(fresh).Should(Receive(BeTrue()))
	})
})
