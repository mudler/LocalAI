// Package upgraderuntest has fakes of the job paths and the checks of an
// upgrade run, for the tests of the packages that wire or serve the runs.
package upgraderuntest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// Jobs is the operation status table.
type Jobs struct {
	mu       sync.Mutex
	statuses map[string]*galleryop.OpStatus
}

// NewJobs returns an empty table.
func NewJobs() *Jobs { return &Jobs{statuses: map[string]*galleryop.OpStatus{}} }

func (f *Jobs) GetStatus(id string) *galleryop.OpStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.statuses[id]
	if !ok {
		return nil
	}
	c := *st
	return &c
}

func (f *Jobs) CancelOperation(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.statuses[id]
	if !ok {
		return errors.New("not found")
	}
	if !st.Cancellable {
		return galleryop.ErrNotCancellable
	}
	st.Cancelled, st.Processed, st.Cancellable, st.Message = true, true, false, "cancelled"
	return nil
}

// Set writes the status of a job.
func (f *Jobs) Set(id string, st galleryop.OpStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[id] = &st
}

// Complete ends a job without an error.
func (f *Jobs) Complete(id string) {
	f.Set(id, galleryop.OpStatus{Processed: true, Progress: 100, Message: galleryop.MessageCompleted})
}

// Checker lists the candidates of some kinds; tests add and remove them.
type Checker struct {
	mu    sync.Mutex
	kinds []upgraderun.Kind
	cands map[string]upgraderun.Candidate
}

// NewChecker lists the given kinds.
func NewChecker(kinds ...upgraderun.Kind) *Checker {
	return &Checker{kinds: kinds, cands: map[string]upgraderun.Candidate{}}
}

func (c *Checker) Kinds() []upgraderun.Kind { return c.kinds }

func (c *Checker) List(context.Context, bool) (upgraderun.Listing, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := upgraderun.Listing{Complete: true}
	for _, cand := range c.cands {
		l.Candidates = append(l.Candidates, cand)
	}
	return l, nil
}

// Add lists a candidate.
func (c *Checker) Add(cand upgraderun.Candidate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cand.Risks == nil {
		cand.Risks = []string{}
	}
	c.cands[cand.Key()] = cand
}

// Remove stops listing a candidate, as after its update.
func (c *Checker) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cands, key)
}

// Starter starts a job that waits until the test ends it, and records the
// actor of each start.
type Starter struct {
	mu     sync.Mutex
	jobs   *Jobs
	n      int
	byKey  map[string]string
	actors []string
	err    error
	gate   chan struct{}
}

// Block holds every later start until gate is closed or the start's context
// ends, as a starter whose operation is slow to be accepted.
func (s *Starter) Block(gate chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gate = gate
}

// SetErr makes every start fail with err; nil starts again.
func (s *Starter) SetErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// NewStarter starts jobs in jobs.
func NewStarter(jobs *Jobs) *Starter { return &Starter{jobs: jobs, byKey: map[string]string{}} }

func (s *Starter) Start(ctx context.Context, actor string, it upgraderun.Item) (string, error) {
	s.mu.Lock()
	gate := s.gate
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actors = append(s.actors, actor)
	if s.err != nil {
		return "", s.err
	}
	s.n++
	id := fmt.Sprintf("job-%s-%d", it.Name, s.n)
	s.byKey[it.Key] = id
	s.jobs.Set(id, galleryop.OpStatus{Phase: galleryop.PhaseQueued, Message: galleryop.PhaseQueued, Cancellable: true})
	return id, nil
}

// Job is the job started for an item key.
func (s *Starter) Job(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byKey[key]
}

// Actors are the actors of the starts, in order.
func (s *Starter) Actors() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.actors...)
}

// OpenSQLite opens a SQLite database in dir.
func OpenSQLite(dir string) (*gorm.DB, error) {
	return sqlitedb.Open("file:"+filepath.Join(dir, "runs.db")+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
}

// Harness is a service over fakes: one starter for every kind, a checker of
// the backends (gallery, library), one of the models and one of the add-ons.
type Harness struct {
	Service  *upgraderun.Service
	Jobs     *Jobs
	Starter  *Starter
	Backends *Checker
	Models   *Checker
	Addons   *Checker
}

// Options change a harness.
type Options struct {
	// Starters replace the fake starter of some kinds.
	Starters map[upgraderun.Kind]upgraderun.Starter
	// Jobs replaces the fake status table the runs read (the fake starter
	// still writes to the fake one).
	Jobs upgraderun.Jobs
	// Addons replaces the fake check of the add-ons.
	Addons upgraderun.Checker
}

// NewHarness builds a running service on a SQLite database in dir. Close the
// service when done.
func NewHarness(dir string, o Options) (*Harness, error) {
	db, err := OpenSQLite(dir)
	if err != nil {
		return nil, err
	}
	store, err := distributed.NewUpgradeRunStore(db)
	if err != nil {
		return nil, err
	}
	h := &Harness{
		Jobs:     NewJobs(),
		Backends: NewChecker(upgraderun.KindGallery, upgraderun.KindLibrary),
		Models:   NewChecker(upgraderun.KindModel),
		Addons:   NewChecker(upgraderun.KindAddon),
	}
	h.Starter = NewStarter(h.Jobs)
	starters := map[upgraderun.Kind]upgraderun.Starter{
		upgraderun.KindGallery: h.Starter, upgraderun.KindLibrary: h.Starter,
		upgraderun.KindModel: h.Starter, upgraderun.KindAddon: h.Starter,
	}
	for k, s := range o.Starters {
		starters[k] = s
	}
	checkers := []upgraderun.Checker{h.Backends, h.Models, h.Addons}
	if o.Addons != nil {
		checkers[2] = o.Addons
	}
	var jobs upgraderun.Jobs = h.Jobs
	if o.Jobs != nil {
		jobs = o.Jobs
	}
	svc, err := upgraderun.New(upgraderun.Options{
		Store: store, Jobs: jobs, Instance: "test",
		Starters:     starters,
		Checkers:     checkers,
		PollInterval: 10 * time.Millisecond, VerifyRetry: 10 * time.Millisecond, LostAfter: time.Second,
	})
	if err != nil {
		return nil, err
	}
	h.Service = svc
	return h, nil
}
