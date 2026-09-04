// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"github.com/mudler/LocalAI/core/services/pgbus"
)

// The four process-lifetime caches, each wired onto the broadcast carrier by
// one function here, and none of them by a call site naming a carrier.
//
// Two are methods on DistributedServices and take NO carrier at all, because
// their call sites hold the whole struct and could otherwise reach for the
// cancel carrier.
// The other two run inside initDistributed before that struct exists, so they
// take the CONCRETE *pgbus.Bus rather than messaging.Broadcaster.
//
// Concrete on purpose. *messaging.Client satisfies messaging.Broadcaster just
// as well as the carrier does, so an interface parameter at these sites lets a
// caller hand over the NATS client that is also in scope: it compiles, it
// starts, it publishes, it is delivered, onto a carrier the deployment is being
// taken off, and nothing fails until NATS goes away. With the concrete type
// that mistake is a build error rather than a deployment that looks healthy.
//
// The adopters themselves still take the interface, so their own specs drive
// them with an in-memory double. The narrowing is only here, where the wrong
// carrier is in scope.

// wireGalleryBroadcasts puts the gallery service's progress, cancel and
// cache-invalidation traffic on the carrier, and opens the wildcard
// subscriptions that mirror a peer's.
//
// Set and subscribe in one call because they are one decision: a service that
// published where nobody listened would show every operation it started and
// none of its peers', which is what /api/operations looks like on a replica
// that has been load-balanced away from.
//
// The caller must have hydrated from the store and bound OnModelsChanged
// first; both are stated on the methods themselves.
func (ds *DistributedServices) wireGallery(gs *galleryop.GalleryService) error {
	if ds == nil || ds.Bus == nil {
		return fmt.Errorf("wiring gallery broadcasts: no broadcast carrier, so gallery progress and cancels would reach no peer replica")
	}
	if gs == nil {
		return nil
	}
	gs.SetBroadcaster(ds.Bus)
	return gs.SubscribeBroadcasts()
}

// WireOpCache puts the admin operation cache on the carrier and starts it,
// which hydrates from PostgreSQL and subscribes.
//
// Exported, unlike its siblings, because the OpCache is built in the HTTP layer
// rather than in initDistributed. It takes neither a carrier nor a store: both
// come off this struct, so the HTTP layer cannot pass the NATS client that
// hangs off it beside them.
//
// A hydrate failure is the OpCache's own business and is logged there; a
// subscribe failure is returned, because a cache that hydrated and did not
// subscribe reports the operations it found at boot and never learns of another.
func (ds *DistributedServices) WireOpCache(ctx context.Context, cache *galleryop.OpCache) error {
	if cache == nil {
		return nil
	}
	if ds == nil || ds.Bus == nil {
		return fmt.Errorf("wiring the operation cache: no broadcast carrier, so /api/operations would answer with whatever this one replica admitted")
	}
	cache.SetBroadcaster(ds.Bus)
	if ds.DistStores != nil && ds.DistStores.Gallery != nil {
		cache.SetGalleryStore(ds.DistStores.Gallery)
	}
	return cache.Start(ctx)
}

// wireStagingBroadcasts puts file-staging progress on the carrier in both
// directions. The tracker's own SetBroadcaster is what makes those one call;
// see the comment there.
func wireStagingBroadcasts(bus *pgbus.Bus, tracker *nodes.StagingTracker) (messaging.Subscription, error) {
	if bus == nil {
		return nil, fmt.Errorf("wiring staging broadcasts: no broadcast carrier, so a staging transfer would show a progress bar only on the replica performing it")
	}
	if tracker == nil {
		return nil, nil
	}
	return tracker.SetBroadcaster(bus)
}

// wirePrefixCacheBroadcasts builds the cross-frontend prefix-cache layer on the
// carrier and subscribes it to peers, after refusing a configuration whose
// observations could not travel in a notification.
func wirePrefixCacheBroadcasts(bus *pgbus.Bus, cfg prefixcache.Config, idx prefixcache.Provider) (*prefixcache.Sync, error) {
	// The configuration first, and the carrier second. A depth this carrier
	// cannot hold is wrong whether or not a carrier was supplied, and naming
	// the more specific fault is what makes the startup message actionable.
	if err := requirePrefixCacheFitsInline(cfg); err != nil {
		return nil, err
	}
	if bus == nil {
		return nil, fmt.Errorf("wiring the prefix cache: no broadcast carrier, so each frontend would route on nothing but its own history")
	}
	sync := prefixcache.NewSync(idx, bus)
	if _, err := sync.SubscribeBroadcasts(); err != nil {
		return nil, err
	}
	return sync, nil
}

// prefixCacheIdentifierAllowance is how many bytes of model id plus node id a
// prefix-cache observation is budgeted for when its worst case is checked
// against the notification cap.
//
// Both are operator-chosen strings with no enforced length, so no bound here is
// a proof. It does not need to be one: an observation that does not fit is
// SPILLED like any other broadcast, at the cost of a row and a SELECT, and is
// never lost. What the check exists to catch is the other failure, the one that
// has no symptom: a change to Config.MaxDepth that quietly puts every
// observation over the cap and turns the inference path into a table write per
// request. A generous allowance catches that and does not fire on a long model
// name.
const prefixCacheIdentifierAllowance = 512

// requirePrefixCacheFitsInline refuses a prefix-cache configuration whose
// observations would spill.
//
// prefixcache.ExtractChain caps a chain at Config.MaxDepth blocks, so an
// observation's size has a worst case that is known before the deployment
// serves a request: MaxDepth hashes at their widest decimal encoding, plus the
// identifiers. That bound is the reason Sync.Observe can publish like every
// other family instead of being given a way to refuse.
//
// It is a startup error and not a warning because the alternative reading is
// the one this programme exists to remove: a deployment that came up, spills a
// row and reads it back on every replica for every request whose prefix
// changed, and looks exactly like one that is merely slow.
func requirePrefixCacheFitsInline(cfg prefixcache.Config) error {
	// The widest a uint64 encodes to in JSON, so the check does not depend on
	// which hashes a workload happens to produce.
	chain := make([]uint64, cfg.MaxDepth)
	for i := range chain {
		chain[i] = math.MaxUint64
	}
	worst := messaging.PrefixCacheObserveEvent{
		Model:   strings.Repeat("m", prefixCacheIdentifierAllowance/2),
		Chain:   chain,
		NodeID:  strings.Repeat("n", prefixCacheIdentifierAllowance/2),
		Replica: math.MaxInt32,
	}
	fits, err := pgbus.FitsInline(messaging.SubjectPrefixCacheObserve, worst)
	if err != nil {
		return fmt.Errorf("sizing a prefix-cache observation: %w", err)
	}
	if !fits {
		return fmt.Errorf("the prefix-cache depth is too large to broadcast: an observation for %d blocks does not fit in one notification, so every request whose prefix changed would write a row and every replica would read it back on the inference path", cfg.MaxDepth)
	}
	return nil
}
