package openresponses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/syncstate"
	"github.com/mudler/xlog"
)

const (
	// defaultMaxStreamEvents bounds how many resume-buffer events a single
	// background response retains. Without a cap, a long-running or abandoned
	// background generation grows StreamEvents without limit and can exhaust
	// process memory. When the cap is exceeded the oldest events are evicted
	// from the front (see AppendEvent). Mirrors llama.cpp's byte-capped slot
	// ring used for resumable /slots state.
	defaultMaxStreamEvents = 8192

	// defaultMaxStreamBytes caps the total serialized size of retained
	// resume-buffer events, evicting oldest-first when exceeded. This guards
	// against a handful of very large events defeating the count cap. 0
	// disables the byte cap (count cap still applies).
	defaultMaxStreamBytes = 64 << 20 // 64 MiB
)

// ErrOffsetLost is returned by GetEventsAfter when the requested
// starting_after sequence number is older than the oldest event still
// retained in the resume buffer (i.e. the events between the requested
// offset and the current watermark were evicted by the cap). Callers should
// surface this to clients as a distinct error instead of silently returning
// a truncated stream that omits the dropped events.
var ErrOffsetLost = errors.New("resume offset lost: requested events were evicted from the buffer")

// ResponseStore provides thread-safe storage for Open Responses API responses
type ResponseStore struct {
	mu            sync.RWMutex
	responses     map[string]*StoredResponse
	ttl           time.Duration // Time-to-live for stored responses (0 = no expiration)
	cleanupCtx    context.Context
	cleanupCancel context.CancelFunc

	// maxStreamEvents / maxStreamBytes bound the per-response resume buffer.
	// Set once at construction from the default constants; tests may lower
	// them. A value <= 0 disables that particular cap.
	maxStreamEvents int
	maxStreamBytes  int

	// Cross-replica replication. All zero unless EnableDistributed was called
	// (see sync.go), which is how a standalone deployment keeps exactly the
	// previous process-local behaviour. Guarded by mu.
	synced     *syncstate.SyncedMap[string, *syncedResponse]
	nats       messaging.MessagingClient
	cancelSub  messaging.Subscription
	replicaID  string
	lifeCtx    context.Context
	lifeCancel context.CancelFunc
}

// StreamedEvent represents a buffered SSE event for streaming resume
type StreamedEvent struct {
	SequenceNumber int    `json:"sequence_number"`
	EventType      string `json:"event_type"`
	Data           []byte `json:"data"` // JSON-serialized event
}

// StoredResponse contains a complete response with its input request and output items
type StoredResponse struct {
	Request   *schema.OpenResponsesRequest
	Response  *schema.ORResponseResource
	Items     map[string]*schema.ORItemField // item_id -> item mapping for quick lookup
	StoredAt  time.Time
	ExpiresAt *time.Time // nil if no expiration

	// Owner is the identity (user ID) that created this response. It is set
	// once at creation and never mutated, so it can be read without holding
	// mu. Empty means "no owner" (single-key / no-auth deployments), in which
	// case ownership checks are skipped for backward compatibility.
	Owner string

	// Background execution support
	CancelFunc    context.CancelFunc // For cancellation of background tasks
	StreamEvents  []StreamedEvent    // Buffered events for streaming resume
	StreamEnabled bool               // Was created with stream=true
	IsBackground  bool               // Was created with background=true
	EventsChan    chan struct{}      // Signals new events for live subscribers
	mu            sync.RWMutex       // Protect concurrent access to this response

	// Remote marks a read-only view materialised from another replica's
	// replicated metadata rather than from this process's own map. Such a view
	// has no CancelFunc, no EventsChan and no resume buffer, so every path that
	// needs one of those must check it and either delegate over the bus
	// (cancel) or refuse with ErrResponseNotLocal (stream resume).
	// OwnerReplica names the replica that does hold them.
	Remote       bool
	OwnerReplica string

	// streamBytes tracks the total serialized size of the events currently
	// retained in StreamEvents, used to enforce the byte cap. droppedThrough
	// is the highest sequence number evicted from the front of the buffer
	// (-1 = nothing evicted); it is the watermark GetEventsAfter compares
	// against to detect a lost resume offset. Both are guarded by mu.
	streamBytes    int
	droppedThrough int
}

var getGlobalStore = sync.OnceValue(func() *ResponseStore {
	return NewResponseStore(0) // Default: no TTL, will be updated from appConfig
})

