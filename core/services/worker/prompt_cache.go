package worker

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/services/promptcachesvc"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// registerPromptCacheVerbs serves promptcache.sync and promptcache.usage on a
// control server. One promptcachesvc.Worker serves every route, so a sync that
// two routes deliver at once still runs one at a time.
func registerPromptCacheVerbs(srv controlServer, w *promptcachesvc.Worker) error {
	if err := srv.handle(verbPromptCacheSync, unary(decodeJSON[workerctl.PromptCacheSyncRequest], refusePromptCacheSync,
		func(ctx context.Context, req workerctl.PromptCacheSyncRequest) workerctl.PromptCacheSyncReply {
			return w.Sync(ctx, req)
		})); err != nil {
		return err
	}
	return srv.handle(verbPromptCacheUsage, unary(ignoreBody[workerctl.PromptCacheUsageRequest], refuseNever[workerctl.PromptCacheUsageReply],
		func(ctx context.Context, _ workerctl.PromptCacheUsageRequest) workerctl.PromptCacheUsageReply {
			return w.Usage(ctx)
		}))
}

func refusePromptCacheSync(err error) workerctl.PromptCacheSyncReply {
	xlog.Warn("Ignoring malformed control request", "verb", verbPromptCacheSync, "error", err)
	return workerctl.PromptCacheSyncReply{Error: fmt.Sprintf("invalid request: %v", err)}
}
