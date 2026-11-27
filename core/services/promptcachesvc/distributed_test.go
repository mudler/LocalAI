package promptcachesvc_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/promptcache"
	"github.com/mudler/LocalAI/core/services/promptcachesvc"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// fleet is the workers of a cluster: each one is the worker half
// (promptcachesvc.Worker) on a disk of its own. It is also the Sender of the
// frontends, so a request reaches the worker as the control verb would.
type fleet struct {
	mu      sync.Mutex
	workers map[string]*fleetWorker
	order   []string
	sent    map[string]int
}

type fleetWorker struct {
	id, root    string
	online      bool
	incarnation string
	half        *promptcachesvc.Worker
	node        *promptcachesvc.Node
}

func newFleet() *fleet {
	return &fleet{workers: map[string]*fleetWorker{}, sent: map[string]int{}}
}

// start starts (or restarts) the worker process of id on its disk.
func (f *fleet) start(id string) *fleetWorker {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.workers[id]
	if !ok {
		w = &fleetWorker{id: id, root: filepath.Join(GinkgoT().TempDir(), "prompt-cache"), online: true}
		f.workers[id] = w
		f.order = append(f.order, id)
	}
	w.incarnation = fmt.Sprintf("%s-%d", id, time.Now().UnixNano())
	w.half = promptcachesvc.NewWorker(false, w.incarnation)
	w.node = promptcachesvc.NewNode(w.root, "", w.half.Settings,
		promptcachesvc.WithUsers(w.half.Users),
		promptcachesvc.WithStats(fixedStats{free: 500 * gib, total: 1000 * gib}))
	w.half.Attach(w.node)
	return w
}

func (f *fleet) setOnline(id string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workers[id].online = on
}

func (f *fleet) get(id string) *fleetWorker {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.workers[id]
}

func (f *fleet) reach(id string) (*fleetWorker, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := f.workers[id]
	if w == nil || !w.online {
		return nil, fmt.Errorf("%w: node %s", workerctl.ErrNoRoute, id)
	}
	f.sent[id]++
	return w, nil
}

func (f *fleet) sentTo(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent[id]
}

func (f *fleet) PromptCacheSync(ctx context.Context, id string, req workerctl.PromptCacheSyncRequest, _ time.Duration) (*workerctl.PromptCacheSyncReply, error) {
	w, err := f.reach(id)
	if err != nil {
		return nil, err
	}
	r := w.half.Sync(ctx, req)
	return &r, nil
}

func (f *fleet) PromptCacheUsage(ctx context.Context, id string, _ time.Duration) (*workerctl.PromptCacheUsageReply, error) {
	w, err := f.reach(id)
	if err != nil {
		return nil, err
	}
	r := w.half.Usage(ctx)
	return &r, nil
}

// peers is the registry view: every worker, with the incarnation it last
// reported and whether it is online.
func (f *fleet) peers(context.Context) ([]promptcachesvc.Peer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []promptcachesvc.Peer
	for _, id := range f.order {
		w := f.workers[id]
		out = append(out, promptcachesvc.Peer{ID: id, Name: "node-" + id, Incarnation: w.incarnation, Online: w.online})
	}
	return out, nil
}

// aged writes an entry and sets its last use to at.
func aged(root, model, ns string, at time.Time) string {
	p := writeEntry(root, model, ns)
	Expect(os.Chtimes(p, at, at)).To(Succeed())
	return p
}