// GetGlobalStore returns the singleton response store instance
func GetGlobalStore() *ResponseStore {
	return getGlobalStore()
}

// SetTTL updates the TTL for the store
// This will affect all new responses stored after this call
func (s *ResponseStore) SetTTL(ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop existing cleanup loop if running
	if s.cleanupCancel != nil {
		s.cleanupCancel()
		s.cleanupCancel = nil
		s.cleanupCtx = nil
	}

	s.ttl = ttl

	// If TTL > 0, start cleanup loop
	if ttl > 0 {
		s.cleanupCtx, s.cleanupCancel = context.WithCancel(context.Background())
		go s.cleanupLoop(s.cleanupCtx)
	}

	xlog.Debug("Updated Open Responses store TTL", "ttl", ttl, "cleanup_running", ttl > 0)
}

// NewResponseStore creates a new response store with optional TTL
// If ttl is 0, responses are stored indefinitely
func NewResponseStore(ttl time.Duration) *ResponseStore {
	store := &ResponseStore{
		responses:       make(map[string]*StoredResponse),
		ttl:             ttl,
		maxStreamEvents: defaultMaxStreamEvents,
		maxStreamBytes:  defaultMaxStreamBytes,
	}

	// Start cleanup goroutine if TTL is set
	if ttl > 0 {
		store.cleanupCtx, store.cleanupCancel = context.WithCancel(context.Background())
		go store.cleanupLoop(store.cleanupCtx)
	}

	return store
}

// Store stores a response with its request and items
func (s *ResponseStore) Store(responseID string, request *schema.OpenResponsesRequest, response *schema.ORResponseResource) {
	s.mu.Lock()

	// Build item index for quick lookup
	items := make(map[string]*schema.ORItemField)
	for i := range response.Output {
		item := &response.Output[i]
		if item.ID != "" {
			items[item.ID] = item
		}
	}

	stored := &StoredResponse{
		Request:        request,
		Response:       response,
		Items:          items,
		StoredAt:       time.Now(),
		ExpiresAt:      nil,
		droppedThrough: -1,
	}

	// Set expiration if TTL is configured
	if s.ttl > 0 {
		expiresAt := time.Now().Add(s.ttl)
		stored.ExpiresAt = &expiresAt
	}

	s.responses[responseID] = stored
	s.mu.Unlock()

	// Replicate outside the lock: the broadcast can be delivered synchronously
	// and a subscriber re-enters the store.
	s.mirror(responseID, stored)
	xlog.Debug("Stored Open Responses response", "response_id", responseID, "items_count", len(items))
}

// Get retrieves a stored response by ID.
//
// In distributed mode a miss in this process's map is not proof the response
// does not exist: a round-robin load balancer routes polls and
// previous_response_id lookups to any replica, not the one that created it. So
// a local miss falls back to the replicated metadata and returns a read-only
// remote view (issue #10993). Only a miss in both is a real "not found".
func (s *ResponseStore) Get(responseID string) (*StoredResponse, error) {
	s.mu.RLock()
	stored, exists := s.responses[responseID]
	s.mu.RUnlock()

	if exists {
		// Check expiration
		if stored.ExpiresAt != nil && time.Now().After(*stored.ExpiresAt) {
			// Expired, but we'll return it anyway and let caller handle cleanup
			return nil, fmt.Errorf("response expired: %s", responseID)
		}
		return stored, nil
	}

	if remote, ok := s.remoteGet(responseID); ok {
		return remote, nil
	}

	return nil, fmt.Errorf("response not found: %s", responseID)
}

// GetItem retrieves a specific item from a stored response
func (s *ResponseStore) GetItem(responseID, itemID string) (*schema.ORItemField, error) {
	stored, err := s.Get(responseID)
	if err != nil {
		return nil, err
	}

	item, exists := stored.Items[itemID]
	if !exists {
		return nil, fmt.Errorf("item not found: %s in response %s", itemID, responseID)
	}

	return item, nil
}

