package agentpool

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/mudler/LocalAGI/webui/collections"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/localrecall/rag/engine"
	"github.com/mudler/xlog"
	"golang.org/x/sync/singleflight"
)

// Shared mode: with the PostgreSQL vector engine, the database is the source
// of truth for which collections exist. The in-process backend keeps the
// collections a frontend has opened in a map, which is only a cache. A
// collection created through one frontend is absent from that map on every
// other frontend, so without this layer a lookup on another frontend answers
// "collection not found" until that frontend restarts.
//
// sharedCollections wraps the in-process backend and:
//   - answers lists from the shared registry,
//   - re-checks the registry before it reports a collection as not found, and
//     opens a collection that exists there but not in the local cache,
//   - drops a cached collection once the registry no longer has it,
//   - publishes create/reset events so that the other frontends refresh at once
//     instead of waiting for the next re-check.

const (
	// collectionRecheckInterval bounds how long a frontend keeps serving a
	// cached collection that another frontend removed from the registry.
	collectionRecheckInterval = 5 * time.Second
	// collectionRegistryTimeout bounds each query to the shared registry.
	collectionRegistryTimeout = 5 * time.Second

	collectionOpCreate = "create"
	collectionOpReset  = "reset"
)

// collectionRegistry is the shared record of which collections exist.
type collectionRegistry interface {
	Exists(ctx context.Context, name string) (bool, error)
	List(ctx context.Context) ([]string, error)
}

// postgresCollectionRegistry reads the collection registry of the PostgreSQL
// vector engine.
type postgresCollectionRegistry struct{ databaseURL string }

func (r postgresCollectionRegistry) Exists(ctx context.Context, name string) (bool, error) {
	return engine.PostgresCollectionExists(ctx, r.databaseURL, name)
}

func (r postgresCollectionRegistry) List(ctx context.Context) ([]string, error) {
	return engine.ListPostgresCollections(ctx, r.databaseURL)
}

// collectionEvent is the payload published on
// messaging.SubjectCacheInvalidateCollection.
type collectionEvent struct {
	Name string `json:"name"`
	Op   string `json:"op"`
}

// sharedCollections implements collections.Backend on top of the in-process
// backend and the shared registry.
type sharedCollections struct {
	inner    collections.Backend
	state    *collections.State
	registry collectionRegistry
	bus      messaging.Broadcaster // nil without a message bus
	recheck  time.Duration

	opens singleflight.Group

	mu      sync.Mutex
	checked map[string]time.Time // last time the registry confirmed a name
	sub     messaging.Subscription
}

var _ collections.Backend = (*sharedCollections)(nil)

// newSharedCollections wraps inner. bus may be nil: the frontends then
// converge through the registry alone, at the latest after the re-check
// interval.
func newSharedCollections(inner collections.Backend, state *collections.State, registry collectionRegistry, bus messaging.Broadcaster) *sharedCollections {
	s := &sharedCollections{
		inner:    inner,
		state:    state,
		registry: registry,
		bus:      bus,
		recheck:  collectionRecheckInterval,
		checked:  map[string]time.Time{},
	}
	if bus != nil {
		sub, err := messaging.SubscribeJSON(bus, messaging.SubjectCacheInvalidateCollectionAll, s.onEvent)
		if err != nil {
			xlog.Warn("Failed to subscribe to collection events; relying on registry re-checks", "error", err)
		} else {
			s.sub = sub
		}
	}
	return s
}

// Close stops listening for collection events.
func (s *sharedCollections) Close() {
	if s.sub != nil {
		_ = s.sub.Unsubscribe()
	}
}

// onEvent handles an event from any frontend, including this one. It never
// publishes, so it cannot loop.
func (s *sharedCollections) onEvent(evt collectionEvent) {
	if evt.Name == "" {
		return
	}
	s.forget(evt.Name)
	if evt.Op == collectionOpCreate {
		// Nothing to drop. The next lookup opens it from the registry.
		return
	}
	s.reconcile(evt.Name)
}

func (s *sharedCollections) publish(name, op string) {
	if s.bus == nil {
		return
	}
	if err := s.bus.Publish(messaging.SubjectCacheInvalidateCollection(name), collectionEvent{Name: name, Op: op}); err != nil {
		xlog.Warn("Failed to publish collection event", "collection", name, "op", op, "error", err)
	}
}

func (s *sharedCollections) fresh(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.checked[name]
	return ok && time.Since(t) < s.recheck
}

func (s *sharedCollections) markChecked(name string) {
	s.mu.Lock()
	s.checked[name] = time.Now()
	s.mu.Unlock()
}

func (s *sharedCollections) forget(name string) {
	s.mu.Lock()
	delete(s.checked, name)
	s.mu.Unlock()
}

func (s *sharedCollections) cached(name string) bool {
	s.state.Mu.RLock()
	defer s.state.Mu.RUnlock()
	kb, ok := s.state.Collections[name]
	return ok && kb != nil
}