var _ = Describe("the disk tier of a distributed cluster", func() {
	var (
		ctx      context.Context
		ledger   *promptcachesvc.DBLedger
		workers  *fleet
		settings config.PromptCacheConfig
		setMu    sync.Mutex
		a, b     *promptcachesvc.Distributed
		hourAgo  time.Time
	)

	current := func() workerctl.PromptCacheSettings {
		setMu.Lock()
		defer setMu.Unlock()
		return promptcachesvc.WireSettings(settings, true, 3)
	}

	// frontend is one frontend replica: its own Distributed on the shared
	// database and the shared fleet.
	frontend := func() *promptcachesvc.Distributed {
		return &promptcachesvc.Distributed{Ledger: ledger, Sender: workers, Peers: workers.peers, Settings: current}
	}

	BeforeEach(func() {
		ctx = context.Background()
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "cluster.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		Expect(err).ToNot(HaveOccurred())
		ledger, err = promptcachesvc.NewDBLedger(db)
		Expect(err).ToNot(HaveOccurred())
		workers = newFleet()
		settings = config.DefaultPromptCacheConfig()
		a, b = frontend(), frontend()
		hourAgo = time.Now().Add(-time.Hour)
	})

	It("brings a settings change made through one frontend to every worker, through the other", func() {
		w1, w2 := workers.start("1"), workers.start("2")
		Expect(w1.half.Settings().TTL).To(Equal(config.DefaultPromptCacheTTL), "a worker with no settings yet runs the defaults")

		Expect(a.Tick(ctx)).To(Succeed())
		authOn, users := w1.half.Users()
		Expect(authOn).To(BeTrue())
		Expect(users).To(Equal(3), "the user count reaches the worker for the automatic quota")

		setMu.Lock()
		settings.TTL = 2 * time.Hour
		settings.MaxSizeMiB = 512
		settings.DiskDisabled = true
		setMu.Unlock()
		Expect(b.Tick(ctx)).To(Succeed())
		for _, w := range []*fleetWorker{w1, w2} {
			Expect(w.half.Settings().TTL).To(Equal(2 * time.Hour))
			Expect(w.node.Policy().MaxBytes).To(Equal(int64(512) << 20))
			Expect(w.node.Enabled()).To(BeFalse())
		}

		sent := workers.sentTo("1")
		Expect(a.Tick(ctx)).To(Succeed())
		Expect(workers.sentTo("1")).To(Equal(sent), "a worker that is up to date is not asked again")
	})

	It("sends the settings again to a worker that restarted", func() {
		workers.start("1")
		setMu.Lock()
		settings.TTL = 3 * time.Hour
		setMu.Unlock()
		Expect(a.Tick(ctx)).To(Succeed())

		w := workers.start("1") // a new process: its settings are gone
		Expect(w.half.Settings().TTL).To(Equal(config.DefaultPromptCacheTTL))
		Expect(b.Tick(ctx)).To(Succeed())
		Expect(w.half.Settings().TTL).To(Equal(3 * time.Hour))
	})

	It("keeps the switch of a worker that refused the tier", func() {
		half := promptcachesvc.NewWorker(true, "i")
		half.Sync(ctx, workerctl.PromptCacheSyncRequest{Settings: new(promptcachesvc.WireSettings(config.DefaultPromptCacheConfig(), false, 0))})
		Expect(half.Settings().DiskDisabled).To(BeTrue())
	})

	It("purges every online worker at once, through the frontend that was asked", func() {
		w1, w2, w3 := workers.start("1"), workers.start("2"), workers.start("3")
		var files []string
		for _, w := range []*fleetWorker{w1, w2, w3} {
			files = append(files, aged(w.root, "m.gguf", "user:1", hourAgo), aged(w.root, "m.gguf", "user:2", hourAgo))
		}

		res, err := a.Purge(ctx, promptcachesvc.Scope{All: true})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Files).To(Equal(6))
		for _, f := range files {
			Expect(f).ToNot(BeAnExistingFile())
		}
	})

	It("purges a worker that was offline when it comes back, from the purge log", func() {
		w1, w2 := workers.start("1"), workers.start("2")
		mine1 := aged(w1.root, "m.gguf", "user:1", hourAgo)
		mine2 := aged(w2.root, "m.gguf", "user:1", hourAgo)
		other2 := aged(w2.root, "m.gguf", "user:2", hourAgo)
		model2 := aged(w2.root, "x.gguf", "user:2", hourAgo)
		workers.setOnline("2", false)

		_, err := a.Purge(ctx, promptcachesvc.NamespaceScope("user:1"))
		Expect(err).ToNot(HaveOccurred())
		_, err = a.Purge(ctx, promptcachesvc.ModelScope("x.gguf"))
		Expect(err).ToNot(HaveOccurred())
		Expect(mine1).ToNot(BeAnExistingFile())
		Expect(mine2).To(BeAnExistingFile(), "the worker is offline")

		// While it was away the worker kept serving the other model: an
		// entry used after the purge was asked for is not part of it.
		workers.setOnline("2", true)
		later := aged(w2.root, "x.gguf", "user:3", time.Now().Add(time.Hour))

		Expect(b.Tick(ctx)).To(Succeed())
		Expect(mine2).ToNot(BeAnExistingFile())
		Expect(model2).ToNot(BeAnExistingFile())
		Expect(other2).To(BeAnExistingFile())
		Expect(later).To(BeAnExistingFile())
	})

	It("does no harm when two frontends send the same purge at once", func() {
		w := workers.start("1")
		old := aged(w.root, "m.gguf", "user:1", hourAgo)
		workers.setOnline("1", false)
		_, err := a.Purge(ctx, promptcachesvc.Scope{All: true})
		Expect(err).ToNot(HaveOccurred())
		workers.setOnline("1", true)
		fresh := aged(w.root, "m.gguf", "user:2", time.Now().Add(time.Hour))

		var wg sync.WaitGroup
		for _, f := range []*promptcachesvc.Distributed{a, b, a, b} {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				_, err := f.SyncAll(ctx)
				Expect(err).ToNot(HaveOccurred())
			}()
		}
		wg.Wait()
		Expect(old).ToNot(BeAnExistingFile())
		Expect(fresh).To(BeAnExistingFile(), "a repeated purge deletes nothing newer than the request")

		rows, err := ledger.Nodes(ctx)
		Expect(err).ToNot(HaveOccurred())
		last, err := ledger.LastSeq(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(rows["1"].PurgedThrough).To(Equal(last))
	})

	It("owes a purge of everything to a worker whose purges the log dropped", func() {
		w := workers.start("1")
		Expect(a.Tick(ctx)).To(Succeed())
		old := aged(w.root, "m.gguf", "user:1", time.Now().Add(-48*time.Hour))
		workers.setOnline("1", false)
		_, err := a.Purge(ctx, promptcachesvc.NamespaceScope("user:9"))
		Expect(err).ToNot(HaveOccurred())

		a.Retention = time.Nanosecond
		time.Sleep(time.Millisecond)
		Expect(a.Tick(ctx)).To(Succeed())
		last, err := ledger.LastSeq(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(last).To(BeZero(), "the purge left the log")
		rows, err := ledger.Nodes(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(rows["1"].FullPurgeAt).ToNot(BeNil())

		workers.setOnline("1", true)
		fresh := aged(w.root, "m.gguf", "user:2", time.Now().Add(time.Hour))
		Expect(b.Tick(ctx)).To(Succeed())
		Expect(old).ToNot(BeAnExistingFile(), "everything used before the dropped purge goes")
		Expect(fresh).To(BeAnExistingFile())
		rows, err = ledger.Nodes(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(rows["1"].FullPurgeAt).To(BeNil())
	})

	It("reports every worker and the total; a worker that is away is in the report with why", func() {
		w1, w2 := workers.start("1"), workers.start("2")
		workers.start("3")
		aged(w1.root, "m.gguf", "user:1", hourAgo)
		aged(w2.root, "m.gguf", "user:1", hourAgo)
		aged(w2.root, "m.gguf", "user:2", hourAgo)
		for _, w := range []*fleetWorker{w1, w2} {
			_, err := w.node.Sweep(ctx)
			Expect(err).ToNot(HaveOccurred())
		}
		workers.setOnline("3", false)

		rep, err := promptcachesvc.Report(ctx, a, true, false, map[string]string{promptcache.ModelID("m.gguf"): "m"})
		Expect(err).ToNot(HaveOccurred())
		Expect(rep.Nodes).To(HaveLen(3))
		Expect(rep.Nodes[0].Node).To(Equal("node-1"))
		Expect(rep.Nodes[0].Enabled).To(BeTrue())
		Expect(rep.Nodes[1].Files).To(Equal(2))
		Expect(rep.Nodes[1].Models[0].Model).To(Equal("m"))
		Expect(rep.Nodes[2].Error).To(Equal("offline"))
		Expect(rep.Total.Files).To(Equal(3))
		Expect(rep.Total.Nodes).To(Equal(2))
		Expect(rep.Total.Unreachable).To(Equal(1))
		Expect(rep.Total.CapBytes).To(Equal(rep.Nodes[0].Status.EffectiveCapBytes + rep.Nodes[1].Status.EffectiveCapBytes))
	})

	It("forgets the cursor of a worker that left the cluster", func() {
		workers.start("1")
		Expect(a.Tick(ctx)).To(Succeed())
		Expect(ledger.ForgetNodes(ctx, []string{"2"})).To(Succeed())
		rows, err := ledger.Nodes(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(rows).ToNot(HaveKey("1"))
		Expect(ledger.ForgetNodes(ctx, nil)).To(Succeed(), "an empty registry read forgets nothing")
	})

	It("refuses a purge that names nothing", func() {
		_, err := a.Purge(ctx, promptcachesvc.Scope{})
		Expect(err).To(MatchError(promptcachesvc.ErrNoScope))
	})
})

var _ = Describe("the settings a worker gets", func() {
	It("has one version for one set of values and keeps the defaults for zero values", func() {
		c := config.DefaultPromptCacheConfig()
		v1 := promptcachesvc.WireSettings(c, false, 0)
		Expect(promptcachesvc.WireSettings(c, false, 0).Version).To(Equal(v1.Version))
		Expect(promptcachesvc.WireSettings(c, true, 2).Version).ToNot(Equal(v1.Version), "the user count feeds the automatic quota")
		Expect(promptcachesvc.ConfigOf(v1)).To(Equal(c))
		Expect(promptcachesvc.ConfigOf(workerctl.PromptCacheSettings{})).To(Equal(c))
	})
})