// FindItem searches for an item across all stored responses
// Returns the item and the response ID it was found in
func (s *ResponseStore) FindItem(itemID string) (*schema.ORItemField, string, error) {
	now := time.Now()

	if item, responseID, found := s.findItemLocal(itemID, now); found {
		return item, responseID, nil
	}

	// An item referenced by ID can belong to a response created on any replica,
	// so the local sweep alone reproduces the same coin-flip lookup #10993
	// describes for GET /v1/responses/{id}.
	if m := s.syncMap(); m != nil {
		for responseID, v := range m.Snapshot() {
			if v == nil || v.Response == nil {
				continue
			}
			if v.ExpiresAt != nil && now.After(*v.ExpiresAt) {
				continue
			}
			for i := range v.Response.Output {
				if v.Response.Output[i].ID == itemID {
					return &v.Response.Output[i], responseID, nil
				}
			}
		}
	}

	return nil, "", fmt.Errorf("item not found in any stored response: %s", itemID)
}

// findItemLocal sweeps only this process's own responses. Split out so the
// replicated sweep in FindItem runs with s.mu released - the SyncedMap is a
// separate component with its own lock and must not be entered under ours.
func (s *ResponseStore) findItemLocal(itemID string, now time.Time) (*schema.ORItemField, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for responseID, stored := range s.responses {
		// Skip expired responses
		if stored.ExpiresAt != nil && now.After(*stored.ExpiresAt) {
			continue
		}

		if item, exists := stored.Items[itemID]; exists {
			return item, responseID, true
		}
	}
	return nil, "", false
}

// Delete removes a response from storage
func (s *ResponseStore) Delete(responseID string) {
	s.mu.Lock()
	delete(s.responses, responseID)
	s.mu.Unlock()

	s.unmirror(responseID)
	xlog.Debug("Deleted Open Responses response", "response_id", responseID)
}

// Cleanup removes expired responses
func (s *ResponseStore) Cleanup() int {
	if s.ttl == 0 {
		return 0
	}

	s.mu.Lock()
	now := time.Now()
	expired := []string{}
	for id, stored := range s.responses {
		if stored.ExpiresAt != nil && now.After(*stored.ExpiresAt) {
			delete(s.responses, id)
			expired = append(expired, id)
		}
	}
	s.mu.Unlock()

	// Reap replicated entries too, including those whose owner never got to
	// expire them because it was scaled down mid-flight. This covers the
	// locally-expired IDs as well, since they were mirrored with the same
	// ExpiresAt. Any replica may do this; the delete broadcast is idempotent.
	if m := s.syncMap(); m != nil {
		for id, v := range m.Snapshot() {
			if v != nil && v.ExpiresAt != nil && now.After(*v.ExpiresAt) {
				s.unmirror(id)
			}
		}
	}

	if len(expired) > 0 {
		xlog.Debug("Cleaned up expired Open Responses", "count", len(expired))
	}

	return len(expired)
}

// cleanupLoop runs periodic cleanup of expired responses
func (s *ResponseStore) cleanupLoop(ctx context.Context) {
	if s.ttl == 0 {
		return
	}

	ticker := time.NewTicker(s.ttl / 2) // Cleanup at half TTL interval
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			xlog.Debug("Stopped Open Responses store cleanup loop")
			return
		case <-ticker.C:
			s.Cleanup()
		}
	}
}

// Count returns the number of stored responses
func (s *ResponseStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.responses)
}

// StoreBackground stores a background response with cancel function and optional streaming support
func (s *ResponseStore) StoreBackground(responseID string, request *schema.OpenResponsesRequest, response *schema.ORResponseResource, cancelFunc context.CancelFunc, streamEnabled bool) {
	s.mu.Lock()

	// Build item index for quick lookup
	items := make(map[string]*schema.ORItemField)
	for i := range response.Output {
		item := &response.Output[i]
		if item.ID != "" {
			items[item.ID] = item
		}
	}

	stored := &StoredResponse{
		Request:        request,
		Response:       response,
		Items:          items,
		StoredAt:       time.Now(),
		ExpiresAt:      nil,
		CancelFunc:     cancelFunc,
		StreamEvents:   []StreamedEvent{},
		StreamEnabled:  streamEnabled,
		IsBackground:   true,
		EventsChan:     make(chan struct{}, 100), // Buffered channel for event notifications
		droppedThrough: -1,
	}

	// Set expiration if TTL is configured
	if s.ttl > 0 {
		expiresAt := time.Now().Add(s.ttl)
		stored.ExpiresAt = &expiresAt
	}

	s.responses[responseID] = stored
	s.mu.Unlock()

	// Only the metadata crosses the bus. CancelFunc and the resume buffer stay
	// here, which is what makes this replica the owner for cancel and resume.
	s.mirror(responseID, stored)
	xlog.Debug("Stored background Open Responses response", "response_id", responseID, "stream_enabled", streamEnabled)
}

