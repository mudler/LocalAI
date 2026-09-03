package openresponses

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/syncstate"
	"github.com/mudler/xlog"
)

// syncStateName is the syncstate namespace for replicated response metadata.
// It becomes the broadcast subject "state.responses-metadata.delta" (the '.' in
// the name is sanitized to '-', so the subject keeps three tokens).
const syncStateName = "responses.metadata"

// ErrResponseNotLocal is returned by the stream-resume accessors when the
// response exists in the cluster but its resume buffer lives in another
// replica's process memory.
//
// The buffer is a byte log plus a live notification channel: neither can be
// replicated without shipping every generated token over the message bus, so a
// resume request that lands on the wrong replica cannot be served. Returning a
// distinct error lets the HTTP layer say exactly that instead of handing the
// client an empty event list that looks like a completed stream. It is
// deliberately NOT ErrOffsetLost: that means "the buffer evicted the events you
// asked for" (a retention decision on the owning replica) and a client may
// reasonably give up on the stream. This one means "ask the replica that owns
// it", which is a routing problem, and the two must stay distinguishable.
var ErrResponseNotLocal = errors.New("response is owned by another replica: its stream resume buffer is not available here")

// syncedResponse is the replicable projection of a StoredResponse.
//
// Only what a peer can act on is carried: the request (so previous_response_id
// chaining can replay it), the response resource (so polling returns real
// state), the expiry, and the owning replica. Everything process-local is
// excluded by construction - the CancelFunc is a function pointer, EventsChan is
// a live channel, and StreamEvents is an unbounded byte log. Cancel is therefore
// delegated to the owner over the bus rather than replicated (see
// delegateCancel), and stream resume is refused off-owner (ErrResponseNotLocal).
type syncedResponse struct {
	ID            string                       `json:"id"`
	OwnerReplica  string                       `json:"owner_replica"`
	Owner         string                       `json:"owner,omitempty"`
	Request       *schema.OpenResponsesRequest `json:"request,omitempty"`
	Response      *schema.ORResponseResource   `json:"response,omitempty"`
	StoredAt      time.Time                    `json:"stored_at"`
	ExpiresAt     *time.Time                   `json:"expires_at,omitempty"`
	StreamEnabled bool                         `json:"stream_enabled,omitempty"`
	IsBackground  bool                         `json:"is_background,omitempty"`
}

// responseCancelEvent is the wire envelope broadcast on SubjectResponseCancel.
// Origin lets the publisher skip its own echo: it has already cancelled locally
// (or knows it holds nothing to cancel) before publishing.
type responseCancelEvent struct {
	ResponseID string `json:"response_id"`
	Origin     string `json:"origin"`
}

