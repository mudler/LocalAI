// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"errors"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// stubConnections decides what is connected, so a selection spec can state the
// cluster state it is about in one line instead of staging claim rows and
// instance heartbeats to produce it. What the real read answers for a given
// cluster state is pinned against the real database in
// core/services/cluster/connected_among_test.go, and the relayed round trip in
// agent_control_test.go drives the real registry over a real transport.
type stubConnections struct {
	held        []string
	heldByOwner []string
	err         error

	// owners records the owner id each call was made with. An empty one makes
	// heldByOwner always empty in production, so every call takes a relay hop
	// with nothing anywhere saying so.
	owners []string
	// asked records the candidate list of each call.
	asked [][]string

	// presence is what Presence answers per node, and presenceErr makes it
	// fail. graces records the window each Presence call was made with: a
	// selector that passed zero would call every worker that lost its tunnel a
	// moment ago GONE, and silently drop the cancels addressed to it.
	presence    map[string]cluster.Presence
	presenceErr error
	graces      []time.Duration
}

// testGrace is the reconnect window these specs pass. A real value rather than
// zero, because zero is what NewAgentSelector substitutes a default for, and a
// spec that passed it could not tell the substitution from the value.
const testGrace = 42 * time.Second

// presenceOf answers what the deployment can say about a node's tunnel. The
// zero value is cluster.PresenceUnknown, which is the value nobody may act on,
// so a spec that forgets to state a node's presence cannot accidentally get the
// one answer (PresenceGone) that licenses a caller to conclude something.
func (s *stubConnections) Presence(_ context.Context, nodeID string, grace time.Duration) (cluster.Presence, error) {
	s.graces = append(s.graces, grace)
	if s.presenceErr != nil {
		return cluster.PresenceUnknown, s.presenceErr
	}
	return s.presence[nodeID], nil
}

func (s *stubConnections) ConnectedAmong(_ context.Context, nodeIDs []string, owner string) ([]string, []string, error) {
	s.owners = append(s.owners, owner)
	s.asked = append(s.asked, append([]string(nil), nodeIDs...))
	if s.err != nil {
		return nil, nil, s.err
	}
	// The real read answers only about ids it was given, so the stub must too:
	// a stub that answered about a node the selector had excluded would hide an
	// exclusion that stopped working.
	offered := map[string]bool{}
	for _, id := range nodeIDs {
		offered[id] = true
	}
	var held, byOwner []string
	for _, id := range s.held {
		if offered[id] {
			held = append(held, id)
		}
	}
	for _, id := range s.heldByOwner {
		if offered[id] {
			byOwner = append(byOwner, id)
		}
	}
	return held, byOwner, nil
}

