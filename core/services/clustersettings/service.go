package clustersettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/syncstate"
	"github.com/mudler/xlog"
)

const (
	// MapName is the syncstate subject namespace of the memory settings.
	MapName = "settings.memory"
	// MemoryKey is the key of the memory settings document.
	MemoryKey = "memory"

	// DefaultResync is how often a frontend re-reads the store, to repair a
	// missed delta.
	DefaultResync = 30 * time.Second

	// maxCASAttempts bounds the read-merge-write retries of one update.
	maxCASAttempts = 20
)

// Options configures a Service.
type Options struct {
	// Bus replicates changes to the other frontends. Nil: this frontend only.
	Bus messaging.Broadcaster
	// Store is the durable source of truth. Nil: in memory only.
	Store *DBStore
	// Apply puts a memory settings document into effect on this frontend
	// (the local ApplicationConfig and the services that read it). It is
	// called once per new revision, never concurrently.
	Apply func(config.RuntimeSettings)
	// Resync is the period of the store re-read; 0 uses DefaultResync.
	Resync time.Duration
}

// Service holds the cluster memory settings of one frontend.
type Service struct {
	opts Options
	m    *syncstate.SyncedMap[string, Entry]

	writeMu sync.Mutex // one local read-merge-write at a time

	applyMu sync.Mutex // serialises Apply and guards current
	current Entry

	stop chan struct{}
	done chan struct{}
}

// New builds the service, hydrates it from the store and applies the stored
// document. Seed it next with the frontend's env values.
func New(ctx context.Context, opts Options) (*Service, error) {
	if opts.Resync <= 0 {
		opts.Resync = DefaultResync
	}
	s := &Service{opts: opts, stop: make(chan struct{}), done: make(chan struct{})}
	cfg := syncstate.Config[string, Entry]{
		Name:    MapName,
		Key:     func(e Entry) string { return e.Key },
		Nats:    opts.Bus,
		OnApply: func(op string, _ string, e Entry) { s.applyEntry(e) },
	}
	if opts.Store != nil {
		cfg.Store = opts.Store
		cfg.Reconcile = opts.Resync
	}
	s.m = syncstate.New(cfg)
	if err := s.m.Start(ctx); err != nil {
		return nil, fmt.Errorf("clustersettings: starting: %w", err)
	}
	if e, ok := s.m.Get(MemoryKey); ok {
		s.applyEntry(e)
	}
	// A hydrate after a reconnect fires no OnApply. This callback runs after
	// the map's own (registration order), so the map is fresh.
	if r, ok := opts.Bus.(interface{ OnReconnect(func()) }); ok {
		r.OnReconnect(func() {
			if err := s.Resync(context.Background()); err != nil {
				xlog.Warn("clustersettings: resync after reconnect failed", "error", err)
			}
		})
	}
	go s.resyncLoop()
	return s, nil
}

// Close stops the service.
func (s *Service) Close() error {
	select {
	case <-s.stop:
		return nil
	default:
	}
	close(s.stop)
	<-s.done
	return s.m.Close()
}

func (s *Service) resyncLoop() {
	defer close(s.done)
	t := time.NewTicker(s.opts.Resync)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			if err := s.Resync(context.Background()); err != nil {
				xlog.Warn("clustersettings: resync failed", "error", err)
			}
		}
	}
}

// Resync applies the newest stored document when this frontend missed it.
func (s *Service) Resync(ctx context.Context) error {
	if s.opts.Store != nil {
		e, ok, err := s.opts.Store.Get(ctx, MemoryKey)
		if err != nil {
			return err
		}
		if ok {
			s.applyEntry(e)
		}
		return nil
	}
	if e, ok := s.m.Get(MemoryKey); ok {
		s.applyEntry(e)
	}
	return nil
}

func decode(e Entry) (config.RuntimeSettings, error) {
	var rs config.RuntimeSettings
	if e.Value == "" {
		return rs, nil
	}
	if err := json.Unmarshal([]byte(e.Value), &rs); err != nil {
		return rs, err
	}
	mem, _ := config.SplitMemorySettings(rs)
	return mem, nil
}

// applyEntry puts e into effect when it is newer than what this frontend
// runs. Deltas can arrive out of order or twice; the revision orders them.
func (s *Service) applyEntry(e Entry) {
	if e.Key != MemoryKey {
		return
	}
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	if e.Revision <= s.current.Revision {
		return
	}
	rs, err := decode(e)
	if err != nil {
		xlog.Warn("clustersettings: ignoring an unreadable memory settings document", "revision", e.Revision, "error", err)
		return
	}
	s.current = e
	if s.opts.Apply != nil {
		s.opts.Apply(rs)
	}
}