// EnableDistributed turns on cross-replica replication for this store.
//
// It is called once during route registration when the application is in
// distributed mode, before any request is served. Standalone deployments never
// call it and keep the store exactly as it was: a process-local map with no
// broadcast, no subscription and no behavioural change.
//
// Two independent legs are wired:
//
//   - a syncstate.SyncedMap carrying response metadata, so a GET or a
//     previous_response_id lookup that a round-robin load balancer sends to a
//     replica which did not create the response still resolves;
//   - a wildcard subscription on the response-cancel subject, so a cancel that
//     lands on the wrong replica still reaches the context.CancelFunc.
//
// The SyncedMap is backed by a durable Store, and that is what makes a gap in
// the carrier survivable. Both carriers deliver at most once to CONNECTED
// subscribers and neither replays: a replica whose subscription was down while a
// response was created never receives that delta, and with deltas as the only
// convergence path it would answer 404 for that response forever while its peers
// answered 200. The Store is what the reconnect re-hydrate and the periodic
// reconcile actually read, so the gap closes rather than becoming permanent.
//
// What is NOT durable is unchanged and deliberate: the resume buffer and the
// CancelFunc never leave the owning replica (see syncedResponse), so the write
// through is one row per response state change, not one per generated token.
//
// The store is required rather than optional. This is called only from the
// distributed branch of route registration, so a nil store there is a wiring bug
// and not a deployment shape; tolerating it would silently restore the
// deltas-only map this parameter exists to replace, and a non-nil interface
// wrapping a nil pointer would instead surface as a panic on a request.
func (s *ResponseStore) EnableDistributed(ctx context.Context, bus messaging.Broadcaster,
	replicaID string, store *distributed.ResponseMetadataStore) error {
	if store == nil {
		return errors.New("enabling cross-replica Open Responses: the store parameter is nil, so a reconnecting replica would have nothing to re-hydrate from")
	}
	if bus == nil {
		return nil
	}

	// The subscription callbacks and the delegated-cancel publisher outlive the
	// caller's ctx (which is startup-scoped), so hold a context that only Close
	// cancels.
	lifeCtx, lifeCancel := context.WithCancel(context.Background()) //#nosec G118 -- cancelled in Close()

	synced := syncstate.New(syncstate.Config[string, *syncedResponse]{
		Name:  syncStateName,
		Key:   func(v *syncedResponse) string { return v.ID },
		Bus:   bus,
		Store: &responseMetadataStoreAdapter{store: store},
	})
	if err := synced.Start(ctx); err != nil {
		lifeCancel()
		return fmt.Errorf("starting response metadata sync: %w", err)
	}

	// Publish the wiring before subscribing: the subscription handler re-enters
	// the store, so everything it reads has to be in place first.
	s.mu.Lock()
	s.replicaID = replicaID
	s.bus = bus
	s.lifeCtx, s.lifeCancel = lifeCtx, lifeCancel
	s.synced = synced
	s.mu.Unlock()

	sub, err := messaging.SubscribeJSON(bus, messaging.SubjectResponseCancelWildcard, s.applyRemoteCancel)
	if err != nil {
		if cerr := s.Close(); cerr != nil {
			xlog.Warn("failed to tear down response metadata sync after subscribe error", "error", cerr)
		}
		return fmt.Errorf("subscribing to response cancel wildcard: %w", err)
	}

	s.mu.Lock()
	s.cancelSub = sub
	s.mu.Unlock()

	s.startMetadataPurge(lifeCtx, store)

	xlog.Info("Open Responses store replicating across replicas", "replica_id", replicaID)
	return nil
}

// DefaultResponseMetadataPurgeInterval is how often a replica sweeps rows whose
// TTL has passed out of the durable metadata table.
//
// Every replica runs the sweep; the DELETE is idempotent and rows are selected
// on the database clock, so two replicas sweeping at once cost one extra
// statement and never disagree about which rows are dead.
const DefaultResponseMetadataPurgeInterval = 5 * time.Minute

// startMetadataPurge launches the sweep goroutine on the store's own lifetime
// context, so Close stops it.
//
// purgeTicks is the seam that makes the sweep testable without a clock: a spec
// installs its own channel and sends one tick, which is deterministic where a
// five-minute ticker and a sleep are not.
func (s *ResponseStore) startMetadataPurge(lifeCtx context.Context, store *distributed.ResponseMetadataStore) {
	s.mu.Lock()
	ticks := s.purgeTicks
	s.mu.Unlock()

	var stop func()
	if ticks == nil {
		t := time.NewTicker(DefaultResponseMetadataPurgeInterval)
		ticks, stop = t.C, t.Stop
	}

	s.purgeWG.Add(1)
	go s.purgeExpiredMetadata(lifeCtx, store, ticks, stop)
}

// purgeExpiredMetadata sweeps expired rows until the store is closed.
//
// A failed sweep is logged and retried on the next tick rather than ending the
// loop: an unreachable database is not evidence that there is nothing to purge,
// and a loop that exited on the first transient error would leave the table
// growing for the rest of the process's life.
func (s *ResponseStore) purgeExpiredMetadata(ctx context.Context, store *distributed.ResponseMetadataStore,
	ticks <-chan time.Time, stop func()) {
	defer s.purgeWG.Done()
	if stop != nil {
		defer stop()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			n, err := store.PurgeExpired(ctx)
			if err != nil {
				xlog.Warn("failed to purge expired Open Responses metadata", "error", err)
				continue
			}
			if n > 0 {
				xlog.Debug("Purged expired Open Responses metadata", "rows", n)
			}
		}
	}
}

