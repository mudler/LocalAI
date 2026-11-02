// SPDX-License-Identifier: MIT
package backend

import (
	"context"
	"github.com/google/uuid"
	"github.com/mudler/localrecall/rag"
)

// MemoryWrite freezes exact content and application metadata. PayloadHash is
// required and must be computed with MemoryPayloadHash, not JSON encoding.
type MemoryWrite = rag.MemoryWrite

// MemoryReceipt preserves the richer dependency protocol: State distinguishes
// absent/preparing/saved/deleted and PayloadHash/ChunkIDs permit reconciliation.
type MemoryReceipt = rag.MemoryReceipt
type MemoryFence = rag.MemoryFence
type MemoryCapabilities = rag.MemoryCapabilities

const (
	MemoryAbsent    = rag.MemoryAbsent
	MemoryPreparing = rag.MemoryPreparing
	MemorySaved     = rag.MemorySaved
	MemoryDeleted   = rag.MemoryDeleted
)

var (
	ErrMemoryUnsupported = rag.ErrMemoryUnsupported
	ErrMemoryConflict    = rag.ErrMemoryConflict
	ErrMemoryNotFound    = rag.ErrMemoryNotFound
	ErrMemoryStaleFence  = rag.ErrMemoryStaleFence
	ErrMemoryCorrupt     = rag.ErrMemoryCorrupt
)

// MemoryPayloadHash delegates the versioned canonical encoding to LocalRecall.
func MemoryPayloadHash(content string, metadata map[string]string) (string, error) {
	return rag.MemoryPayloadHash(content, metadata)
}

// MemoryBackend is optional and deliberately separate from legacy Upload.
// Collection names must already be principal-scoped by the caller. No method
// creates a collection or falls back to UUID uploads. Distributed callers must
// require DistributedFencing and use the explicit reservation/fenced write pair.
// A backend fence does not atomically claim a chat queue item: the future durable
// queue adapter must coordinate takeover and persist fences before effects.
type MemoryBackend interface {
	MemoryCapabilities(context.Context, string) (MemoryCapabilities, error)
	WriteMemory(context.Context, string, MemoryWrite) (MemoryReceipt, error)
	BeginMemoryAttempt(context.Context, string, string, string) (MemoryFence, error)
	WriteMemoryFenced(context.Context, string, MemoryFence, MemoryWrite) (MemoryReceipt, error)
	ReconcileMemory(context.Context, string, string) (MemoryReceipt, error)
	DeleteMemoryEntry(context.Context, string, string) (MemoryReceipt, error)
}

var _ MemoryBackend = (*backendInProcess)(nil)

// The local lock protects the collection/model lifecycle, not distributed
// idempotency. LocalRecall's journal ownership or PostgreSQL row locks do that.
func (b *backendInProcess) memoryKB(ctx context.Context, collection string) (*rag.PersistentKB, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	switch b.cfg.VectorEngine {
	case "chromem", "postgres":
	default:
		return nil, nil, ErrMemoryUnsupported
	}
	settings, err := b.settings(collection)
	if err != nil {
		return nil, nil, err
	}
	unlock := b.lockCollection(collection)
	kb, err := b.writable(collection, settings, false)
	if err != nil {
		unlock()
		return nil, nil, err
	}
	return kb, unlock, nil
}
func (b *backendInProcess) MemoryCapabilities(ctx context.Context, collection string) (MemoryCapabilities, error) {
	switch b.cfg.VectorEngine {
	case "chromem", "postgres":
	default:
		return MemoryCapabilities{Reason: "engine has no accepted durable memory protocol"}, ctx.Err()
	}
	kb, unlock, err := b.memoryKB(ctx, collection)
	if err != nil {
		return MemoryCapabilities{}, err
	}
	defer unlock()
	return kb.MemoryCapabilities(ctx)
}

// WriteMemory is a convenience for synchronous, non-queue callers. Each call
// reserves a fresh backend generation. Queue workers must use WriteMemoryFenced
// with their persisted reservation, never this convenience on a stale claim.
func (b *backendInProcess) WriteMemory(ctx context.Context, collection string, req MemoryWrite) (MemoryReceipt, error) {
	kb, unlock, err := b.memoryKB(ctx, collection)
	if err != nil {
		return MemoryReceipt{}, err
	}
	defer unlock()
	if err = rag.ValidateMemoryWrite(req); err != nil {
		return MemoryReceipt{}, err
	}
	fence, err := kb.BeginMemoryAttempt(ctx, req.Key, uuid.NewString())
	if err != nil {
		return MemoryReceipt{}, err
	}
	return kb.WriteMemory(ctx, fence, req)
}
func (b *backendInProcess) BeginMemoryAttempt(ctx context.Context, collection, key, owner string) (MemoryFence, error) {
	kb, unlock, err := b.memoryKB(ctx, collection)
	if err != nil {
		return MemoryFence{}, err
	}
	defer unlock()
	return kb.BeginMemoryAttempt(ctx, key, owner)
}
func (b *backendInProcess) WriteMemoryFenced(ctx context.Context, collection string, fence MemoryFence, req MemoryWrite) (MemoryReceipt, error) {
	kb, unlock, err := b.memoryKB(ctx, collection)
	if err != nil {
		return MemoryReceipt{}, err
	}
	defer unlock()
	return kb.WriteMemory(ctx, fence, req)
}
func (b *backendInProcess) ReconcileMemory(ctx context.Context, collection, key string) (MemoryReceipt, error) {
	kb, unlock, err := b.memoryKB(ctx, collection)
	if err != nil {
		return MemoryReceipt{}, err
	}
	defer unlock()
	return kb.ReconcileMemory(ctx, key)
}
func (b *backendInProcess) DeleteMemoryEntry(ctx context.Context, collection, entry string) (MemoryReceipt, error) {
	kb, unlock, err := b.memoryKB(ctx, collection)
	if err != nil {
		return MemoryReceipt{}, err
	}
	defer unlock()
	return kb.DeleteMemoryEntry(ctx, entry)
}