// UpdateStatus updates the status of a stored response
func (s *ResponseStore) UpdateStatus(responseID string, status string, completedAt *int64) error {
	s.mu.RLock()
	stored, exists := s.responses[responseID]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("response not found: %s", responseID)
	}

	stored.mu.Lock()
	stored.Response.Status = status
	stored.Response.CompletedAt = completedAt
	stored.mu.Unlock()

	// Peers poll this response too, so every status transition has to be
	// republished or their view stays stuck at "queued" forever.
	s.mirror(responseID, stored)

	xlog.Debug("Updated response status", "response_id", responseID, "status", status)
	return nil
}

// UpdateResponse updates the entire response object for a stored response
func (s *ResponseStore) UpdateResponse(responseID string, response *schema.ORResponseResource) error {
	s.mu.RLock()
	stored, exists := s.responses[responseID]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("response not found: %s", responseID)
	}

	stored.mu.Lock()

	// Rebuild item index
	items := make(map[string]*schema.ORItemField)
	for i := range response.Output {
		item := &response.Output[i]
		if item.ID != "" {
			items[item.ID] = item
		}
	}

	stored.Response = response
	stored.Items = items
	stored.mu.Unlock()

	// The final output is what a peer's poll must return, so replicate it.
	s.mirror(responseID, stored)

	xlog.Debug("Updated response", "response_id", responseID, "status", response.Status, "items_count", len(items))
	return nil
}

// AppendEvent appends a streaming event to the buffer for resume support
func (s *ResponseStore) AppendEvent(responseID string, event *schema.ORStreamEvent) error {
	s.mu.RLock()
	stored, exists := s.responses[responseID]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("response not found: %s", responseID)
	}

	// Serialize the event
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	stored.mu.Lock()
	stored.StreamEvents = append(stored.StreamEvents, StreamedEvent{
		SequenceNumber: event.SequenceNumber,
		EventType:      event.Type,
		Data:           data,
	})
	stored.streamBytes += len(data)

	// Evict oldest events from the front once either cap is exceeded. The
	// byte cap never evicts the only remaining event (a single oversized
	// event is still served once). Each eviction advances droppedThrough so
	// a later resume below the watermark is reported as ErrOffsetLost rather
	// than silently skipping the dropped events.
	for (s.maxStreamEvents > 0 && len(stored.StreamEvents) > s.maxStreamEvents) ||
		(s.maxStreamBytes > 0 && stored.streamBytes > s.maxStreamBytes && len(stored.StreamEvents) > 1) {
		evicted := stored.StreamEvents[0]
		stored.streamBytes -= len(evicted.Data)
		if evicted.SequenceNumber > stored.droppedThrough {
			stored.droppedThrough = evicted.SequenceNumber
		}
		// Release the evicted payload so it can be GC'd even though the
		// backing array element is still owned by the slice until reuse.
		stored.StreamEvents[0].Data = nil
		stored.StreamEvents = stored.StreamEvents[1:]
	}
	stored.mu.Unlock()

	// Notify any subscribers of new event
	select {
	case stored.EventsChan <- struct{}{}:
	default:
		// Channel full, subscribers will catch up
	}

	return nil
}

// GetEventsAfter returns all events with sequence number greater than startingAfter
func (s *ResponseStore) GetEventsAfter(responseID string, startingAfter int) ([]StreamedEvent, error) {
	s.mu.RLock()
	stored, exists := s.responses[responseID]
	s.mu.RUnlock()

	if !exists {
		// The response may be alive on a peer, whose resume buffer is not
		// replicated. Say so explicitly instead of reporting "not found" (which
		// invites the client to give up on a live stream) or an empty slice
		// (which looks like a finished one).
		if _, remote := s.remoteGet(responseID); remote {
			return nil, ErrResponseNotLocal
		}
		return nil, fmt.Errorf("response not found: %s", responseID)
	}

	stored.mu.RLock()
	defer stored.mu.RUnlock()

	// If the requested offset is older than the watermark, the events the
	// client expects next (those in (startingAfter, droppedThrough]) were
	// evicted by the cap. Signal the gap rather than returning a stream that
	// silently skips them.
	if startingAfter < stored.droppedThrough {
		return nil, ErrOffsetLost
	}

	var result []StreamedEvent
	for _, event := range stored.StreamEvents {
		if event.SequenceNumber > startingAfter {
			result = append(result, event)
		}
	}

	return result, nil
}