// drop forgets a collection that the registry no longer has and releases its
// resources.
func (s *sharedCollections) drop(name string) {
	s.forget(name)
	s.state.Mu.Lock()
	kb, ok := s.state.Collections[name]
	delete(s.state.Collections, name)
	s.state.Mu.Unlock()
	if !ok {
		return
	}
	s.state.SourceManager.UnregisterCollection(name)
	if kb != nil {
		kb.Close()
	}
	xlog.Info("Dropped a collection that no longer exists in the shared registry", "collection", name)
}

// reconcile drops the cached entry when the registry no longer has the name.
// A registry error keeps the entry: a database blip must not become a 404.
func (s *sharedCollections) reconcile(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), collectionRegistryTimeout)
	defer cancel()
	exists, err := s.registry.Exists(ctx, name)
	if err != nil {
		xlog.Warn("Could not check the collection registry", "collection", name, "error", err)
		return
	}
	if !exists {
		s.drop(name)
	}
}

// ensure makes the local cache agree with the registry for one collection. It
// returns a "collection not found" error when the registry does not have it.
func (s *sharedCollections) ensure(name string) error {
	if s.fresh(name) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), collectionRegistryTimeout)
	defer cancel()
	exists, err := s.registry.Exists(ctx, name)
	if err != nil {
		// Keep serving what this frontend already has.
		xlog.Warn("Could not check the collection registry", "collection", name, "error", err)
		return nil
	}
	if !exists {
		s.drop(name)
		return fmt.Errorf("collection not found: %s", name)
	}
	if !s.cached(name) {
		// Concurrent first lookups open the collection (and its connection
		// pool) once.
		_, err, _ := s.opens.Do(name, func() (any, error) {
			if s.cached(name) {
				return nil, nil
			}
			if _, ok := s.state.EnsureCollection(name); !ok {
				return nil, fmt.Errorf("failed to open collection %s from the shared registry", name)
			}
			return nil, nil
		})
		if err != nil {
			return err
		}
	}
	s.markChecked(name)
	return nil
}

func (s *sharedCollections) ListCollections() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), collectionRegistryTimeout)
	defer cancel()
	return s.registry.List(ctx)
}

func (s *sharedCollections) CreateCollection(name string) error {
	if err := s.inner.CreateCollection(name); err != nil {
		return err
	}
	s.markChecked(name)
	s.publish(name, collectionOpCreate)
	return nil
}

func (s *sharedCollections) Reset(collection string) error {
	if err := s.ensure(collection); err != nil {
		return err
	}
	if err := s.inner.Reset(collection); err != nil {
		return err
	}
	// The inner backend dropped its entry. The registry decides whether the
	// collection still exists, so the next lookup opens it again.
	s.forget(collection)
	s.publish(collection, collectionOpReset)
	return nil
}

func (s *sharedCollections) Upload(collection, filename string, fileBody io.Reader) (string, error) {
	if err := s.ensure(collection); err != nil {
		return "", err
	}
	return s.inner.Upload(collection, filename, fileBody)
}

func (s *sharedCollections) ListEntries(collection string) ([]string, error) {
	if err := s.ensure(collection); err != nil {
		return nil, err
	}
	return s.inner.ListEntries(collection)
}

func (s *sharedCollections) GetEntryContent(collection, entry string) (string, int, error) {
	if err := s.ensure(collection); err != nil {
		return "", 0, err
	}
	return s.inner.GetEntryContent(collection, entry)
}

func (s *sharedCollections) Search(collection, query string, maxResults int) ([]collections.SearchResult, error) {
	if err := s.ensure(collection); err != nil {
		return nil, err
	}
	return s.inner.Search(collection, query, maxResults)
}

func (s *sharedCollections) DeleteEntry(collection, entry string) ([]string, error) {
	if err := s.ensure(collection); err != nil {
		return nil, err
	}
	return s.inner.DeleteEntry(collection, entry)
}

func (s *sharedCollections) AddSource(collection, url string, intervalMin int) error {
	if err := s.ensure(collection); err != nil {
		return err
	}
	return s.inner.AddSource(collection, url, intervalMin)
}

func (s *sharedCollections) RemoveSource(collection, url string) error {
	if err := s.ensure(collection); err != nil {
		return err
	}
	return s.inner.RemoveSource(collection, url)
}

func (s *sharedCollections) ListSources(collection string) ([]collections.SourceInfo, error) {
	if err := s.ensure(collection); err != nil {
		return nil, err
	}
	return s.inner.ListSources(collection)
}

func (s *sharedCollections) EntryExists(collection, entry string) bool {
	if err := s.ensure(collection); err != nil {
		return false
	}
	return s.inner.EntryExists(collection, entry)
}

func (s *sharedCollections) GetEntryFilePath(collection, entry string) (string, error) {
	if err := s.ensure(collection); err != nil {
		return "", err
	}
	return s.inner.GetEntryFilePath(collection, entry)
}
