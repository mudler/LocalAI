package upgraderun_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	. "github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// fakeJobs is the operation status table every job path writes to (the
// gallery service in the real server). It is shared by every frontend of a
// test, as the bus replicates the statuses between real frontends.
type fakeJobs struct {
	mu       sync.Mutex
	statuses map[string]*galleryop.OpStatus
	cancels  []string
}

func newFakeJobs() *fakeJobs { return &fakeJobs{statuses: map[string]*galleryop.OpStatus{}} }

func (f *fakeJobs) GetStatus(id string) *galleryop.OpStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.statuses[id]
	if !ok {
		return nil
	}
	c := *st
	return &c
}

func (f *fakeJobs) CancelOperation(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.statuses[id]
	if !ok {
		return errors.New("not found")
	}
	if !st.Cancellable {
		return galleryop.ErrNotCancellable
	}
	f.cancels = append(f.cancels, id)
	st.Cancelled, st.Processed, st.Cancellable, st.Message = true, true, false, "cancelled"
	return nil
}

func (f *fakeJobs) set(id string, st galleryop.OpStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[id] = &st
}

func (f *fakeJobs) drop(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.statuses, id)
}

func (f *fakeJobs) cancelled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cancels...)
}

func (f *fakeJobs) complete(id string) {
	f.set(id, galleryop.OpStatus{Processed: true, Progress: 100, Message: galleryop.MessageCompleted})
}

func (f *fakeJobs) fail(id, phase, msg string) {
	f.set(id, galleryop.OpStatus{Processed: true, Phase: phase, Error: errors.New(msg), Message: "error: " + msg})
}

// fakeStarter starts a job that waits until the test ends it.
type fakeStarter struct {
	mu     sync.Mutex
	jobs   *fakeJobs
	n      int
	byKey  map[string]string
	order  []string
	actors []string
	err    error
	// gate, when set, holds every start until it is closed.
	gate chan struct{}
	// returned, when set, runs as a start returns.
	returned func()
}

// onReturn runs fn as each later start returns.
func (s *fakeStarter) onReturn(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.returned = fn
}

// block holds every later start until gate is closed.
func (s *fakeStarter) block(gate chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gate = gate
}

func newFakeStarter(jobs *fakeJobs) *fakeStarter {
	return &fakeStarter{jobs: jobs, byKey: map[string]string{}}
}

func (s *fakeStarter) Start(ctx context.Context, actor string, it Item) (string, error) {
	s.mu.Lock()
	gate := s.gate
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		fn := s.returned
		s.mu.Unlock()
		if fn != nil {
			fn()
		}
	}()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return "", s.err
	}
	s.n++
	id := fmt.Sprintf("job-%s-%d", it.Name, s.n)
	s.byKey[it.Key] = id
	s.order = append(s.order, it.Key)
	s.actors = append(s.actors, actor)
	s.jobs.set(id, galleryop.OpStatus{Phase: galleryop.PhaseQueued, Message: galleryop.PhaseQueued, Cancellable: true})
	return id, nil
}

func (s *fakeStarter) job(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byKey[key]
}

func (s *fakeStarter) actorList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.actors...)
}

func (s *fakeStarter) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *fakeStarter) started() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

// fakeChecker lists candidates the test controls.
type fakeChecker struct {
	mu         sync.Mutex
	kinds      []Kind
	cands      map[string]Candidate
	incomplete bool
	throttled  bool
	fresh      int
	// gate, when set, blocks fresh checks until it is closed.
	gate chan struct{}
}

func newFakeChecker(kinds ...Kind) *fakeChecker {
	return &fakeChecker{kinds: kinds, cands: map[string]Candidate{}}
}

func (c *fakeChecker) Kinds() []Kind { return c.kinds }

func (c *fakeChecker) List(ctx context.Context, fresh bool) (Listing, error) {
	c.mu.Lock()
	gate := c.gate
	c.mu.Unlock()
	if fresh && gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return Listing{}, ctx.Err()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if fresh {
		c.fresh++
	}
	l := Listing{Complete: !c.incomplete, Throttled: c.throttled}
	for _, cand := range c.cands {
		l.Candidates = append(l.Candidates, cand)
	}
	return l, nil
}

func (c *fakeChecker) add(cand Candidate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cand.To == "" {
		cand.To = "2"
	}
	if cand.From == "" {
		cand.From = "1"
	}
	c.cands[cand.Key()] = cand
}

func (c *fakeChecker) remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cands, key)
}