// Cancel cancels a background response if it's still in progress.
//
// The context.CancelFunc that actually stops generation is a function pointer
// and only exists in the process that created the response. When the cancel
// lands on any other replica - which a round-robin load balancer makes roughly
// as likely as landing on the right one - it is delegated to the owner over the
// bus instead of being reported as a 404 while generation keeps running
// (issue #10993).
func (s *ResponseStore) Cancel(responseID string) (*schema.ORResponseResource, error) {
	s.mu.RLock()
	stored, exists := s.responses[responseID]
	s.mu.RUnlock()

	if exists {
		response, err := s.cancelLocal(responseID, stored)
		if err != nil {
			return nil, err
		}
		s.mirror(responseID, stored)
		return response, nil
	}

	if m := s.syncMap(); m != nil {
		if v, ok := m.Get(responseID); ok && v != nil {
			return s.delegateCancel(v)
		}
	}

	return nil, fmt.Errorf("response not found: %s", responseID)
}

// cancelLocal cancels a response this process owns. Shared by the direct HTTP
// path and by the delegated path a peer triggers over the bus, so both go
// through exactly the same terminal-state and CancelFunc handling.
func (s *ResponseStore) cancelLocal(responseID string, stored *StoredResponse) (*schema.ORResponseResource, error) {
	stored.mu.Lock()
	defer stored.mu.Unlock()

	// If already in a terminal state, just return the response (idempotent)
	status := stored.Response.Status
	if status == schema.ORStatusCompleted || status == schema.ORStatusFailed ||
		status == schema.ORStatusIncomplete || status == schema.ORStatusCancelled {
		xlog.Debug("Response already in terminal state", "response_id", responseID, "status", status)
		return stored.Response, nil
	}

	// Cancel the context if available
	if stored.CancelFunc != nil {
		stored.CancelFunc()
		xlog.Debug("Cancelled background response", "response_id", responseID)
	}

	// Update status to cancelled
	now := time.Now().Unix()
	stored.Response.Status = schema.ORStatusCancelled
	stored.Response.CompletedAt = &now

	return stored.Response, nil
}

// GetEventsChan returns the events notification channel for a response
func (s *ResponseStore) GetEventsChan(responseID string) (chan struct{}, error) {
	s.mu.RLock()
	stored, exists := s.responses[responseID]
	s.mu.RUnlock()

	if !exists {
		// A live subscriber channel cannot be handed across processes; see
		// ErrResponseNotLocal.
		if _, remote := s.remoteGet(responseID); remote {
			return nil, ErrResponseNotLocal
		}
		return nil, fmt.Errorf("response not found: %s", responseID)
	}

	return stored.EventsChan, nil
}

// IsStreamEnabled checks if a response was created with streaming enabled
func (s *ResponseStore) IsStreamEnabled(responseID string) (bool, error) {
	s.mu.RLock()
	stored, exists := s.responses[responseID]
	s.mu.RUnlock()

	if !exists {
		// Stream-enabled is replicated metadata, so a peer can answer this one
		// even though it cannot serve the buffer itself.
		if remote, ok := s.remoteGet(responseID); ok {
			return remote.StreamEnabled, nil
		}
		return false, fmt.Errorf("response not found: %s", responseID)
	}

	stored.mu.RLock()
	defer stored.mu.RUnlock()

	return stored.StreamEnabled, nil
}

// SetOwner records the identity that owns a stored response. It is called
// once, right after the response is stored and before its ID is handed back
// to any client, so no lock on the stored response is required. A no-op for
// an empty owner or unknown response ID.
func (s *ResponseStore) SetOwner(responseID, owner string) {
	if owner == "" {
		return
	}

	s.mu.RLock()
	stored, exists := s.responses[responseID]
	s.mu.RUnlock()
	if !exists {
		return
	}

	stored.Owner = owner

	// Owner is part of the replicated metadata: without it a peer would treat
	// the response as ownerless and skip the access check in accessAllowed.
	s.mirror(responseID, stored)
}

// accessAllowed reports whether a caller identified by callerID may read or
// mutate the given stored response. An empty owner (single-key / no-auth
// deployments) is accessible by anyone, preserving backward compatibility;
// otherwise the caller identity must match the recorded owner.
func accessAllowed(stored *StoredResponse, callerID string) bool {
	return stored.Owner == "" || stored.Owner == callerID
}
