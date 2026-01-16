package openresponses

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/xlog"
)

// ResponseStore provides thread-safe storage for Open Responses API responses
type ResponseStore struct {
	mu            sync.RWMutex
	responses     map[string]*StoredResponse
	ttl           time.Duration // Time-to-live for stored responses (0 = no expiration)
	cleanupCtx    context.Context
	cleanupCancel context.CancelFunc
}

// StoredResponse contains a complete response with its input request and output items
type StoredResponse struct {
	Request   *schema.OpenResponsesRequest
	Response  *schema.ORResponseResource
	Items     map[string]*schema.ORItemField // item_id -> item mapping for quick lookup
	StoredAt  time.Time
	ExpiresAt *time.Time // nil if no expiration
}

var (
	globalStore *ResponseStore
	storeOnce   sync.Once
)

// GetGlobalStore returns the singleton response store instance
func GetGlobalStore() *ResponseStore {
	storeOnce.Do(func() {
		globalStore = NewResponseStore(0) // Default: no TTL, will be updated from appConfig
	})
	return globalStore
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
		responses: make(map[string]*StoredResponse),
		ttl:       ttl,
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
	defer s.mu.Unlock()

	// Build item index for quick lookup
	items := make(map[string]*schema.ORItemField)
	for i := range response.Output {
		item := &response.Output[i]
		if item.ID != "" {
			items[item.ID] = item
		}
	}

	stored := &StoredResponse{
		Request:   request,
		Response:  response,
		Items:     items,
		StoredAt:  time.Now(),
		ExpiresAt: nil,
	}

	// Set expiration if TTL is configured
	if s.ttl > 0 {
		expiresAt := time.Now().Add(s.ttl)
		stored.ExpiresAt = &expiresAt
	}

	s.responses[responseID] = stored
	xlog.Debug("Stored Open Responses response", "response_id", responseID, "items_count", len(items))
}

// Get retrieves a stored response by ID
func (s *ResponseStore) Get(responseID string) (*StoredResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stored, exists := s.responses[responseID]
	if !exists {
		return nil, fmt.Errorf("response not found: %s", responseID)
	}

	// Check expiration
	if stored.ExpiresAt != nil && time.Now().After(*stored.ExpiresAt) {
		// Expired, but we'll return it anyway and let caller handle cleanup
		return nil, fmt.Errorf("response expired: %s", responseID)
	}

	return stored, nil
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	for responseID, stored := range s.responses {
		// Skip expired responses
		if stored.ExpiresAt != nil && now.After(*stored.ExpiresAt) {
			continue
		}

		if item, exists := stored.Items[itemID]; exists {
			return item, responseID, nil
		}
	}

	return nil, "", fmt.Errorf("item not found in any stored response: %s", itemID)
}

// Delete removes a response from storage
func (s *ResponseStore) Delete(responseID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.responses, responseID)
	xlog.Debug("Deleted Open Responses response", "response_id", responseID)
}

// Cleanup removes expired responses
func (s *ResponseStore) Cleanup() int {
	if s.ttl == 0 {
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	count := 0
	for id, stored := range s.responses {
		if stored.ExpiresAt != nil && now.After(*stored.ExpiresAt) {
			delete(s.responses, id)
			count++
		}
	}

	if count > 0 {
		xlog.Debug("Cleaned up expired Open Responses", "count", count)
	}

	return count
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
