package modelupdates_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

var _ = Describe("The upgrade state machine", func() {
	phases := []string{"", gallery.PhasePreflight, gallery.PhaseStaging, gallery.PhaseUnloading, gallery.PhaseCommitting,
		gallery.PhaseLoading, gallery.PhaseConfirmed, gallery.PhaseRollingBack, gallery.PhaseRolledBack, gallery.PhaseFailed}
	events := []modelupdates.Event{modelupdates.EvStart, modelupdates.EvOK, modelupdates.EvFailed, modelupdates.EvInterrupted}
	// The whole table. A missing pair is a refused event.
	want := map[string]map[modelupdates.Event]string{
		"":                       {modelupdates.EvStart: gallery.PhasePreflight},
		gallery.PhaseConfirmed:   {modelupdates.EvStart: gallery.PhasePreflight},
		gallery.PhaseRolledBack:  {modelupdates.EvStart: gallery.PhasePreflight},
		gallery.PhaseFailed:      {modelupdates.EvStart: gallery.PhasePreflight},
		gallery.PhasePreflight:   {modelupdates.EvOK: gallery.PhaseStaging, modelupdates.EvFailed: gallery.PhaseFailed, modelupdates.EvInterrupted: gallery.PhaseFailed},
		gallery.PhaseStaging:     {modelupdates.EvOK: gallery.PhaseUnloading, modelupdates.EvFailed: gallery.PhaseFailed, modelupdates.EvInterrupted: gallery.PhaseFailed},
		gallery.PhaseUnloading:   {modelupdates.EvOK: gallery.PhaseCommitting, modelupdates.EvFailed: gallery.PhaseRollingBack, modelupdates.EvInterrupted: gallery.PhaseRollingBack},
		gallery.PhaseCommitting:  {modelupdates.EvOK: gallery.PhaseLoading, modelupdates.EvFailed: gallery.PhaseRollingBack, modelupdates.EvInterrupted: gallery.PhaseRollingBack},
		gallery.PhaseLoading:     {modelupdates.EvOK: gallery.PhaseConfirmed, modelupdates.EvFailed: gallery.PhaseRollingBack, modelupdates.EvInterrupted: gallery.PhaseRollingBack},
		gallery.PhaseRollingBack: {modelupdates.EvOK: gallery.PhaseRolledBack, modelupdates.EvFailed: gallery.PhaseFailed, modelupdates.EvInterrupted: gallery.PhaseRollingBack},
	}
	for _, phase := range phases {
		for _, ev := range events {
			It("maps ("+phase+", "+string(ev)+")", func() {
				got, err := modelupdates.Transition(phase, ev)
				next, ok := want[phase][ev]
				if !ok {
					var bad *modelupdates.ErrBadTransition
					Expect(errors.As(err, &bad)).To(BeTrue(), "refused, got %q", got)
					Expect(got).To(Equal(phase))
					return
				}
				Expect(err).ToNot(HaveOccurred())
				Expect(got).To(Equal(next))
			})
		}
	}
})

// fakeRuntime records the calls and fails the loads it is told to fail. Like
// the model loader, a load of a model that runs does nothing. With dir set, a
// load reads the model's weights, so a spec can tell which version runs.
type fakeRuntime struct {
	mu        sync.Mutex
	loaded    map[string]bool
	calls     []string
	loadFails int
	reloadErr error
	dir       string
	version   map[string]string
	// beforeUnload and beforeLoad run once, at the next unload or load.
	beforeUnload, beforeLoad func()
}

func newRuntime(loaded ...string) *fakeRuntime {
	r := &fakeRuntime{loaded: map[string]bool{}}
	for _, n := range loaded {
		r.loaded[n] = true
	}
	return r
}

func (r *fakeRuntime) IsLoaded(_ context.Context, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loaded[name]
}

func (r *fakeRuntime) Unload(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "unload")
	if hook := r.beforeUnload; hook != nil {
		r.beforeUnload = nil
		hook()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.loaded[name] = false
	return nil
}

func (r *fakeRuntime) Reload(ctx context.Context, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "reload")
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.reloadErr
}

func (r *fakeRuntime) Load(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "load")
	if hook := r.beforeLoad; hook != nil {
		r.beforeLoad = nil
		hook()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.loadFails > 0 {
		r.loadFails--
		return errors.New("the backend could not load the model")
	}
	if !r.loaded[name] {
		r.readVersion(name)
	}
	r.loaded[name] = true
	return nil
}

func (r *fakeRuntime) readVersion(name string) {
	if r.dir == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(r.dir, name+".gguf"))
	Expect(err).ToNot(HaveOccurred())
	if r.version == nil {
		r.version = map[string]string{}
	}
	r.version[name] = string(data)
}

// request is a request for the model that arrives while the upgrade runs: it
// loads the model when it does not run. It returns false when the model's
// loads are blocked.
func (r *fakeRuntime) request(name string, blocked bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if blocked {
		return false
	}
	if !r.loaded[name] {
		r.readVersion(name)
		r.loaded[name] = true
	}
	return true
}