func (c *fakeChecker) setIncomplete(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.incomplete = v
}

func (c *fakeChecker) setGate(g chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gate = g
}

// harness is one frontend: a Service with fakes for the job paths and the
// checks.
type harness struct {
	svc      *Service
	store    *distributed.UpgradeRunStore
	db       *gorm.DB
	jobs     *fakeJobs
	starter  *fakeStarter
	backends *fakeChecker
	models   *fakeChecker
	addons   *fakeChecker
	changes  chan Change
}

func openSQLite() *gorm.DB {
	path := filepath.Join(GinkgoT().TempDir(), "runs.db")
	db, err := sqlitedb.Open("file:"+path+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

type harnessOpts struct {
	db       *gorm.DB
	jobs     *fakeJobs
	bus      messaging.Broadcaster
	instance string
	limit    int
	toucher  OperationToucher
	// recoveredIdle is RecoveredIdleAfter; zero keeps the default.
	recoveredIdle time.Duration
	// shared fakes, for a second frontend of the same cluster
	starter  *fakeStarter
	backends *fakeChecker
	models   *fakeChecker
	addons   *fakeChecker
}

func newHarness(o harnessOpts) *harness {
	if o.db == nil {
		o.db = openSQLite()
	}
	if o.jobs == nil {
		o.jobs = newFakeJobs()
	}
	if o.instance == "" {
		o.instance = "frontend-a"
	}
	if o.limit == 0 {
		o.limit = 2
	}
	if o.starter == nil {
		o.starter = newFakeStarter(o.jobs)
	}
	if o.backends == nil {
		o.backends = newFakeChecker(KindGallery, KindLibrary)
	}
	if o.models == nil {
		o.models = newFakeChecker(KindModel)
	}
	if o.addons == nil {
		o.addons = newFakeChecker(KindAddon)
	}
	store, err := distributed.NewUpgradeRunStore(o.db)
	Expect(err).NotTo(HaveOccurred())
	h := &harness{
		store: store, db: o.db, jobs: o.jobs, starter: o.starter,
		backends: o.backends, models: o.models, addons: o.addons, changes: make(chan Change, 1024),
	}
	limit := o.limit
	opts := Options{
		Store: store, Bus: o.bus, Instance: o.instance, Jobs: o.jobs,
		Starters: map[Kind]Starter{KindGallery: o.starter, KindLibrary: o.starter, KindModel: o.starter, KindAddon: o.starter},
		Checkers: []Checker{o.backends, o.models, o.addons},
		Toucher:  o.toucher,
		Config:   func() Config { return Config{BackendLimit: limit, ModelLimit: 1} },

		PollInterval: 5 * time.Millisecond, SweepInterval: 20 * time.Millisecond, LostAfter: 300 * time.Millisecond,
		TouchInterval: 5 * time.Millisecond, LockCheckInterval: 10 * time.Millisecond,
		VerifyTries: 3, VerifyRetry: 5 * time.Millisecond,
		RecoveredIdleAfter: o.recoveredIdle,
	}
	h.svc, err = New(opts)
	Expect(err).NotTo(HaveOccurred())
	h.svc.AddObserver(ObserverFunc(func(c Change) {
		select {
		case h.changes <- c:
		default:
		}
	}))
	DeferCleanup(h.svc.Close)
	return h
}

func (h *harness) get(id string) Run {
	r, err := h.svc.Get(context.Background(), id)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return r
}

func (h *harness) phase(id, key string) Phase {
	r, err := h.svc.Get(context.Background(), id)
	if err != nil {
		return ""
	}
	it := r.Item(key)
	if it == nil {
		return ""
	}
	return it.Phase
}

func (h *harness) item(id, key string) Item {
	r := h.get(id)
	it := r.Item(key)
	ExpectWithOffset(1, it).NotTo(BeNil())
	return *it
}

func (h *harness) state(id string) RunState {
	r, err := h.svc.Get(context.Background(), id)
	if err != nil {
		return ""
	}
	return r.State
}

// jobOf waits for the job of an item.
func (h *harness) jobOf(key string) string {
	var id string
	EventuallyWithOffset(1, func() string { id = h.starter.job(key); return id }).ShouldNot(BeEmpty(), "job of %s", key)
	return id
}

func (h *harness) start(items ...StartItem) Run {
	r, err := h.svc.Start(context.Background(), "admin", StartRequest{Items: items})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return r
}

func si(kind Kind, name string) StartItem { return StartItem{Kind: kind, Name: name, ExpectedTo: "2"} }