// Close tears down the distributed wiring. It is idempotent so a test (or a
// double shutdown) can call it more than once, and is a no-op for a standalone
// store.
func (s *ResponseStore) Close() error {
	s.mu.Lock()
	sub := s.cancelSub
	s.cancelSub = nil
	synced := s.synced
	s.synced = nil
	cancel := s.lifeCancel
	s.lifeCancel = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	// Wait for the purge sweep before returning: a Close that raced its own
	// goroutine would let a sweep issue a statement against a database the
	// caller has already torn down, which a spec sees as a flake and a
	// deployment sees as an error line during shutdown.
	s.purgeWG.Wait()
	if sub != nil {
		if err := sub.Unsubscribe(); err != nil {
			return err
		}
	}
	if synced != nil {
		return synced.Close()
	}
	return nil
}

// syncMap returns the replicated metadata map, or nil in standalone mode.
func (s *ResponseStore) syncMap() *syncstate.SyncedMap[string, *syncedResponse] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.synced
}

// distributed returns the replication handles as a consistent snapshot. Every
// path that broadcasts reads them through here so a concurrent Close cannot be
// observed half-applied. A nil map means standalone mode.
func (s *ResponseStore) distributed() (*syncstate.SyncedMap[string, *syncedResponse], context.Context, messaging.Broadcaster, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ctx := s.lifeCtx
	if ctx == nil {
		ctx = context.Background()
	}
	return s.synced, ctx, s.bus, s.replicaID
}

// replicaIdentity returns this process's replica ID (empty in standalone mode).
func (s *ResponseStore) replicaIdentity() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.replicaID
}

// mirror publishes the current metadata of a locally-owned response to peers.
//
// It must be called with s.mu released: the broadcast is delivered synchronously
// by the in-memory bus used in tests, and a subscriber callback re-enters the
// store to look up the response.
func (s *ResponseStore) mirror(responseID string, stored *StoredResponse) {
	m, ctx, _, replicaID := s.distributed()
	if m == nil {
		return
	}

	stored.mu.RLock()
	v := &syncedResponse{
		ID:            responseID,
		OwnerReplica:  replicaID,
		Owner:         stored.Owner,
		Request:       stored.Request,
		Response:      stored.Response,
		StoredAt:      stored.StoredAt,
		ExpiresAt:     stored.ExpiresAt,
		StreamEnabled: stored.StreamEnabled,
		IsBackground:  stored.IsBackground,
	}
	stored.mu.RUnlock()

	if err := m.Set(ctx, v); err != nil {
		xlog.Warn("failed to replicate Open Responses metadata", "response_id", responseID, "error", err)
	}
}

// unmirror removes a response from the replicated metadata.
func (s *ResponseStore) unmirror(responseID string) {
	m, ctx, _, _ := s.distributed()
	if m == nil {
		return
	}
	if err := m.Delete(ctx, responseID); err != nil {
		xlog.Warn("failed to remove replicated Open Responses metadata", "response_id", responseID, "error", err)
	}
}