// gatedRuntime is a fakeRuntime that can refuse the loads of a model.
type gatedRuntime struct {
	*fakeRuntime
	blocked map[string]bool
	// unblockFails is how many calls of UnblockLoads fail.
	unblockFails int
}

func (g *gatedRuntime) BlockLoads(_ context.Context, name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, "block")
	g.blocked[name] = true
	return nil
}

func (g *gatedRuntime) UnblockLoads(_ context.Context, name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, "unblock")
	if g.unblockFails > 0 {
		g.unblockFails--
		return errors.New("registry unavailable")
	}
	delete(g.blocked, name)
	return nil
}

func (g *gatedRuntime) request(name string) bool {
	g.mu.Lock()
	blocked := g.blocked[name]
	g.mu.Unlock()
	return g.fakeRuntime.request(name, blocked)
}

// windowFS calls during at the first rename of the commit: the moment the
// model is stopped and its files are about to change.
type windowFS struct {
	modelupdates.OSFS
	during func()
	done   bool
}

func (w *windowFS) Rename(a, b string) error {
	if !w.done {
		w.done = true
		w.during()
	}
	return w.OSFS.Rename(a, b)
}

// crashFS is the real filesystem that stops the process (a panic the spec
// catches) at the n-th change, so a spec can see what a crash leaves.
type crashFS struct {
	modelupdates.OSFS
	at, n int
	fail  bool // return an error instead of crashing
}

type crash struct{}

func (c *crashFS) step() error {
	c.n++
	if c.at > 0 && c.n == c.at {
		if c.fail {
			return errors.New("injected failure")
		}
		panic(crash{})
	}
	return nil
}

func (c *crashFS) Rename(a, b string) error {
	if err := c.step(); err != nil {
		return err
	}
	return c.OSFS.Rename(a, b)
}

func (c *crashFS) WriteFileAtomic(p string, d []byte) error {
	if err := c.step(); err != nil {
		return err
	}
	return c.OSFS.WriteFileAtomic(p, d)
}

// healingFS runs heal once, on the first Remove, so a spec can break the
// journal write during the download and let the failure path write it again.
type healingFS struct {
	modelupdates.OSFS
	heal func()
	done bool
}

func (h *healingFS) Remove(p string) error {
	if !h.done {
		h.done = true
		h.heal()
	}
	return h.OSFS.Remove(p)
}