// The selection that replaces the queue group. Two ways it can be wrong, and
// both are the invariant rather than a preference: picking a node no live
// replica holds sends a request at a process that is gone, and refusing a node
// that is merely re-homing between replicas takes MCP down for a fleet that is
// fine.
var _ = Describe("AgentSelector", func() {
	var (
		ctx      context.Context
		db       *gorm.DB
		registry *NodeRegistry
		conns    *stubConnections
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx = context.Background()
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		conns = &stubConnections{}
	})

	register := func(name, nodeType string) string {
		GinkgoHelper()
		node := &BackendNode{Name: name, NodeType: nodeType, Address: name + ":50051"}
		Expect(registry.Register(ctx, node, true)).To(Succeed())
		fetched, err := registry.GetByName(ctx, name)
		Expect(err).ToNot(HaveOccurred())
		return fetched.ID
	}

	// The repeat count every preference assertion uses. The tie-break is random
	// by design, so a single call proves nothing: with two candidates a
	// selector that ignored the preference entirely would pass one run in two.
	const picks = 20

	It("prefers an agent whose tunnel THIS replica holds, so the call skips the relay", func() {
		mine := register("agent-mine", NodeTypeAgent)
		theirs := register("agent-theirs", NodeTypeAgent)
		conns.held = []string{mine, theirs}
		conns.heldByOwner = []string{mine}

		sel := NewAgentSelector(registry, conns, "me", testGrace)
		for range picks {
			id, nodeType, err := sel.PickConnected(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(id).To(Equal(mine))
			// The node type is what a later re-broadcast checks its allow list
			// against, and an unknown type is DENIED there. An empty one would
			// present only as progress that never reaches a browser.
			Expect(nodeType).To(Equal(NodeTypeAgent))
			Expect(nodeType).ToNot(BeEmpty())
		}
		Expect(conns.owners).To(HaveLen(picks))
		Expect(conns.owners[0]).To(Equal("me"),
			"the connection read must be asked which tunnels THIS replica holds")
	})

	It("takes a peer-held agent when this replica holds none", func() {
		theirs := register("agent-theirs", NodeTypeAgent)
		conns.held = []string{theirs}

		sel := NewAgentSelector(registry, conns, "me", testGrace)
		for range picks {
			id, nodeType, err := sel.PickConnected(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(id).To(Equal(theirs))
			Expect(nodeType).To(Equal(NodeTypeAgent))
		}
	})

	It("spreads its picks over the agents it owns rather than pinning one", func() {
		// Random, not "the first one". A selector that returned candidates[0]
		// would pass every assertion above and send the whole deployment's MCP
		// traffic to one worker.
		a := register("agent-a", NodeTypeAgent)
		b := register("agent-b", NodeTypeAgent)
		conns.held = []string{a, b}
		conns.heldByOwner = []string{a, b}

		sel := NewAgentSelector(registry, conns, "me", testGrace)
		seen := map[string]int{}
		// 40 draws of a fair two-way choice miss one side with probability
		// 2^-39, which is far below the flake floor of anything else here.
		for range 2 * picks {
			id, _, err := sel.PickConnected(ctx)
			Expect(err).ToNot(HaveOccurred())
			seen[id]++
		}
		Expect(seen).To(HaveKey(a))
		Expect(seen).To(HaveKey(b))
	})

	Describe("when nothing can be picked", func() {
		assertNoWorker := func(id, nodeType string, err error) {
			GinkgoHelper()
			Expect(err).To(MatchError(ErrNoAgentWorker))
			// The taxonomy assertion, and the reason this sentinel is its own.
			// ErrWorkerUnroutable is what every path that DELETES a node_models
			// row matches on, and IsWorkerAnswer is what a reap guard acts on.
			// An empty agent fleet is evidence about neither: nothing was asked
			// of any worker.
			Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeFalse())
			Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
			// An id returned beside the error would be dialled by a caller that
			// checked the id first, which is a caller mistake this makes
			// impossible rather than one it documents.
			Expect(id).To(BeEmpty())
			Expect(nodeType).To(BeEmpty())
		}

		It("refuses when registered agents exist but none is connected", func() {
			register("agent-a", NodeTypeAgent)
			sel := NewAgentSelector(registry, conns, "me", testGrace)
			assertNoWorker(sel.PickConnected(ctx))
		})

		It("refuses when the deployment has no agent nodes at all", func() {
			sel := NewAgentSelector(registry, conns, "me", testGrace)
			assertNoWorker(sel.PickConnected(ctx))
		})

		It("refuses when it was built with no way to read connections", func() {
			assertNoWorker(NewAgentSelector(registry, nil, "me", testGrace).PickConnected(ctx))
		})
	})

	It("never offers a BACKEND worker, which serves no MCP verb at all", func() {
		backend := register("backend-1", NodeTypeBackend)
		agent := register("agent-a", NodeTypeAgent)
		// The connection read is told everything is connected; only the
		// candidate list keeps the backend worker out, so a selector that
		// listed every node would pick it and get a 404 it would read as a
		// version skew.
		conns.held = []string{backend, agent}
		conns.heldByOwner = []string{backend, agent}

		sel := NewAgentSelector(registry, conns, "me", testGrace)
		for range picks {
			id, _, err := sel.PickConnected(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(id).To(Equal(agent))
		}
		Expect(conns.asked[0]).To(ConsistOf(agent))
	})

	It("never offers an agent an admin has not approved", func() {
		node := &BackendNode{Name: "agent-pending", NodeType: NodeTypeAgent, Address: "p:50051"}
		Expect(registry.Register(ctx, node, false)).To(Succeed())
		pending, err := registry.GetByName(ctx, "agent-pending")
		Expect(err).ToNot(HaveOccurred())
		Expect(pending.Status).To(Equal(StatusPending))
		conns.held = []string{pending.ID}
		conns.heldByOwner = []string{pending.ID}

		sel := NewAgentSelector(registry, conns, "me", testGrace)
		_, _, err = sel.PickConnected(ctx)
		Expect(err).To(MatchError(ErrNoAgentWorker))
		Expect(conns.asked[0]).To(BeEmpty())
	})

	It("never offers an agent an operator has put into draining", func() {
		id := register("agent-draining", NodeTypeAgent)
		Expect(registry.MarkDraining(ctx, id)).To(Succeed())
		conns.held = []string{id}
		conns.heldByOwner = []string{id}

		sel := NewAgentSelector(registry, conns, "me", testGrace)
		_, _, err := sel.PickConnected(ctx)
		Expect(err).To(MatchError(ErrNoAgentWorker))
	})

	It("still offers an agent the health monitor has marked unhealthy", func() {
		// The invariant, at the one place in this task it can break subtly.
		// Whether a request can reach this worker RIGHT NOW is the connection
		// rows' answer; a health verdict is written on another clock, and
		// letting it filter the candidates would refuse a worker that is
		// connected and answering because a probe was late.
		id := register("agent-a", NodeTypeAgent)
		Expect(registry.MarkUnhealthy(ctx, id)).To(Succeed())
		conns.held = []string{id}
		conns.heldByOwner = []string{id}

		sel := NewAgentSelector(registry, conns, "me", testGrace)
		picked, _, err := sel.PickConnected(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(picked).To(Equal(id))
	})

	// Reachable is what a FAN-OUT verb asks, and it differs from a pick in
	// exactly one way that matters: it has to say what happened to the workers
	// it could not reach. A cancel assembled from a fleet it only partly asked
	// reports "no worker is running that execution" about a run that is.
	Describe("Reachable", func() {
		It("separates a worker that is GONE from one that is merely reconnecting", func() {
			// The whole invariant in one spec. A departure older than the grace
			// is the one routing fact a caller may act on, so it disappears; a
			// departure inside it is an absent connection nobody may act on, so
			// it must be named.
			live := register("agent-live", NodeTypeAgent)
			reconnecting := register("agent-reconnecting", NodeTypeAgent)
			gone := register("agent-gone", NodeTypeAgent)
			conns.held = []string{live}
			conns.presence = map[string]cluster.Presence{
				reconnecting: cluster.PresenceReconnecting,
				gone:         cluster.PresenceGone,
			}

			reach, err := NewAgentSelector(registry, conns, "me", testGrace).Reachable(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(reach.Connected).To(ConsistOf(live))
			Expect(reach.Absent).To(ConsistOf(reconnecting))
		})

		It("counts a worker that has never dialled as absent, not as gone", func() {
			// PresenceUnknown is a worker that has never dialled or whose
			// departure aged out of retention, and the registry cannot say
			// which. Reading it as gone would license a caller to conclude
			// something about a worker it has learned nothing about.
			never := register("agent-never", NodeTypeAgent)

			reach, err := NewAgentSelector(registry, conns, "me", testGrace).Reachable(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(reach.Connected).To(BeEmpty())
			Expect(reach.Absent).To(ConsistOf(never))
		})

		It("counts a presence it could not read as absent, because a failed read is not an answer", func() {
			id := register("agent-a", NodeTypeAgent)
			conns.presenceErr = errors.New("the database would not answer")

			reach, err := NewAgentSelector(registry, conns, "me", testGrace).Reachable(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(reach.Absent).To(ConsistOf(id))
		})

		It("measures a departure against the grace it was built with", func() {
			// A zero window would call every worker that lost its tunnel a
			// moment ago GONE, and the cancels addressed to it would be
			// reported as a run nobody is running.
			register("agent-a", NodeTypeAgent)

			_, err := NewAgentSelector(registry, conns, "me", testGrace).Reachable(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(conns.graces).To(ConsistOf(testGrace))
		})

		It("substitutes the deployment default for a grace of zero", func() {
			register("agent-a", NodeTypeAgent)

			_, err := NewAgentSelector(registry, conns, "me", 0).Reachable(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(conns.graces).To(ConsistOf(config.DefaultWorkerReconnectGrace))
		})

		It("excludes a PENDING node, which can hold no execution to cancel", func() {
			// An unapproved node is refused by the tunnel route on every dial.
			// Counting it would make every cancel in a deployment with one
			// unapproved agent node report undelivered for ever.
			pending := &BackendNode{Name: "agent-pending", NodeType: NodeTypeAgent, Address: "p:50051"}
			Expect(registry.Register(ctx, pending, false)).To(Succeed())

			reach, err := NewAgentSelector(registry, conns, "me", testGrace).Reachable(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(reach.Connected).To(BeEmpty())
			Expect(reach.Absent).To(BeEmpty())
		})

		It("still offers a DRAINING worker, which may take no new work but is finishing what it holds", func() {
			// The one status where a fan-out and a pick must disagree. A
			// draining worker is still running the executions it took, so
			// leaving it out would report the cancel of a live run as a run
			// that no worker is running.
			id := register("agent-draining", NodeTypeAgent)
			Expect(registry.MarkDraining(ctx, id)).To(Succeed())
			conns.held = []string{id}

			reach, err := NewAgentSelector(registry, conns, "me", testGrace).Reachable(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(reach.Connected).To(ConsistOf(id))

			// And the pick still refuses it, which is what the two sets are for.
			_, _, err = NewAgentSelector(registry, conns, "me", testGrace).PickConnected(ctx)
			Expect(err).To(MatchError(ErrNoAgentWorker))
		})

		It("reports a connection read that failed as neither an answer nor a route verdict", func() {
			register("agent-a", NodeTypeAgent)
			conns.err = errors.New("the database would not answer")

			_, err := NewAgentSelector(registry, conns, "me", testGrace).Reachable(ctx)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeFalse())
			Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
			Expect(errors.Is(err, ErrNoAgentWorker)).To(BeFalse())
		})
	})

	It("reports a connection read that failed as neither an answer nor a route verdict", func() {
		register("agent-a", NodeTypeAgent)
		conns.err = errors.New("the database would not answer")

		sel := NewAgentSelector(registry, conns, "me", testGrace)
		id, nodeType, err := sel.PickConnected(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("the database would not answer"))
		// A database that would not answer says nothing about any worker.
		Expect(errors.Is(err, ErrWorkerUnroutable)).To(BeFalse())
		Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
		Expect(errors.Is(err, ErrNoAgentWorker)).To(BeFalse())
		Expect(id).To(BeEmpty())
		Expect(nodeType).To(BeEmpty())
	})
})
