// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"sync/atomic"
	"time"
)

// addonsReachableTTL is how long a frontend keeps its answer about the owner.
// It keeps /api/features cheap: the owner is not dialled for every request.
const addonsReachableTTL = 10 * time.Second

// addonsProbe is one cached answer of a frontend about the owner.
type addonsProbe struct {
	reachable bool
	at        time.Time
}

// addonsProbeState is per-instance state: two frontends can disagree for up to
// addonsReachableTTL after the owner goes up or down (mode 4 of
// .agents/distributed-state.md). Two requests that probe at the same time both
// store an answer; the last one wins, which is harmless.
type addonsProbeState struct {
	last atomic.Pointer[addonsProbe]
	// now and ttl are set by tests only.
	now func() time.Time
	ttl time.Duration
}

func (s *addonsProbeState) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *addonsProbeState) lifetime() time.Duration {
	if s.ttl > 0 {
		return s.ttl
	}
	return addonsReachableTTL
}

// AddonsEnabled reports the instance switch alone, so a refusal can tell a
// disabled feature from an owner that does not answer.
func (a *Application) AddonsEnabled() bool { return a.applicationConfig.Addons.Enabled }

// AddonsFeature is the one place that decides whether the add-on surfaces are
// on for this instance. The routes and the UI read it; nothing else checks
// Addons.Enabled for this purpose.
//
// A combined or owner process answers true when add-ons are enabled and it
// holds the owner manager. A frontend answers true when add-ons are enabled
// and the owner answered the carrier's audience call, cached for ten seconds.
func (a *Application) AddonsFeature(ctx context.Context) bool {
	cfg := a.applicationConfig.Addons
	if !cfg.Enabled || a.addonManager == nil {
		return false
	}
	if cfg.InstanceID == cfg.OwnerID {
		return true
	}
	g := a.addonGateway
	if g == nil || g.Forward == nil {
		return false
	}
	s := &a.addonsProbe
	if p := s.last.Load(); p != nil && s.clock().Sub(p.at) < s.lifetime() {
		return p.reachable
	}
	ok := g.Forward.Reachable(ctx) == nil
	s.last.Store(&addonsProbe{reachable: ok, at: s.clock()})
	return ok
}