var _ = Describe("A model upgrade", func() {
	var (
		f   *fixture
		ctx context.Context
		rt  *fakeRuntime
	)

	upgrader := func(fs modelupdates.FS) *modelupdates.Upgrader {
		return &modelupdates.Upgrader{SystemState: f.systemState, ListGallery: f.list, Runtime: rt, FS: fs, Instance: "a"}
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(f.dir, name))
		Expect(err).ToNot(HaveOccurred())
		return string(data)
	}
	journal := func() *gallery.Journal {
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		return sc.Upgrade
	}
	leftovers := func() []string {
		var out []string
		entries, err := os.ReadDir(f.dir)
		Expect(err).ToNot(HaveOccurred())
		for _, e := range entries {
			if strings.Contains(e.Name(), ".next") || strings.Contains(e.Name(), ".tmp-") {
				out = append(out, e.Name())
			}
		}
		return out
	}

	var oldConfig string
	var oldDigest string

	BeforeEach(func() {
		f = newFixture()
		ctx = context.Background()
		rt = newRuntime("tiny")
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v1")))
		f.install("tiny")
		oldConfig = read("tiny.yaml")
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		oldDigest = sc.Origin.EntryDigest
	})

	It("downloads beside the old file, swaps, loads and keeps the old file", func() {
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		Expect(upgrader(nil).Upgrade(ctx, modelupdates.Request{OpID: "op", Name: "tiny"})).To(Succeed())

		Expect(read("tiny.gguf")).To(Equal("v2"))
		j := journal()
		Expect(j.Phase).To(Equal(gallery.PhaseConfirmed))
		Expect(j.RetainedFiles).To(ConsistOf("tiny.gguf.prev-" + strings.TrimPrefix(oldDigest, "sha256:")[:8]))
		Expect(read(j.RetainedFiles[0])).To(Equal("v1"))
		Expect(rt.calls).To(Equal([]string{"unload", "reload", "load"}))
		Expect(leftovers()).To(BeEmpty())

		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		Expect(sc.Origin.EntryDigest).ToNot(Equal(oldDigest))
		Expect(sc.Files[0].SHA256).To(Equal(gallery.SHA256Hex([]byte("v2"))))
		Expect(sc.ConfigSHA256).To(Equal(gallery.SHA256Hex([]byte(read("tiny.yaml")))))

		svc := f.service(modelupdates.Options{})
		Expect(itemOf(svc.CheckNow(ctx), "tiny").State).To(Equal(modelupdates.StateUpToDate))
	})

	It("does not confirm the old version that a request loaded while the files changed", func() {
		rt.dir = f.dir
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		var during bool
		fs := &windowFS{during: func() { during = rt.request("tiny", false) }}
		Expect(upgrader(fs).Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(Succeed())
		Expect(during).To(BeTrue(), "the request loaded the old version during the switch")
		Expect(rt.version["tiny"]).To(Equal("v2"), "the load check ran on the new weights")
		Expect(rt.calls).To(Equal([]string{"unload", "reload", "unload", "load"}))
	})

	It("refuses the loads of the model while the files change, when the runtime can", func() {
		g := &gatedRuntime{fakeRuntime: rt, blocked: map[string]bool{}}
		rt.dir = f.dir
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		var during bool
		fs := &windowFS{during: func() { during = g.request("tiny") }}
		u := upgrader(fs)
		u.Runtime = g
		Expect(u.Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(Succeed())
		Expect(during).To(BeFalse(), "the request was refused")
		Expect(rt.version["tiny"]).To(Equal("v2"))
		Expect(rt.calls).To(Equal([]string{"block", "unload", "reload", "unblock", "load"}))
		Expect(g.blocked).To(BeEmpty())
	})

	It("lets the loads through again after a rollback", func() {
		g := &gatedRuntime{fakeRuntime: rt, blocked: map[string]bool{}}
		rt.loadFails = 1
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		u := upgrader(nil)
		u.Runtime = g
		Expect(u.Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(HaveOccurred())
		Expect(g.blocked).To(BeEmpty())
		Expect(rt.calls).To(Equal([]string{"block", "unload", "reload", "unblock", "load", "block", "reload", "unblock", "load"}))
		Expect(rt.IsLoaded(ctx, "tiny")).To(BeTrue())
	})

	It("keeps the loads blocked when the gate cannot open, and opens it at the next try", func() {
		g := &gatedRuntime{fakeRuntime: rt, blocked: map[string]bool{}, unblockFails: 1}
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		u := upgrader(nil)
		u.Runtime = g
		err := u.Upgrade(ctx, modelupdates.Request{Name: "tiny"})
		Expect(err).To(MatchError(ContainSubstring("allow the loads of the model again")))
		Expect(g.blocked).To(BeEmpty(), "the retry of the end of the upgrade opened the gate")
		unblocks := 0
		for _, c := range rt.calls {
			if c == "unblock" {
				unblocks++
			}
		}
		Expect(unblocks).To(BeNumerically(">=", 2), "the failed call was not forgotten")
	})

	It("ends failed, not stuck rolling back, when the journal write fails after the download", func() {
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		path := gallery.SidecarPath(f.dir, "tiny")
		saved, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		fs := &healingFS{heal: func() {
			Expect(os.Remove(path)).To(Succeed())
			Expect(os.WriteFile(path, saved, 0600)).To(Succeed())
		}}
		u := upgrader(fs)
		u.Download = func(_ context.Context, _ []downloader.FileTask, _ func(string, string, string, float64)) error {
			// A directory in place of the journal makes the next write fail.
			Expect(os.Remove(path)).To(Succeed())
			Expect(os.Mkdir(path, 0700)).To(Succeed())
			return nil
		}
		Expect(u.Upgrade(ctx, modelupdates.Request{OpID: "op", Name: "tiny"})).To(HaveOccurred())

		j := journal()
		Expect(j.Phase).To(Equal(gallery.PhaseFailed))
		Expect(j.Terminal()).To(BeTrue())
		Expect(read("tiny.gguf")).To(Equal("v1"))
	})

	Context("cancelled", func() {
		// halfDownload writes part of each file, then waits for the cancel.
		halfDownload := func(ctx context.Context, tasks []downloader.FileTask, _ func(string, string, string, float64)) error {
			for _, t := range tasks {
				Expect(os.WriteFile(t.Destination+".partial", []byte("v"), 0600)).To(Succeed())
			}
			<-ctx.Done()
			return ctx.Err()
		}

		It("ends during the download with nothing changed, and drops the partial files", func() {
			f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
			cctx, cancel, _ := userCancellable(ctx)
			u := upgrader(nil)
			u.Download = func(c context.Context, tasks []downloader.FileTask, st func(string, string, string, float64)) error {
				go cancel()
				return halfDownload(c, tasks, st)
			}
			n := &upgradeNotifier{}
			u.Notifier = n
			err := u.Upgrade(cctx, modelupdates.Request{OpID: "op", Name: "tiny"})
			Expect(errors.Is(err, context.Canceled)).To(BeTrue())
			Expect(read("tiny.gguf")).To(Equal("v1"))
			Expect(rt.calls).To(BeEmpty(), "the model never stopped")
			Expect(leftovers()).To(BeEmpty())
			Expect(journal()).To(BeNil(), "a cancel is not a failed upgrade: the model has the journal it had")
			Expect(n.results).To(BeEmpty(), "the operation tells the cancel")
		})

		It("keeps the partial files on a pause, so the next upgrade resumes them", func() {
			f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
			cctx, _, pause := userCancellable(ctx)
			u := upgrader(nil)
			u.Download = func(c context.Context, tasks []downloader.FileTask, st func(string, string, string, float64)) error {
				go pause()
				return halfDownload(c, tasks, st)
			}
			Expect(u.Upgrade(cctx, modelupdates.Request{Name: "tiny"})).To(HaveOccurred())
			Expect(leftovers()).To(ConsistOf("tiny.gguf.next.partial"))
			Expect(read("tiny.gguf")).To(Equal("v1"))
		})

		It("keeps a finished file on a pause, so the next upgrade does not download it again", func() {
			first, second := f.weights("tiny.gguf", "v2"), f.weights("tiny-extra.gguf", "extra")
			e := tinyEntry(f, first)
			e.AdditionalFiles = append(e.AdditionalFiles, second)
			f.publish(e)
			cctx, _, pause := userCancellable(ctx)
			u := upgrader(nil)
			u.Download = func(c context.Context, tasks []downloader.FileTask, st func(string, string, string, float64)) error {
				Expect(tasks).To(HaveLen(2))
				Expect(os.WriteFile(tasks[0].Destination, []byte("v2"), 0600)).To(Succeed())
				Expect(os.WriteFile(tasks[1].Destination+".partial", []byte("e"), 0600)).To(Succeed())
				go pause()
				<-c.Done()
				return c.Err()
			}
			Expect(u.Upgrade(cctx, modelupdates.Request{Name: "tiny"})).To(HaveOccurred())
			Expect(leftovers()).To(ConsistOf("tiny.gguf.next", "tiny-extra.gguf.next.partial"))
			Expect(read("tiny.gguf.next")).To(Equal("v2"))
		})

		It("ends before the stop when the cancel came while the download finished", func() {
			f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
			cctx, cancel, _ := userCancellable(ctx)
			u := upgrader(nil)
			var reported []string
			u.Download = func(c context.Context, tasks []downloader.FileTask, st func(string, string, string, float64)) error {
				err := downloader.DownloadFilesWithContext(c, tasks, st)
				cancel()
				return err
			}
			err := u.Upgrade(cctx, modelupdates.Request{Name: "tiny", Progress: func(p modelupdates.Progress) { reported = append(reported, p.Phase) }})
			Expect(errors.Is(err, context.Canceled)).To(BeTrue())
			Expect(rt.calls).To(BeEmpty())
			Expect(read("tiny.gguf")).To(Equal("v1"))
			Expect(journal()).To(BeNil())
			Expect(reported).To(ContainElement(gallery.PhaseUnloading), "the status said it could no longer be cancelled")
		})

		It("runs to its end once the model stopped, and the rollback still loads the old version", func() {
			rt.loadFails = 1
			f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
			cctx, cancel, _ := userCancellable(ctx)
			fs := &windowFS{during: cancel}
			err := upgrader(fs).Upgrade(cctx, modelupdates.Request{Name: "tiny"})
			Expect(err).To(MatchError(ContainSubstring("could not load")))
			Expect(errors.Is(err, context.Canceled)).To(BeFalse())
			j := journal()
			Expect(j.Phase).To(Equal(gallery.PhaseRolledBack))
			Expect(j.Error).ToNot(ContainSubstring("rollback did not complete"))
			Expect(read("tiny.gguf")).To(Equal("v1"))
			Expect(rt.IsLoaded(ctx, "tiny")).To(BeTrue(), "the old version serves again")
		})
	})

	Context("with the model config edited while it runs", func() {
		edit := func() { patchDisk(f, "tiny", func(m map[string]any) { m["context_size"] = 1234 }) }
		edited := func() {
			GinkgoHelper()
			m := map[string]any{}
			Expect(yaml.Unmarshal([]byte(read("tiny.yaml")), &m)).To(Succeed())
			Expect(m["context_size"]).To(Equal(1234), "the edit is kept")
		}

		It("ends during the download with the edit kept and the model untouched", func() {
			f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
			u := upgrader(nil)
			u.Download = func(c context.Context, tasks []downloader.FileTask, st func(string, string, string, float64)) error {
				edit()
				return downloader.DownloadFilesWithContext(c, tasks, st)
			}
			Expect(u.Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(MatchError(modelupdates.ErrConfigChanged))
			edited()
			Expect(read("tiny.gguf")).To(Equal("v1"))
			Expect(journal().Phase).To(Equal(gallery.PhaseFailed))
			Expect(rt.calls).To(BeEmpty(), "the model never stopped")
			sc, err := gallery.ReadSidecar(f.dir, "tiny")
			Expect(err).ToNot(HaveOccurred())
			Expect(sc.ConfigSHA256).To(Equal(gallery.SHA256Hex([]byte(read("tiny.yaml")))))
		})

		It("refuses the switch when the edit lands after the stop, and keeps the edit", func() {
			f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
			rt.beforeUnload = edit
			Expect(upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(MatchError(modelupdates.ErrConfigChanged))
			edited()
			Expect(read("tiny.gguf")).To(Equal("v1"))
			Expect(journal().Phase).To(Equal(gallery.PhaseRolledBack))
			Expect(rt.IsLoaded(ctx, "tiny")).To(BeTrue(), "the old version serves again")
		})

		It("does not undo an edit made after the switch when the new version fails to load", func() {
			f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
			rt.loadFails = 1
			rt.beforeLoad = edit
			Expect(upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(MatchError(ContainSubstring("could not load")))
			edited()
			Expect(read("tiny.gguf")).To(Equal("v1"), "the old files are back")
			j := journal()
			Expect(j.Phase).To(Equal(gallery.PhaseRolledBack))
			Expect(j.Error).To(ContainSubstring("kept that edit"))
		})
	})

	It("reads the galleries again instead of a cached index another frontend may not have refreshed", func() {
		stale, err := f.list(ctx, true)
		Expect(err).ToNot(HaveOccurred())
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		u := upgrader(nil)
		var fresh []bool
		u.ListGallery = func(c context.Context, wantFresh bool) ([]*gallery.GalleryModel, error) {
			fresh = append(fresh, wantFresh)
			if !wantFresh {
				return stale, nil
			}
			return f.list(c, true)
		}
		Expect(u.Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(Succeed())
		Expect(fresh).To(Equal([]bool{true}))
		Expect(read("tiny.gguf")).To(Equal("v2"))
	})

	It("names the files relative to the models directory in its errors and its journal", func() {
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		u := upgrader(nil)
		u.Download = func(_ context.Context, tasks []downloader.FileTask, _ func(string, string, string, float64)) error {
			return &os.PathError{Op: "open", Path: tasks[0].Destination, Err: os.ErrPermission}
		}
		n := &upgradeNotifier{}
		u.Notifier = n
		err := u.Upgrade(ctx, modelupdates.Request{OpID: "op", Name: "tiny"})
		Expect(err).To(MatchError(os.ErrPermission), "errors.Is still sees the cause")
		Expect(err.Error()).To(ContainSubstring("open tiny.gguf.next"))
		Expect(err.Error()).ToNot(ContainSubstring(f.dir))
		Expect(journal().Error).ToNot(ContainSubstring(f.dir))
		Expect(n.results[0].Err).ToNot(ContainSubstring(f.dir))
	})

	It("takes the gallery config, with only the operator state of the disk, when the install wrote no record of its config", func() {
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		sc.Origin.ConfigYAML = ""
		Expect(gallery.WriteSidecar(f.dir, "tiny", sc)).To(Succeed())
		patchDisk(f, "tiny", func(m map[string]any) { m["context_size"] = 8192; m["pinned"] = true })
		e := tinyEntry(f, f.weights("tiny.gguf", "v1"))
		e.Overrides["context_size"] = 4096
		f.publish(e)
		Expect(upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(Succeed())
		m := map[string]any{}
		Expect(yaml.Unmarshal([]byte(read("tiny.yaml")), &m)).To(Succeed())
		Expect(m["context_size"]).To(Equal(4096), "the gallery's change applies")
		Expect(m["pinned"]).To(BeTrue(), "the operator state carries over")
	})

	It("loads a model nobody had loaded for the check, then unloads it", func() {
		rt = newRuntime()
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		Expect(upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(Succeed())
		Expect(rt.calls).To(Equal([]string{"reload", "load", "unload"}))
	})

	It("rolls back when the new version does not load, and the old one serves again", func() {
		rt.loadFails = 1
		n := &upgradeNotifier{}
		u := upgrader(nil)
		u.Notifier = n
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		err := u.Upgrade(ctx, modelupdates.Request{OpID: "op-1", Name: "tiny"})
		Expect(err).To(MatchError(ContainSubstring("could not load")))

		Expect(read("tiny.yaml")).To(Equal(oldConfig))
		Expect(read("tiny.gguf")).To(Equal("v1"))
		j := journal()
		Expect(j.Phase).To(Equal(gallery.PhaseRolledBack))
		Expect(j.Error).To(ContainSubstring("could not load"))
		Expect(j.Attempts).To(Equal(1))
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		Expect(sc.Origin.EntryDigest).To(Equal(oldDigest))
		Expect(sc.Files[0].SHA256).To(Equal(gallery.SHA256Hex([]byte("v1"))))
		Expect(rt.calls).To(Equal([]string{"unload", "reload", "load", "reload", "load"}))
		Expect(rt.IsLoaded(ctx, "tiny")).To(BeTrue(), "the old version was loaded again")
		Expect(leftovers()).To(BeEmpty())
		Expect(n.results).To(HaveLen(1))
		Expect(n.results[0].Failed).To(BeTrue())
		Expect(n.results[0].RolledBack).To(BeTrue())

		By("reporting the failure until a retry succeeds")
		svc := f.service(modelupdates.Options{})
		it := itemOf(svc.CheckNow(ctx), "tiny")
		Expect(it.State).To(Equal(modelupdates.StateFailed))
		Expect(u.Upgrade(ctx, modelupdates.Request{OpID: "op-2", Name: "tiny", Retry: true})).To(Succeed())
		Expect(journal().Attempts).To(Equal(2))
		Expect(read("tiny.gguf")).To(Equal("v2"))
	})

	It("leaves the old version untouched when a download fails its checksum", func() {
		bad := f.weights("tiny.gguf.src", "v2")
		bad.Filename = "tiny.gguf"
		bad.SHA256 = gallery.SHA256Hex([]byte("something else"))
		f.publish(tinyEntry(f, bad))
		err := upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny"})
		Expect(err).To(HaveOccurred())
		Expect(read("tiny.gguf")).To(Equal("v1"))
		Expect(read("tiny.yaml")).To(Equal(oldConfig))
		Expect(journal().Phase).To(Equal(gallery.PhaseFailed))
		Expect(rt.calls).To(BeEmpty(), "the model never stopped")
		Expect(leftovers()).To(BeEmpty())
	})

	It("applies a config-only change without downloading", func() {
		e := tinyEntry(f, f.weights("tiny.gguf", "v1"))
		e.Overrides["context_size"] = 4096
		f.publish(e)
		var downloads int
		u := upgrader(nil)
		u.Download = func(context.Context, []downloader.FileTask, func(string, string, string, float64)) error {
			downloads++
			return nil
		}
		Expect(u.Upgrade(ctx, modelupdates.Request{Name: "tiny", ConfigOnly: true})).To(Succeed())
		Expect(downloads).To(BeZero())
		m := map[string]any{}
		Expect(yaml.Unmarshal([]byte(read("tiny.yaml")), &m)).To(Succeed())
		Expect(m["context_size"]).To(Equal(4096))
		Expect(journal().RetainedFiles).To(BeEmpty())
	})

	It("refuses a config-only upgrade of an update that needs new files", func() {
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		var refused *modelupdates.ErrRefused
		Expect(errors.As(upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny", ConfigOnly: true}), &refused)).To(BeTrue())
		Expect(journal()).To(BeNil(), "a refusal before the start leaves no journal")
	})

	It("refuses an edited key the update changes until there is a choice, and keeps the operator state", func() {
		patchDisk(f, "tiny", func(m map[string]any) { m["context_size"] = 8192; m["pinned"] = true })
		e := tinyEntry(f, f.weights("tiny.gguf", "v1"))
		e.Overrides["context_size"] = 4096
		f.publish(e)
		var unresolved *modelupdates.ErrNeedsChoices
		Expect(errors.As(upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny"}), &unresolved)).To(BeTrue())
		Expect(journal()).To(BeNil())

		Expect(upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny", Choices: map[string]string{"context_size": modelupdates.ChoiceMine}})).To(Succeed())
		m := map[string]any{}
		Expect(yaml.Unmarshal([]byte(read("tiny.yaml")), &m)).To(Succeed())
		Expect(m["context_size"]).To(Equal(8192))
		Expect(m["pinned"]).To(BeTrue())

		By("still reading the kept value as an edit")
		svc := f.service(modelupdates.Options{})
		it := itemOf(svc.CheckNow(ctx), "tiny")
		Expect(it.State).To(Equal(modelupdates.StateUpToDate))
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		edits, err := modelupdates.DiffEdits([]byte(sc.Origin.ConfigYAML), []byte(read("tiny.yaml")), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(edits).To(HaveLen(1))
		Expect(edits[0].Key).To(Equal("context_size"))
	})

	It("refuses to replace a file another model uses", func() {
		Expect(os.WriteFile(filepath.Join(f.dir, "other.yaml"), []byte("name: other\nbackend: llama-cpp\nparameters:\n  model: tiny.gguf\n"), 0600)).To(Succeed())
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		var refused *modelupdates.ErrRefused
		err := upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny"})
		Expect(errors.As(err, &refused)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("used by other"))
		Expect(read("tiny.gguf")).To(Equal("v1"))
		Expect(journal().Phase).To(Equal(gallery.PhaseFailed))
	})

	It("refuses new weights without a checksum", func() {
		w := f.weights("tiny.gguf", "v2")
		w.SHA256 = ""
		w.URI = "https://example.com/tiny-v2.gguf"
		f.publish(tinyEntry(f, w))
		var refused *modelupdates.ErrRefused
		Expect(errors.As(upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny"}), &refused)).To(BeTrue())
		Expect(read("tiny.gguf")).To(Equal("v1"))
	})

	It("refuses when the backend of the new version is not installed", func() {
		u := upgrader(nil)
		u.BackendAvailable = func(string) (bool, error) { return false, nil }
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		var refused *modelupdates.ErrRefused
		Expect(errors.As(u.Upgrade(ctx, modelupdates.Request{Name: "tiny"}), &refused)).To(BeTrue())
		Expect(read("tiny.gguf")).To(Equal("v1"))
	})

	It("refuses when the disk cannot hold the new files", func() {
		u := upgrader(nil)
		u.ContentLength = func(context.Context, string) int64 { return 1 << 30 }
		u.FreeSpace = func(string) (uint64, error) { return 1 << 20, nil }
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		var refused *modelupdates.ErrRefused
		Expect(errors.As(u.Upgrade(ctx, modelupdates.Request{Name: "tiny"}), &refused)).To(BeTrue())
	})

	It("counts the size of a local source in the disk check", func() {
		u := upgrader(nil)
		u.FreeSpace = func(string) (uint64, error) { return 1, nil }
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		var refused *modelupdates.ErrRefused
		Expect(errors.As(u.Upgrade(ctx, modelupdates.Request{Name: "tiny"}), &refused)).To(BeTrue())
		Expect(refused.Reason).To(ContainSubstring("not enough free disk space"))
	})

	It("materializes the artifact of an entry that lists only an artifact while it stages", func() {
		var phases []string
		mat := &phaseMaterializer{ensure: func() {
			phases = append(phases, journal().Phase)
		}}
		u := upgrader(nil)
		u.InstallOptions = []gallery.InstallOption{gallery.WithArtifactMaterializer(mat)}
		f.publish(gallery.GalleryModel{
			Metadata: gallery.Metadata{Name: "tiny", Description: "v2"},
			Overrides: map[string]any{
				"backend":    "transformers",
				"artifacts":  []any{map[string]any{"name": "model", "target": "model", "source": map[string]any{"type": "huggingface", "repo": "owner/repo"}}},
				"parameters": map[string]any{"model": "owner/repo"},
			},
		})
		Expect(u.Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(Succeed())
		Expect(phases).To(Equal([]string{gallery.PhaseStaging}))
		Expect(read("tiny.yaml")).To(ContainSubstring("0123456789abcdef0123456789abcdef01234567"))
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		Expect(sc.ConfigSHA256).To(Equal(gallery.SHA256Hex([]byte(read("tiny.yaml")))))
	})

	It("does not lose an install commit to a hold written at the same time", func() {
		u := upgrader(nil)
		last := journalDigest(f)
		for i := range 15 {
			f.publish(tinyEntry(f, f.weights("tiny.gguf", fmt.Sprintf("round-%d", i))))
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				for k := range 30 {
					err := u.SetHold(ctx, "tiny", k%2 == 0)
					if err != nil && !errors.Is(err, modelupdates.ErrBusy) {
						Expect(err).ToNot(HaveOccurred())
					}
				}
			}()
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				for {
					err := gallery.InstallModelFromGallery(context.TODO(), f.galleries, nil, f.systemState, nil, f.galleries[0].Name+"@tiny",
						gallery.GalleryModel{}, func(string, string, string, float64) {}, false, false, false)
					if err == nil {
						return
					}
					Expect(errors.Is(err, gallery.ErrModelLocked)).To(BeTrue(), "only a busy model may refuse an install: %v", err)
				}
			}()
			wg.Wait()
			now := journalDigest(f)
			Expect(now).ToNot(Equal(last), "round %d: the hold wrote the gallery file of the old install over the new one", i)
			last = now
			sc, err := gallery.ReadSidecar(f.dir, "tiny")
			Expect(err).ToNot(HaveOccurred())
			Expect(sc.ConfigSHA256).To(Equal(gallery.SHA256Hex([]byte(read("tiny.yaml")))))
		}
	})

	It("tells a refusal before the journal as a failed upgrade, once, from the upgrader", func() {
		n := &upgradeNotifier{}
		u := upgrader(nil)
		u.Notifier = n
		Expect(u.Upgrade(ctx, modelupdates.Request{OpID: "op", Name: "tiny"})).To(MatchError(modelupdates.ErrUpToDate))
		Expect(n.results).To(HaveLen(1))
		Expect(n.results[0].OpID).To(Equal("op"))
		Expect(n.results[0].Failed).To(BeTrue())
		Expect(n.results[0].RolledBack).To(BeFalse())
		Expect(n.results[0].Err).To(ContainSubstring("up to date"))
	})

	It("says when there is nothing to upgrade, and refuses a second upgrade while one runs", func() {
		Expect(upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(MatchError(modelupdates.ErrUpToDate))
		Expect(upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny", Retry: true})).To(MatchError(modelupdates.ErrNothingFailed))
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		sc.Upgrade = &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseStaging}
		Expect(gallery.WriteSidecar(f.dir, "tiny", sc)).To(Succeed())
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		Expect(upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(MatchError(modelupdates.ErrBusy))
	})

	It("refuses a model with an older gallery file", func() {
		Expect(os.WriteFile(gallery.SidecarPath(f.dir, "tiny"), []byte("name: tiny\n"), 0600)).To(Succeed())
		Expect(upgrader(nil).Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(MatchError(modelupdates.ErrNotUpgradable))
	})

	// The commit makes four changes here: keep the old weights, put the new
	// weights in place, then the config (the journal writes are not
	// counted). A crash after any of them, followed by a restart, puts the
	// old version back.
	for step := 1; step <= 3; step++ {
		It("recovers the old version after a crash at commit step "+string(rune('0'+step)), func() {
			f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
			func() {
				defer func() {
					Expect(recover()).To(Equal(crash{}))
				}()
				_ = upgrader(&crashFS{at: step}).Upgrade(ctx, modelupdates.Request{Name: "tiny"})
			}()
			Expect(journal().Phase).To(Equal(gallery.PhaseCommitting))

			recovered, err := upgrader(nil).Recover(ctx, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(recovered).To(ConsistOf("tiny"))
			Expect(read("tiny.yaml")).To(Equal(oldConfig))
			Expect(read("tiny.gguf")).To(Equal("v1"))
			j := journal()
			Expect(j.Phase).To(Equal(gallery.PhaseRolledBack))
			Expect(j.Error).To(ContainSubstring("interrupted"))
			sc, err := gallery.ReadSidecar(f.dir, "tiny")
			Expect(err).ToNot(HaveOccurred())
			Expect(sc.Origin.EntryDigest).To(Equal(oldDigest))
			Expect(leftovers()).To(BeEmpty())
		})
	}

	It("rolls back in process when a commit step fails", func() {
		f.publish(tinyEntry(f, f.weights("tiny.gguf", "v2")))
		Expect(upgrader(&crashFS{at: 2, fail: true}).Upgrade(ctx, modelupdates.Request{Name: "tiny"})).To(MatchError(ContainSubstring("injected")))
		Expect(read("tiny.yaml")).To(Equal(oldConfig))
		Expect(read("tiny.gguf")).To(Equal("v1"))
		Expect(journal().Phase).To(Equal(gallery.PhaseRolledBack))
		Expect(rt.IsLoaded(ctx, "tiny")).To(BeTrue())
	})

	It("ends an upgrade a restart interrupted during the download as failed, with nothing changed", func() {
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(f.dir, "tiny.gguf.next"), []byte("half"), 0600)).To(Succeed())
		sc.Upgrade = &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseStaging,
			Files: []gallery.StagedFile{{Path: "tiny.gguf", Next: "tiny.gguf.next", Prev: "tiny.gguf.prev-x"}}}
		Expect(gallery.WriteSidecar(f.dir, "tiny", sc)).To(Succeed())
		_, err = upgrader(nil).Recover(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(journal().Phase).To(Equal(gallery.PhaseFailed))
		Expect(leftovers()).To(BeEmpty())
		Expect(read("tiny.gguf")).To(Equal("v1"))
	})

	It("leaves a running journal of another frontend alone", func() {
		sc, err := gallery.ReadSidecar(f.dir, "tiny")
		Expect(err).ToNot(HaveOccurred())
		sc.Upgrade = &gallery.Journal{Kind: gallery.JournalUpgrade, Phase: gallery.PhaseLoading, Instance: "b"}
		Expect(gallery.WriteSidecar(f.dir, "tiny", sc)).To(Succeed())
		recovered, err := upgrader(nil).Recover(ctx, func(j *gallery.Journal) bool { return j.Instance == "a" })
		Expect(err).ToNot(HaveOccurred())
		Expect(recovered).To(BeEmpty())
		Expect(journal().Phase).To(Equal(gallery.PhaseLoading))
	})

	It("updates a prompt template beside the old one", func() {
		base := filepath.Join(f.src, "tiny-base.yaml")
		write := func(content string) {
			Expect(os.WriteFile(base, []byte("name: tiny\nprompt_templates:\n- name: tiny-chat\n  content: \""+content+"\"\n"), 0600)).To(Succeed())
		}
		write("{{.Input}}")
		e := tinyEntry(f, f.weights("tiny.gguf", "v1"))
		e.Name = "tmpl"
		e.URL = "file://" + base
		f.publish(e)
		f.install("tmpl")
		write("Q: {{.Input}}")
		gallery.ForgetGalleryIndexes()
		u := upgrader(nil)
		Expect(u.Upgrade(ctx, modelupdates.Request{Name: "tmpl", ConfigOnly: true})).To(Succeed())
		Expect(read("tiny-chat.tmpl")).To(Equal("Q: {{.Input}}"))
		sc, err := gallery.ReadSidecar(f.dir, "tmpl")
		Expect(err).ToNot(HaveOccurred())
		Expect(sc.Upgrade.RetainedFiles).To(HaveLen(1))
		Expect(read(sc.Upgrade.RetainedFiles[0])).To(Equal("{{.Input}}"))
	})
})

// userCancellable is the context of a gallery operation: cancel removes the
// partial downloads, pause keeps them.
func userCancellable(parent context.Context) (context.Context, context.CancelFunc, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	return ctx, func() { cancel(downloader.ErrUserCancelled) }, func() { cancel(context.Canceled) }
}

type upgradeNotifier struct{ results []modelupdates.UpgradeResult }

func (n *upgradeNotifier) ModelUpgradeResult(_ context.Context, r modelupdates.UpgradeResult) error {
	n.results = append(n.results, r)
	return nil
}

// phaseMaterializer is an artifact materializer that tells when it runs and
// resolves every spec to a fixed revision.
type phaseMaterializer struct {
	ensure func()
}

func (m *phaseMaterializer) Ensure(_ context.Context, _ string, spec modelartifacts.Spec) (modelartifacts.Result, error) {
	m.ensure()
	spec.Resolved = &modelartifacts.Resolved{
		Endpoint: "https://huggingface.co",
		Revision: "0123456789abcdef0123456789abcdef01234567",
		CacheKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	return modelartifacts.Result{Spec: spec, RelativePath: ".artifacts/huggingface/key/snapshot"}, nil
}

// journalDigest is the entry digest the gallery file of the model "tiny"
// records for its install.
func journalDigest(f *fixture) string {
	sc, err := gallery.ReadSidecar(f.dir, "tiny")
	Expect(err).ToNot(HaveOccurred())
	return sc.Origin.EntryDigest
}
