// SPDX-License-Identifier: MIT

package application

import (
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// distributedSchedulerOptions stamps the absence wiring onto the scheduler's
// options and returns them.
//
// Two assignments in a named function rather than two more fields in the
// twenty-field literal they used to live in. The literal cannot be reached by a
// unit spec, because the function that builds it also opens a NATS connection
// and a database; these two lines can, and they are the two lines this whole
// change comes down to. Losing them in the literal was silent and green.
//
// The grace comes from the same expression the membership loop is given
// (Membership.SetReconnectGrace), so the window a departure is measured against
// and the window a departure is RETAINED for cannot drift apart.
func distributedSchedulerOptions(cfg config.DistributedConfig, presence nodes.NodePresenceReader, opts nodes.SmartRouterOptions) nodes.SmartRouterOptions {
	opts.Presence = presence
	opts.ReconnectGrace = cfg.ReconnectGraceOrDefault()
	return opts
}

// requireAbsenceWiring refuses to start a distributed deployment in which
// nothing can decide that a worker has gone away.
//
// Two components read absence, from one source and against one window: the
// scheduler, which stops placing work on a departed worker, and the health
// monitor, which stops reporting one as healthy. Each reads it through a field
// assigned in a large construction literal in initDistributed.
//
// It is checked rather than assumed because losing either assignment is
// SILENT. A scheduler with no absence source places work on workers that are
// gone and demotes none; a health monitor with none reports a worker whose
// tunnel died an hour ago as healthy, forever, with every request for a model
// loaded on it failing "no route to that worker". Neither logs anything,
// neither fails a request that would not have failed anyway, and both look
// exactly like a fleet that is fine. Refusing to boot is the only symptom
// either failure has, and it is the reason this is a startup error and not a
// warning: a deployment that came up and quietly decided absence by nothing is
// the state the tunnel work exists to remove.
//
// What this guard itself rests on, stated because it is a real limit: the two
// helper functions below and above are pinned by unit specs, but the CALL to
// this one lives in initDistributed, which opens NATS and a database and so has
// no unit spec at all. Deleting the call, or writing a literal nil where
// initDistributed passes the cluster registry, compiles and leaves every suite
// in this repository green. Only tests/e2e/distributed/cluster catches it, by
// booting the real binary: the error is returned from initDistributed and
// aborts application startup, so a frontend so wired never comes up.
func requireAbsenceWiring(router *nodes.SmartRouter, health *nodes.HealthMonitor) error {
	if !router.ReadsAbsence() {
		return fmt.Errorf("the distributed scheduler was built with no source of worker absence: it would place work on workers that have gone away and never demote one")
	}
	if !health.ReadsAbsence() {
		return fmt.Errorf("the node health monitor was built with no source of worker absence: it would report a worker whose tunnel is gone as healthy indefinitely")
	}
	return nil
}
