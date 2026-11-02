package collections

import (
	"context"
	"errors"
	"strings"

	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
)

// memoryBackend never creates a destination and does not use legacy upload/reset.
func (s *Service) memoryBackend(user, collection string) (lagi.MemoryBackend, error) {
	b, err := s.Backend(user)
	if err != nil {
		return nil, err
	}
	if shared, ok := b.(*sharedCollections); ok {
		if err = shared.ensure(collection); err != nil {
			return nil, err
		}
		b = shared.inner
	}
	mb, ok := b.(lagi.MemoryBackend)
	if !ok {
		return nil, lagi.ErrMemoryUnsupported
	}
	return mb, nil
}
func (s *Service) WriteMemory(ctx context.Context, user, collection string, req lagi.MemoryWrite, authorize func(context.Context) error) (lagi.MemoryReceipt, error) {
	mb, err := s.memoryBackend(user, collection)
	if err != nil {
		return lagi.MemoryReceipt{}, err
	}
	if authorize == nil {
		return lagi.MemoryReceipt{}, errors.New("memory authorization required")
	}
	if err = authorize(ctx); err != nil {
		return lagi.MemoryReceipt{}, err
	}
	return mb.WriteMemory(ctx, collection, req)
}
func (s *Service) DeleteMemory(ctx context.Context, user, collection, entry string, authorize func(context.Context) error) (lagi.MemoryReceipt, error) {
	if entry == "" || len(entry) > 512 || strings.ContainsAny(entry, "*?\\\x00\r\n") || strings.Contains(entry, "..") || strings.TrimSpace(entry) != entry {
		return lagi.MemoryReceipt{}, errors.New("exact entry ID required")
	}
	mb, err := s.memoryBackend(user, collection)
	if err != nil {
		return lagi.MemoryReceipt{}, err
	}
	if authorize == nil {
		return lagi.MemoryReceipt{}, errors.New("memory authorization required")
	}
	if err = authorize(ctx); err != nil {
		return lagi.MemoryReceipt{}, err
	}
	return mb.DeleteMemoryEntry(ctx, collection, entry)
}

// BeginMemoryAttempt is only a backend reservation. Queue workers must invoke it
// inside CaptureMemoryStore.ReserveCaptureMemory, never before checking a claim.
func (s *Service) BeginMemoryAttempt(ctx context.Context, user, collection, key, owner string) (lagi.MemoryFence, error) {
	mb, err := s.memoryBackend(user, collection)
	if err != nil {
		return lagi.MemoryFence{}, err
	}
	return mb.BeginMemoryAttempt(ctx, collection, key, owner)
}

// WriteMemoryFenced must be called inside CaptureMemoryStore.WithCaptureMemory.
// Authorization is rechecked at the backend boundary, not reused from reservation.
func (s *Service) WriteMemoryFenced(ctx context.Context, user, collection string, fence lagi.MemoryFence, req lagi.MemoryWrite, authorize func(context.Context) error) (lagi.MemoryReceipt, error) {
	mb, err := s.memoryBackend(user, collection)
	if err != nil {
		return lagi.MemoryReceipt{}, err
	}
	if authorize == nil {
		return lagi.MemoryReceipt{}, errors.New("memory authorization required")
	}
	if err = authorize(ctx); err != nil {
		return lagi.MemoryReceipt{}, err
	}
	return mb.WriteMemoryFenced(ctx, collection, fence, req)
}

func (s *Service) ReconcileMemory(ctx context.Context, user, collection, key string) (lagi.MemoryReceipt, error) {
	mb, err := s.memoryBackend(user, collection)
	if err != nil {
		return lagi.MemoryReceipt{}, err
	}
	return mb.ReconcileMemory(ctx, collection, key)
}
