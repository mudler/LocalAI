package distributed_test

import (
	"fmt"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/tests/e2e/distributed/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The two sentinels below are returned by statusOf in place of a node status.
// Neither can ever equal one of the nodes.Status* constants, which is the whole
// point: every assertion in this file compares against a real status with
// Equal, so an unreachable frontend or a vanished row fails the assertion and
// names itself rather than quietly satisfying it.
//
// This is not hypothetical. The obvious way to write "the dead worker is gone"
// is ShouldNot(ContainElement(name)) over a list of healthy names, and the
// probe returns an empty list on any error, so an expired session, a 401 at the
// second replica or a decode failure all satisfy that matcher. The spec would
// go green having observed nothing at all.
const (
	statusUnreachable = "<roster unreachable>"
	statusAbsent      = "<absent from roster>"
)

const (
	// orphanEvictionWindow is how long a spec watches a roster to be sure the
	// system had a real chance to evict a node and chose not to.
	//
	// It is sized from the only eviction path there is. Node liveness is
	// heartbeat freshness: the health monitor wakes every HealthCheckInterval
	// (15s) and marks any node whose last heartbeat is older than
	// StaleNodeThreshold (60s) offline (core/services/nodes/health.go, defaults
	// in core/config/distributed_config.go). Neither is settable from the CLI,
	// so 60s + one 15s tick = 75s is the worst case and cannot be shortened.
	//
	// Measured rather than assumed: a worker whose registrar was killed goes
	// offline at both surviving replicas at t=74.2s. A window shorter than that
	// would be the classic false green, a Consistently that passes because
	// nothing has had time to happen yet. 100s clears the measured latency by a
	// third.
	orphanEvictionWindow = "100s"
	rosterPollInterval   = "2s"

	// workerDeathTimeout bounds the wait for a killed worker to be marked
	// offline. Roughly twice the measured 74.3s, which absorbs a health tick
	// landing just before the kill plus a slow CI runner.
	workerDeathTimeout = "150s"

	// settledStatusWindow is how long an observed status has to hold before the
	// spec believes it. A killed worker does not go straight to offline: it
	// flaps to unhealthy at ~8s and back to healthy at ~14s (see the note in
	// the worker-death spec), so a status has to outlast that transient and two
	// further 15s health ticks to count as the settled state.
	settledStatusWindow = "45s"

	// restartRehydrationTimeout bounds the wait for a cold-restarted replica to
	// answer with the roster. The restart itself measured 1.0s; the budget is
	// for a loaded CI runner, not for a slow code path.
	restartRehydrationTimeout = "60s"

	// frontendExitTimeout bounds the wait for a signalled frontend to be
	// collected. SIGTERM measured 0.2s. It is polled rather than sampled
	// because FrontendAlive reports true for the zombie window between the
	// child exiting and the reaper calling waitid.
	frontendExitTimeout = "30s"
	frontendExitPoll    = "200ms"
)

// statusOf refreshes the roster at the probe's frontend and returns the status
// that frontend reports for name.
//
// It shares rosterProbe's lastErr/lastSeen so describe() still explains a
// failure, but unlike healthyNames it never collapses an error into an empty
// result: the caller is comparing against an exact status, so an error has to
// be a value that no assertion can accept.
func (p *rosterProbe) statusOf(name string) string {
	var roster []node
	if err := p.cluster.GetJSON(p.client, p.frontend, "/api/nodes", &roster); err != nil {
		p.lastErr = err
		return statusUnreachable
	}
	p.lastErr = nil
	p.lastSeen = roster
	for _, n := range roster {
		if n.Name == name {
			return n.Status
		}
	}
	return statusAbsent
}

// explain builds a lazy failure description.
//
// Gomega formats a (string, args...) description as soon as the assertion is
// constructed, which for an Eventually or a Consistently is before anything has
// gone wrong; the roster it quoted would be the one from before the wait. A
// func() string is called only on failure, so describe() reports the last
// observation the assertion actually made.
func (p *rosterProbe) explain(format string, args ...any) func() string {
	return func() string {
		return fmt.Sprintf(format, args...) + ": " + p.describe()
	}
}

// explainStuckOffline is explain with one extra diagnosis attached.
//
// An assertion waiting for offline has a failure mode that looks like a harness
// bug and is not one, so the message names it rather than leaving the reader to
// find it. See proveHealthCheckingIsAlive and the comment on the dead-worker
// spec for the mechanism.
func (p *rosterProbe) explainStuckOffline(worker, format string, args ...any) func() string {
	return func() string {
		message := fmt.Sprintf(format, args...) + ": " + p.describe()
		for _, n := range p.lastSeen {
			if n.Name != worker || n.Status != nodes.StatusUnhealthy {
				continue
			}
			message += "\n\nThe node is stuck at unhealthy, which is a LocalAI defect rather than " +
				"a harness one: core/services/nodes/health.go:153-155 skips MarkOffline for a node " +
				"already marked unhealthy, so a node whose unhealthy mark lands after its heartbeat " +
				"has gone stale never reaches offline at all. Start there, not here."
		}
		return message
	}
}

// proveHealthCheckingIsAlive kills a worker and waits for the roster to settle
// it to offline.
//
// It is the terminating positive control for the two specs that assert a
// healthy worker STAYS healthy. On their own those are pure negative
// assertions: a cluster whose health checking had wedged entirely, say by
// leaking the Postgres advisory lock the monitor takes
// (core/services/nodes/health.go:112), would freeze the roster and satisfy them
// while observing a corpse.
//
// WHAT IT ACTUALLY PROVES, which is less than it looks like. Killing a worker
// afterwards and requiring the roster to react proves the monitor was alive at
// the END of the preceding window. It does not observe the window itself. The
// inference back across it holds only if a wedge would have been sticky, i.e.
// still present when this helper ran.
//
// THE RESIDUAL GAP, and it is not hypothetical in the peer-replica-death spec.
// Health checks are single-flighted across replicas by a session-scoped
// pg_try_advisory_lock (advisorylock.TryWithLockCtx, non-blocking: a replica
// that does not get the lock returns immediately and checks nothing, silently,
// because checkAll discards the acquired flag). That spec SIGKILLs frontend 1,
// which may have been holding the lock at the moment it died. Postgres releases
// a session-level advisory lock only when it reaps the dead backend, so until
// then frontend 0's ticks acquire nothing and no check runs. The roster freezes,
// Consistently(healthy) passes BECAUSE NOTHING WAS CHECKING, and this helper
// still succeeds afterwards once the session is reaped and the lock comes free.
// That wedge is transient rather than permanent, which is exactly the shape the
// backwards inference cannot see. Low probability, real, and bounded by how
// fast Postgres reaps the dead backend, usually immediate on a local socket
// close.
//
// So treat this as a floor and not a proof: it rules out a health monitor that
// is permanently dead, which is the failure that would otherwise make the
// preceding Consistently a statement about a stopped clock, and it does not rule
// out a monitor that was idle for part of the window. Closing the gap needs a
// positive observation from inside the window (a log or metric assertion that a
// check ran), not a stronger assertion here.
//
// It costs a full detection cycle, which is why it is a shared helper: the
// wall-clock price should be paid once per spec and explained once.
func proveHealthCheckingIsAlive(c *cluster.Cluster, probe *rosterProbe, workerIndex int) {
	GinkgoHelper()
	worker := c.WorkerName(workerIndex)
	Expect(c.KillWorker(workerIndex)).To(Succeed())
	Eventually(probe.statusOf, workerDeathTimeout, rosterPollInterval).
		WithArguments(worker).
		Should(Equal(nodes.StatusOffline),
			probe.explainStuckOffline(worker,
				"frontend %d never reacted to a killed worker, so health checking was not running during the window above and the assertion before this one proved nothing",
				probe.frontend))
}

var _ = Describe("Cluster failover", Label("Distributed"), Label("Cluster"), func() {
	It("keeps a healthy worker in the roster when a peer replica dies", func() {
		// Two replicas, one worker. The worker registers and heartbeats with
		// frontend 0 only (the harness default), so frontend 1 is a replica it
		// has never spoken to.
		c := startCluster(2, 1)
		worker := c.WorkerName(0)

		// One session for the whole cluster: register/login/token-login/password
		// share a five-per-minute-per-IP budget at every frontend
		// (core/http/routes/auth.go:190) and everything here comes from
		// 127.0.0.1. The cookie is valid at both replicas because sessions live
		// in the shared Postgres and the harness pins one HMAC secret.
		client, err := c.AdminSession(0)
		Expect(err).ToNot(HaveOccurred())

		survivor := newRosterProbe(c, client, 0)
		Eventually(survivor.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(worker), survivor.describe)

		// Kill the replica this worker never registered with. That choice is
		// what makes the assertion below mean anything.
		//
		// Killing frontend 0 instead would sever the worker's only heartbeat
		// path, because the heartbeat loop posts to the URL it was handed at
		// boot and never re-resolves it; the worker is then genuinely orphaned
		// and IS evicted, at a measured 74.2s. A spec written that way can only
		// pass by watching for less time than the eviction takes.
		Expect(c.WorkerRegistrar(0)).ToNot(Equal(1),
			"this spec kills frontend 1 precisely because worker 0 does not depend on it")
		Expect(c.KillFrontend(1)).To(Succeed())
		Eventually(func() bool { return c.FrontendAlive(1) }, frontendExitTimeout, frontendExitPoll).
			Should(BeFalse(), "frontend 1 did not die, so nothing below is a failover assertion")

		// The survivor must keep answering, and must keep the worker healthy.
		//
		// A GET that returns 200 with a decodable roster is the "keeps serving"
		// half; statusUnreachable would fail this matcher. The window outlasts
		// the full 75s stale-plus-one-tick eviction path, so an implementation
		// that reacted to a dead peer by sweeping its nodes, by resetting
		// heartbeats, or by marking the whole roster stale would be caught
		// whether it reacted immediately or on a health tick.
		Consistently(survivor.statusOf, orphanEvictionWindow, rosterPollInterval).
			WithArguments(worker).
			Should(Equal(nodes.StatusHealthy),
				survivor.explain("killing a peer replica must not disturb a worker that never depended on it"))

		// Everything above is a negative: nothing happened. Prove that the
		// survivor was capable of making something happen the whole time.
		proveHealthCheckingIsAlive(c, survivor, 0)
	})

	It("rediscovers the worker from shared state after a cold rolling restart", func() {
		c := startCluster(2, 1)
		worker := c.WorkerName(0)

		client, err := c.AdminSession(0)
		Expect(err).ToNot(HaveOccurred())

		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(worker), probe.describe)
		registeredID := probe.idOf(worker)
		Expect(registeredID).ToNot(BeEmpty(), "frontend 0 reported the worker without a registration ID")

		// The rolling-update shape: drain, wait for the process to actually go,
		// then bring the replacement up. Restarting without that wait would
		// SIGKILL the replica mid-drain and silently turn this into the crash
		// case.
		Expect(c.StopFrontendGracefully(0)).To(Succeed())
		Eventually(func() bool { return c.FrontendAlive(0) }, frontendExitTimeout, frontendExitPoll).
			Should(BeFalse(), "frontend 0 ignored SIGTERM, so the restart below would be a SIGKILL mid-drain")

		// RestartFrontend wipes the replica's data directory, so the process
		// that comes back has no local memory of the cluster. Everything the
		// assertions below observe has to come out of the shared Postgres.
		Expect(c.RestartFrontend(0)).To(Succeed())

		restarted := newRosterProbe(c, client, 0)
		Eventually(restarted.statusOf, restartRehydrationTimeout, nodeRosterPoll).
			WithArguments(worker).
			Should(Equal(nodes.StatusHealthy),
				restarted.explain("a replica with an empty data directory must rehydrate the roster from shared state"))
		Expect(restarted.idOf(worker)).To(Equal(registeredID),
			"the restarted replica invented a new row for the worker instead of resolving the shared one")

		// Rehydration alone is a weak claim: the row was written before the
		// restart and would still read healthy for up to 75s even if the
		// replacement never accepted another heartbeat. Holding it past that
		// window is what proves the worker's heartbeats are landing again,
		// which is the part a restart can plausibly break (a replacement on a
		// different port, or one that rejects the node id it did not issue).
		Consistently(restarted.statusOf, orphanEvictionWindow, rosterPollInterval).
			WithArguments(worker).
			Should(Equal(nodes.StatusHealthy),
				restarted.explain("the worker went stale after the restart, so its heartbeats are not reaching the replacement"))

		// Same hole as the peer-death spec, and it is worse here: a cold
		// restart is exactly the event that could leave a replacement unable to
		// run health checks at all, and a frozen roster reads identically to a
		// healthy one. This is the assertion that tells the two apart.
		proveHealthCheckingIsAlive(c, restarted, 0)
	})

	It("settles a dead worker to offline and both replicas report it offline", func() {
		c := startCluster(2, 1)
		worker := c.WorkerName(0)

		client, err := c.AdminSession(0)
		Expect(err).ToNot(HaveOccurred())

		at0 := newRosterProbe(c, client, 0)
		at1 := newRosterProbe(c, client, 1)
		Eventually(at0.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(worker), at0.describe)

		Expect(c.KillWorker(0)).To(Succeed())

		// Assert the settled status, not the absence of a healthy name.
		//
		// A killed worker does not move monotonically. Measured: healthy until
		// ~8s, unhealthy at 8s, healthy again at 14s, offline from 74s. The
		// unhealthy blip comes from a liveness probe; the health monitor's
		// "heartbeat is still fresh" branch then marks it healthy again
		// (core/services/nodes/health.go), and only the stale-heartbeat branch
		// reaches MarkOffline. So requiring exactly offline is what pins this to
		// the stale-detection path rather than to the transient, which any
		// not-healthy or not-present matcher would accept at t=8s.
		//
		// KNOWN HAZARD, read this before blaming the harness for a timeout here.
		// The staleness branch skips a node that is already unhealthy
		// (core/services/nodes/health.go:153-155, `if node.Status ==
		// StatusOffline || node.Status == StatusUnhealthy { continue }`). The
		// skip exists to stop the monitor re-logging nodes an operator took
		// down, but it applies to the flap too: if the transient unhealthy mark
		// lands AFTER the heartbeat has already gone stale, rather than at the
		// ~8s observed here, MarkOffline is never called and this node stays
		// unhealthy forever. This spec would then hang to workerDeathTimeout
		// and fail with a roster that looks perfectly ordinary. The ordering
		// that triggers it did not occur in any run so far, but nothing
		// prevents it, so explainStuckOffline says so in the failure message
		// when it sees a node stuck at unhealthy. Fixing it is LocalAI work,
		// not test work.
		// Reading the same verdict at both replicas proves shared-verdict
		// propagation, NOT two independent detectors. Health checks are
		// single-flighted by the advisory lock (see proveHealthCheckingIsAlive),
		// so exactly one replica ran the check that wrote the offline status, and
		// both probes then read that one Postgres row back. What this rules out
		// is a replica that keeps a private roster, or one that reads the shared
		// row and reports something else. A spec claiming both replicas can
		// detect death on their own would have to isolate them from each other,
		// which the shared database makes impossible by design.
		for _, probe := range []*rosterProbe{at0, at1} {
			Eventually(probe.statusOf, workerDeathTimeout, rosterPollInterval).
				WithArguments(worker).
				Should(Equal(nodes.StatusOffline),
					probe.explainStuckOffline(worker, "frontend %d never settled the dead worker to offline", probe.frontend))
		}

		// And it has to stay offline. Nothing may resurrect a row for a process
		// that no longer exists, and this window covers three health ticks.
		for _, probe := range []*rosterProbe{at0, at1} {
			Consistently(probe.statusOf, settledStatusWindow, rosterPollInterval).
				WithArguments(worker).
				Should(Equal(nodes.StatusOffline),
					probe.explain("frontend %d flipped the dead worker away from offline", probe.frontend))
		}
	})

	It("converges on one roster when two replicas register a worker each", func() {
		// SpreadWorkerRegistrations sends worker 0 to frontend 0 and worker 1 to
		// frontend 1, so the roster is written through two different replicas.
		//
		// This is a shared-roster identity test, NOT a concurrency test, and the
		// distinction matters because the obvious reading of the spec name is
		// the wrong one. Start spawns workers one after another and waits for
		// neither, and the registrations land about a second apart in practice;
		// there is no synchronisation point and nothing here is tuned to make
		// the two writes collide. What it does establish is that a roster
		// written through two replicas is one roster and not two: same rows,
		// same identities, read back from either process. A genuine concurrent
		// registration test would need workers released together against a
		// shared barrier, and does not exist yet.
		c := startCluster(2, 2, func(o *cluster.Options) {
			o.SpreadWorkerRegistrations = true
		})
		registrar0, err := c.WorkerRegistrar(0)
		Expect(err).ToNot(HaveOccurred())
		registrar1, err := c.WorkerRegistrar(1)
		Expect(err).ToNot(HaveOccurred())
		Expect(registrar0).ToNot(Equal(registrar1),
			"both workers registered through the same replica, so nothing below says anything about two replicas sharing a roster")

		client, err := c.AdminSession(0)
		Expect(err).ToNot(HaveOccurred())

		at0 := newRosterProbe(c, client, 0)
		at1 := newRosterProbe(c, client, 1)
		expected := []string{c.WorkerName(0), c.WorkerName(1)}

		// ConsistOf, not ContainElements: it fails on a third entry, which is
		// how a duplicated row for one worker would show up.
		Eventually(at0.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ConsistOf(expected), at0.describe)
		Eventually(at1.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ConsistOf(expected), at1.describe)

		// Same names is not the same roster. Compare the registration ids, which
		// is the only way to tell "both replicas read one set of rows" from
		// "each replica has its own row per worker that happens to share a
		// name".
		for _, name := range expected {
			id := at0.idOf(name)
			Expect(id).ToNot(BeEmpty(), fmt.Sprintf("frontend 0 reported %s without a registration ID", name))
			Expect(at1.idOf(name)).To(Equal(id),
				fmt.Sprintf("the replicas disagree on the identity of %s, so they are not sharing one roster", name))
		}
	})
})