// remoteGet resolves a response from the replicated metadata and materialises a
// read-only StoredResponse view of it.
//
// The view is rebuilt on every call rather than inserted into s.responses: it
// carries no CancelFunc and no resume buffer, so caching it locally would make
// the store unable to tell "mine" from "a peer's" and would silently break the
// stream and cancel paths. Remote is the flag every process-local accessor keys
// off.
func (s *ResponseStore) remoteGet(responseID string) (*StoredResponse, bool) {
	m := s.syncMap()
	if m == nil {
		return nil, false
	}
	v, ok := m.Get(responseID)
	if !ok || v == nil || v.Response == nil {
		return nil, false
	}
	if v.ExpiresAt != nil && time.Now().After(*v.ExpiresAt) {
		return nil, false
	}

	// Rebuild the item index on this side so GetItem/FindItem behave the same
	// as they do on the owner; the index is derived data and is not shipped.
	items := make(map[string]*schema.ORItemField)
	for i := range v.Response.Output {
		item := &v.Response.Output[i]
		if item.ID != "" {
			items[item.ID] = item
		}
	}

	return &StoredResponse{
		Request:        v.Request,
		Response:       v.Response,
		Items:          items,
		StoredAt:       v.StoredAt,
		ExpiresAt:      v.ExpiresAt,
		Owner:          v.Owner,
		StreamEnabled:  v.StreamEnabled,
		IsBackground:   v.IsBackground,
		Remote:         true,
		OwnerReplica:   v.OwnerReplica,
		droppedThrough: -1,
	}, true
}

// delegateCancel handles a cancel for a response this replica does not own.
//
// The CancelFunc lives in the owner's process, so the request is broadcast and
// the owner applies it locally (applyRemoteCancel). The broadcast is
// fire-and-forget: a request/reply would block this HTTP handler for a timeout
// whenever the owner has crashed or been scaled down, which is exactly the case
// where the caller most needs a prompt answer.
//
// The replicated status is moved to cancelled here rather than waiting for the
// owner to confirm. If the owner is alive it will cancel and converge on the
// same value; if it is gone its generation died with its process, so cancelled
// is the truthful terminal state either way.
func (s *ResponseStore) delegateCancel(v *syncedResponse) (*schema.ORResponseResource, error) {
	if v.Response == nil {
		return nil, fmt.Errorf("response not found: %s", v.ID)
	}

	status := v.Response.Status
	if status == schema.ORStatusCompleted || status == schema.ORStatusFailed ||
		status == schema.ORStatusIncomplete || status == schema.ORStatusCancelled {
		xlog.Debug("Peer-owned response already in terminal state", "response_id", v.ID, "status", status)
		return v.Response, nil
	}

	m, ctx, bus, replicaID := s.distributed()
	if bus != nil {
		if err := bus.Publish(messaging.SubjectResponseCancel(v.ID),
			responseCancelEvent{ResponseID: v.ID, Origin: replicaID}); err != nil {
			xlog.Warn("failed to broadcast Open Responses cancel", "response_id", v.ID, "error", err)
		}
	}

	// Copy before mutating: the value in the SyncedMap is shared with any peer
	// delta already applied, and the response resource is handed to the client.
	updated := *v
	response := *v.Response
	response.Status = schema.ORStatusCancelled
	now := time.Now().Unix()
	response.CompletedAt = &now
	updated.Response = &response

	if m != nil {
		if err := m.Set(ctx, &updated); err != nil {
			xlog.Warn("failed to replicate cancelled Open Responses status", "response_id", v.ID, "error", err)
		}
	}

	xlog.Debug("Delegated Open Responses cancel to owning replica", "response_id", v.ID, "owner_replica", v.OwnerReplica)
	return &response, nil
}

// applyRemoteCancel runs a peer's delegated cancel against local state.
//
// Only the replica that actually holds the response in s.responses has anything
// to do; every other subscriber drops the event. It deliberately does not
// re-broadcast and does not re-write the replicated metadata - the delegating
// replica already published the cancelled status - which is the same echo-loop
// guard syncstate applies on its own apply path.
func (s *ResponseStore) applyRemoteCancel(evt responseCancelEvent) {
	if evt.ResponseID == "" || evt.Origin == s.replicaIdentity() {
		return
	}

	s.mu.RLock()
	stored, exists := s.responses[evt.ResponseID]
	s.mu.RUnlock()
	if !exists {
		return
	}

	if _, err := s.cancelLocal(evt.ResponseID, stored); err != nil {
		xlog.Warn("failed to apply delegated Open Responses cancel", "response_id", evt.ResponseID, "error", err)
		return
	}
	xlog.Debug("Applied delegated Open Responses cancel", "response_id", evt.ResponseID, "origin", evt.Origin)
}