// Memory returns the memory settings in force and their revision (0 when
// none is stored).
func (s *Service) Memory() (config.RuntimeSettings, int64) {
	s.applyMu.Lock()
	e := s.current
	s.applyMu.Unlock()
	rs, _ := decode(e)
	return rs, e.Revision
}

func (s *Service) read(ctx context.Context) (Entry, bool, error) {
	if s.opts.Store != nil {
		return s.opts.Store.Get(ctx, MemoryKey)
	}
	e, ok := s.m.Get(MemoryKey)
	return e, ok, nil
}

// UpdateMemory merges patch (only its memory settings) over the stored
// document and stores the result for every frontend. With expected set, the
// update applies only to that revision and fails with ErrStaleRevision
// otherwise; without it, a concurrent update is merged and retried.
func (s *Service) UpdateMemory(ctx context.Context, patch config.RuntimeSettings, expected *int64, by string) (config.RuntimeSettings, int64, error) {
	patch, _ = config.SplitMemorySettings(patch)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for range maxCASAttempts {
		cur, _, err := s.read(ctx)
		if err != nil {
			return config.RuntimeSettings{}, 0, err
		}
		if expected != nil && *expected != cur.Revision {
			return config.RuntimeSettings{}, cur.Revision, fmt.Errorf("%w: revision %d, current %d", ErrStaleRevision, *expected, cur.Revision)
		}
		doc, err := decode(cur)
		if err != nil {
			return config.RuntimeSettings{}, 0, fmt.Errorf("clustersettings: reading the stored document: %w", err)
		}
		doc.MergeNonNil(patch)
		next, err := entry(doc, cur.Revision+1, by)
		if err != nil {
			return config.RuntimeSettings{}, 0, err
		}
		err = s.m.Set(ctx, next)
		if errors.Is(err, errConflict) {
			if expected != nil {
				return config.RuntimeSettings{}, cur.Revision, fmt.Errorf("%w: revision %d was replaced", ErrStaleRevision, *expected)
			}
			continue
		}
		if err != nil {
			return config.RuntimeSettings{}, 0, err
		}
		// Without a bus this frontend gets no delta of its own write.
		s.applyEntry(next)
		return doc, next.Revision, nil
	}
	return config.RuntimeSettings{}, 0, fmt.Errorf("clustersettings: too many concurrent updates, retry")
}

func entry(doc config.RuntimeSettings, rev int64, by string) (Entry, error) {
	b, err := json.Marshal(doc)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Key: MemoryKey, Value: string(b), Revision: rev, UpdatedBy: by, UpdatedAt: time.Now().UTC()}, nil
}

// Seed merges env (the memory settings that this frontend's env or flags
// set) into the stored document and reports whether it stored a new
// revision. The first writer of a key wins: env only adds the keys the
// document does not hold yet, so an empty store takes all of env and a
// partial one takes the missing keys. A key whose env value differs from the
// stored one is logged, and the stored value applies.
func (s *Service) Seed(ctx context.Context, env config.RuntimeSettings, by string) (bool, error) {
	env, _ = config.SplitMemorySettings(env)
	for range maxCASAttempts {
		cur, ok, err := s.read(ctx)
		if err != nil {
			return false, err
		}
		var stored config.RuntimeSettings
		if ok {
			if stored, err = decode(cur); err != nil {
				return false, fmt.Errorf("clustersettings: reading the stored document: %w", err)
			}
		}
		if diff := differing(env, stored); len(diff) > 0 {
			xlog.Warn("The memory settings of the cluster differ from this frontend's environment; the cluster value applies. Change them in Settings, not in the environment.", "keys", diff)
		}
		merged := env
		merged.MergeNonNil(stored)
		if len(merged.SetKeys()) == len(stored.SetKeys()) {
			if ok {
				s.applyEntry(cur)
			}
			return false, nil
		}
		next, err := entry(merged, cur.Revision+1, by)
		if err != nil {
			return false, err
		}
		err = s.m.Set(ctx, next)
		if errors.Is(err, errConflict) {
			continue // another frontend wrote first: merge with its document
		}
		if err != nil {
			return false, err
		}
		s.applyEntry(next)
		return true, nil
	}
	return false, fmt.Errorf("clustersettings: too many concurrent updates, retry")
}

// differing lists the keys env sets to a value other than the stored one.
func differing(env, stored config.RuntimeSettings) []string {
	var out []string
	storedKeys := stored.SetKeys()
	for _, key := range env.SetKeys() {
		if !slices.Contains(storedKeys, key) {
			continue
		}
		a, _ := json.Marshal(pick(env, key))
		b, _ := json.Marshal(pick(stored, key))
		if string(a) != string(b) {
			out = append(out, key)
		}
	}
	return out
}

func pick(rs config.RuntimeSettings, key string) any {
	var m map[string]any
	b, _ := json.Marshal(rs)
	_ = json.Unmarshal(b, &m)
	return m[key]
}
