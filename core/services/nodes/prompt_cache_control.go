package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// PromptCacheControl sends the verbs of the disk prompt cache to one worker:
// promptcache.sync (the cluster settings and the purges the worker has not
// applied) and promptcache.usage. Like model.prefetch it sits outside
// NodeCommandSender: a route that cannot send it is a route that does not
// exist, and a worker that does not serve it is older than the verbs, which
// both read as ErrNoRoute. The worker owns its cache files; the frontend only
// asks it to act on them.
type PromptCacheControl interface {
	PromptCacheSync(ctx context.Context, nodeID string, req workerctl.PromptCacheSyncRequest, timeout time.Duration) (*workerctl.PromptCacheSyncReply, error)
	PromptCacheUsage(ctx context.Context, nodeID string, timeout time.Duration) (*workerctl.PromptCacheUsageReply, error)
}

var _ PromptCacheControl = (*nodeControl)(nil)

// PromptCacheSync sends promptcache.sync over the link of the carrier.
func (a *nodeControl) PromptCacheSync(ctx context.Context, nodeID string, req workerctl.PromptCacheSyncRequest, timeout time.Duration) (*workerctl.PromptCacheSyncReply, error) {
	return promptCacheCall[workerctl.PromptCacheSyncRequest, workerctl.PromptCacheSyncReply](ctx, a.link, nodeID, workerctl.VerbPromptCacheSync, req, timeout)
}

// PromptCacheUsage sends promptcache.usage over the link of the carrier.
func (a *nodeControl) PromptCacheUsage(ctx context.Context, nodeID string, timeout time.Duration) (*workerctl.PromptCacheUsageReply, error) {
	return promptCacheCall[workerctl.PromptCacheUsageRequest, workerctl.PromptCacheUsageReply](ctx, a.link, nodeID, workerctl.VerbPromptCacheUsage, workerctl.PromptCacheUsageRequest{}, timeout)
}

// promptCacheCall bounds the call by the deadline of ctx and maps a worker
// that does not serve the verb onto ErrNoRoute.
func promptCacheCall[Req, Reply any](ctx context.Context, link controlLink, nodeID, verb string, req Req, timeout time.Duration) (*Reply, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	reply, err := callVerb[Req, Reply](ctx, link, nodeID, verb, req, timeout)
	if err != nil && errors.Is(err, errVerbNotServed) {
		err = fmt.Errorf("%w: %v", ErrNoRoute, err)
	}
	return reply, err
}
