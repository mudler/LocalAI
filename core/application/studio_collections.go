package application

import (
	"context"
	"errors"
	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
	"github.com/mudler/LocalAI/core/services/studio/memory"
	"github.com/mudler/LocalAI/core/services/studio/memoryaudit"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
)

// studioCollections lets the chats search the collections of the collections service.
// It asks for the pool at each call: the service starts after the chats service.
type studioCollections struct{ a *Application }

// CaptureReady distinguishes enabled collections still opening from a live
// permission denial. The worker polls without claiming durable work in this window.
func (c studioCollections) CaptureReady() bool {
	return c.a.CollectionsService() != nil || c.a.applicationConfig == nil || !c.a.applicationConfig.Collections.Enabled
}

// owner maps the chat's user to the user the collections are kept under. The
// chats name the unauthenticated user "anonymous"; the collections name it "".
func (studioCollections) owner(user string) string {
	if user == studio.AnonymousPrincipal {
		return ""
	}
	return user
}

// Allowed holds when the collections service runs and the user has the collections feature,
// the rule of /api/collections.
func (c studioCollections) Allowed(ctx context.Context, user string) bool {
	if c.a.CollectionsService() == nil {
		return false
	}
	if c.a.authDB == nil {
		return true
	}
	u, err := c.a.loadStudioUser(ctx, user)
	return err == nil && auth.HasFeatureAccess(c.a.authDB, u, auth.FeatureCollections)
}

func (c studioCollections) List(ctx context.Context, user string) ([]string, error) {
	svc := c.a.CollectionsService()
	if svc == nil || !c.Allowed(ctx, user) {
		return nil, studio.ErrNoCollection
	}
	return svc.ListForUser(c.owner(user))
}

func (c studioCollections) Search(ctx context.Context, user, collection, query string, limit int) ([]studio.CollectionHit, error) {
	svc := c.a.CollectionsService()
	if svc == nil || !c.Allowed(ctx, user) {
		return nil, studio.ErrNoCollection
	}
	res, err := svc.SearchForUser(ctx, c.owner(user), collection, query, limit)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, studio.ErrNoCollection
		}
		return nil, err
	}
	out := make([]studio.CollectionHit, 0, len(res))
	for _, r := range res {
		out = append(out, studio.CollectionHit{EntryID: r.EntryID, Content: r.Content, Similarity: r.Similarity, Metadata: r.Metadata})
	}
	return out, nil
}

func (c studioCollections) memoryCheck(ctx context.Context, req studio.MemoryAuthorization, authorize func(context.Context) error) error {
	if !c.Allowed(ctx, req.Principal) || c.a.studioService == nil || authorize == nil {
		return studio.ErrMemoryDenied
	}
	if err := c.a.studioService.AuthorizeMemory(ctx, req); err != nil {
		return err
	}
	return authorize(ctx)
}
func (c studioCollections) WriteMemory(ctx context.Context, req studio.MemoryRequest, authorize func(context.Context) error) (studio.MemoryResult, error) {
	if req.Delete || strings.TrimSpace(req.Content) == "" || len(req.Content) > studio.MemoryContentBytes || !utf8.ValidString(req.Content) || req.Key == "" {
		return studio.MemoryResult{}, errors.New("invalid memory request")
	}
	if err := c.memoryCheck(ctx, req.MemoryAuthorization, authorize); err != nil {
		return studio.MemoryResult{}, err
	}
	metadata := map[string]string{}
	for k, v := range req.Metadata {
		metadata[k] = v
	}
	metadata["agent_id"] = req.AgentID
	metadata["chat_id"] = req.ChatID
	metadata["capture_source"] = req.Source
	hash, err := lagi.MemoryPayloadHash(req.Content, metadata)
	if err != nil {
		return studio.MemoryResult{}, err
	}
	receipt, err := c.a.CollectionsService().WriteMemory(ctx, c.owner(req.Principal), req.Destination, lagi.MemoryWrite{Key: req.Key, Content: req.Content, Metadata: metadata, PayloadHash: hash}, func(ctx context.Context) error { return c.memoryCheck(ctx, req.MemoryAuthorization, authorize) })
	return studio.MemoryResult{Destination: req.Destination, EntryID: receipt.EntryID}, err
}
func (c studioCollections) DeleteMemory(ctx context.Context, req studio.DeleteMemoryRequest, authorize func(context.Context) error) (result studio.MemoryResult, err error) {
	if c.a.studioMemoryAudit == nil {
		return result, errors.New("memory audit unavailable")
	}
	event := memoryaudit.Event{Principal: req.Principal, Agent: req.AgentID, Chat: req.ChatID, Collection: req.Destination, EntryID: req.EntryID, Outcome: "attempt", At: time.Now().UTC()}
	if err = c.a.studioMemoryAudit.Add(context.WithoutCancel(ctx), event); err != nil {
		return result, err
	}
	defer func() {
		event.Outcome = "deleted"
		if err != nil {
			event.Outcome = "failed"
		}
		event.At = time.Now().UTC()
		if auditErr := c.a.studioMemoryAudit.Add(context.WithoutCancel(ctx), event); auditErr != nil {
			err = errors.Join(err, auditErr)
		}
	}()
	if !req.Delete {
		return result, studio.ErrMemoryDenied
	}
	if err = c.memoryCheck(ctx, req.MemoryAuthorization, authorize); err != nil {
		return result, err
	}
	receipt, err := c.a.CollectionsService().DeleteMemory(ctx, c.owner(req.Principal), req.Destination, req.EntryID, func(ctx context.Context) error { return c.memoryCheck(ctx, req.MemoryAuthorization, authorize) })
	return studio.MemoryResult{Destination: req.Destination, EntryID: receipt.EntryID}, err
}

// captureCheck is synchronous: ctx can carry the queue store's locked snapshot.
func (c studioCollections) captureCheck(ctx context.Context, i memory.Identity, authorize func(context.Context) error) error {
	if !c.Allowed(ctx, i.Principal) || c.a.studioService == nil || authorize == nil {
		return studio.ErrMemoryDenied
	}
	if err := c.a.studioService.AuthorizeCapture(ctx, i); err != nil {
		return err
	}
	return authorize(ctx)
}
func (c studioCollections) BeginCapture(ctx context.Context, i memory.Identity, key, owner string, authorize func(context.Context) error) (lagi.MemoryFence, error) {
	if err := c.captureCheck(ctx, i, authorize); err != nil {
		return lagi.MemoryFence{}, err
	}
	return c.a.CollectionsService().BeginMemoryAttempt(ctx, c.owner(i.Principal), i.Destination, key, owner)
}
func (c studioCollections) ReconcileCapture(ctx context.Context, i memory.Identity, key string) (lagi.MemoryReceipt, error) {
	if err := c.captureCheck(ctx, i, func(context.Context) error { return nil }); err != nil {
		return lagi.MemoryReceipt{}, err
	}
	return c.a.CollectionsService().ReconcileMemory(ctx, c.owner(i.Principal), i.Destination, key)
}
func (c studioCollections) WriteCapture(ctx context.Context, i memory.Identity, fence lagi.MemoryFence, req lagi.MemoryWrite, authorize func(context.Context) error) (lagi.MemoryReceipt, error) {
	return c.a.CollectionsService().WriteMemoryFenced(ctx, c.owner(i.Principal), i.Destination, fence, req, func(ctx context.Context) error { return c.captureCheck(ctx, i, authorize) })
}
