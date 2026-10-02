package distributed_test

import (
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	clustersvc "github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/tests/e2e/distributed/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The whole programme, end to end, on the topology it was built for: TWO
// frontend replicas and TWO backend workers, with no message broker anywhere.
//
// Why two of each and not the one-worker shape almost every other cluster spec
// uses. With two workers spread across two replicas the OWNER path and the
// RELAY path are live AT THE SAME TIME, against one roster, one scheduler and
// one health monitor. A routing mistake then has somewhere to show up: a
// frontend that answered for the worker it owns and quietly failed for the
// other, or one that relayed everything to a single replica, is a green run on
// a one-worker cluster and a red one here. It is also the production shape:
// with N replicas behind round robin, (N-1)/N of traffic arrives at a replica
// that owns nothing about the request it is holding.
//
// Read the LAST spec in this file before trusting the first two. Frontends and
// workers share a host here, so every port a frontend names is one it could
// also have dialled directly; if it did, the positive specs would pass with the
// tunnels doing nothing at all. The last one takes BOTH tunnels away and
// requires BOTH workers to become unreachable, then puts back ONE of them and
// requires exactly one to recover. That differential is what makes the two
// positive specs mean anything.

const (
	// alphaModel and bravoModel are pinned to one worker each, by label and
	// node selector, so "which worker served this" is a fact of the
	// arrangement and not a guess about what the scheduler felt like doing.
	// Every spec here then VERIFIES the pin from node_models rather than
	// trusting it: a selector that silently failed open would otherwise leave
	// a spec claiming a worker served a request it never saw.
	alphaModel = "alpha-model"
	bravoModel = "bravo-model"

	// slotLabel is the label key the two node selectors discriminate on. A key
	// nothing else in this deployment sets, so a node matching it matched
	// because this spec labelled it.
	slotLabel = "e2e.slot"
	alphaSlot = "alpha"
	bravoSlot = "bravo"

	// nodeModelSettleTimeout bounds the wait for a loaded model to appear in
	// the node_models rows a frontend reports. The load itself has already
	// answered by the time this is polled; this covers the write that records
	// where it landed.
	nodeModelSettleTimeout = "60s"
	nodeModelSettlePoll    = "500ms"

	// buslessOwnershipTimeout bounds the wait for both workers' tunnels to be
	// claimed. Two workers dial two balancers at once, so it is the same wait
	// the one-worker specs make, with room for the second claim.
	buslessOwnershipTimeout = 90 * time.Second
	buslessOwnershipPoll    = 500 * time.Millisecond

	// deadInstanceTimeout bounds leg 1 of the churn spec: how long the killed
	// replica may go on reading as LIVE in the instances table.
	//
	// It has to outlast cluster.InstanceLiveness (30s), because the ownership
	// query joins against instances the DATABASE still considers live. Measured
	// at 27.0s on the box this was written on, so the bound is a shade over
	// twice that rather than a shade over the constant: a run that lands one
	// sweep interval late is a slow runner, not a regression, and the spec has
	// room for it because leg 2 checks that the two legs together still fit
	// inside the grace.
	//
	// Nothing is asserted before this completes: before it, the scenario is not
	// yet about absence at all, and a Consistently placed inside the liveness
	// window is the vacuous spec this file exists not to repeat.
	deadInstanceTimeout = "60s"
	deadInstancePoll    = "1s"

	// absenceProbeTimeout caps ONE leg 2 poll.
	//
	// The shared session's budget is three minutes, sized for a cold model load
	// across a tunnel, and a single poll allowed to spend it would consume the
	// whole grace and then report the model row as evicted when what actually
	// happened is that one read never came back. Capping the poll turns that
	// into a legible failure in seconds. Observed once, on a host-wide stall in
	// which a LOOPBACK dial timed out.
	absenceProbeTimeout = 20 * time.Second

	// absenceHoldWindow is leg 2: how long the fleet must survive with the
	// worker's tunnel genuinely unowned and its departure inside the reconnect
	// grace.
	//
	// It starts only AFTER leg 1 has observed the owner leave the live set, so
	// every second of it is spent in the branch the assertion is about. It is
	// well inside config.DefaultWorkerReconnectGrace (90s) and past the health
	// monitor's 15s tick, so at least one sweep runs against a worker nothing
	// can reach and finds nothing to reap.
	//
	// ONE window covering both facts, not one per fact. Two 20s windows back to
	// back put leg 1 plus leg 2 at 40s plus whatever leg 1 cost, which on a slow
	// runner reaches the 90s grace; the grace would then expire legitimately
	// mid-assertion and the spec would report an eviction as though it were the
	// defect. One window keeps the pair inside the grace with room to spare, and
	// absenceViolations below is what lets a single window still name WHICH of
	// the two facts broke.
	absenceHoldWindow = "20s"
	absenceHoldPoll   = "1s"

	// buslessRefusalTimeout bounds a request to a worker that holds no tunnel.
	// Resolving the route fails on a table read, so a request still in flight
	// after this is not slow: it is parked on something that will never happen.
	buslessRefusalTimeout = 60 * time.Second
)

// putJSON performs an authenticated PUT and reports the status.
//
// The harness carries GetJSON and PostJSON; node labels are a PUT and there is
// no reason to widen the harness for one verb one file uses.
func putJSON(c *cluster.Cluster, client *http.Client, frontend int, path string, body any) (int, string, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequest(http.MethodPut, c.FrontendURL(frontend)+path, bytes.NewReader(encoded))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String(), nil
}

// pinModelToNode labels a node and gives a model a selector that only that
// label matches, so the model can land on that worker and on no other.
//
// Both halves go through the ADMIN API rather than through the harness or a
// direct database write, which is deliberate: the pin is then the same
// arrangement an operator makes, and a regression in either endpoint fails
// here rather than being routed around.
func pinModelToNode(c *cluster.Cluster, client *http.Client, frontend int, model, nodeID, slot string) {
	GinkgoHelper()
	status, body, err := putJSON(c, client, frontend, "/api/nodes/"+nodeID+"/labels",
		map[string]string{slotLabel: slot})
	Expect(err).ToNot(HaveOccurred())
	Expect(status).To(Equal(http.StatusOK), "labelling node %s failed: %s", nodeID, body)

	status, err = c.PostJSON(client, frontend, "/api/nodes/scheduling", map[string]any{
		"model_name":    model,
		"node_selector": map[string]string{slotLabel: slot},
	}, nil)
	Expect(err).ToNot(HaveOccurred())
	Expect(status).To(Equal(http.StatusOK), "pinning %s to node %s failed", model, nodeID)
}

// servedBy asserts that model is loaded on wantNode and on no other node in
// nodes, reading the frontend's own node_models rows.
//
// This is how every spec in this file answers "which worker served that
// request" instead of assuming it. The exclusion half is the load-bearing one:
// "the model is on worker 0" is also true of a deployment that loaded it
// everywhere, which is precisely what a selector that failed open would do.
func servedBy(c *cluster.Cluster, client *http.Client, frontend int, model, wantNode string, otherNodes ...string) {
	GinkgoHelper()
	Eventually(func() ([]string, error) {
		return nodeModelNames(c, client, frontend, wantNode)
	}, nodeModelSettleTimeout, nodeModelSettlePoll).Should(ContainElement(model),
		"frontend %d does not record %s as loaded on node %s, so nothing here says which worker served the request",
		frontend, model, wantNode)

	for _, other := range otherNodes {
		loaded, err := nodeModelNames(c, client, frontend, other)
		Expect(err).ToNot(HaveOccurred())
		Expect(loaded).ToNot(ContainElement(model),
			"node %s also holds %s, so the selector did not pin it and this spec cannot say which worker answered", other, model)
	}
}

// absenceViolations lists everything a deployment did in reaction to a worker
// whose tunnel is gone but whose departure is still inside the grace. An empty
// result is the invariant holding.
//
// It returns a LIST rather than a boolean so one Consistently window can cover
// both facts and still name which one broke. The alternative, one window per
// fact, doubles the time leg 2 spends and pushes the pair past the grace on a
// slow runner, at which point the spec reports a legitimate expiry as an early
// reap.
//
// A read that fails is a violation too, and deliberately so: this window is the
// only place the spec can observe the branch, and a poll that could not answer
// is not evidence that nothing happened.
func absenceViolations(c *cluster.Cluster, client *http.Client, probe *rosterProbe, frontend int, nodeID string, worker int) []string {
	violations := []string{}
	names, err := nodeModelNames(c, client, frontend, nodeID)
	switch {
	case err != nil:
		violations = append(violations, "node_models unreadable: "+err.Error())
	case !slices.Contains(names, alphaModel):
		violations = append(violations, fmt.Sprintf("%s's model row was evicted (rows now %v)", c.WorkerName(worker), names))
	}
	if status := probe.statusOf(c.WorkerName(worker)); status != "healthy" {
		violations = append(violations, fmt.Sprintf("%s was demoted to %q", c.WorkerName(worker), status))
	}
	return violations
}

// busSymbolsIn reports every module path in the binary's embedded build info
// that belongs to the message broker client, plus how many modules were read.
//
// The count is returned rather than discarded because it is the control: a
// binary whose build info could not be read, or one stripped of it, yields no
// module paths at all and would satisfy "no broker module" while proving
// nothing. Reading the ARTIFACT rather than a process environment is what
// turns "this cluster ran with no bus" from a statement about what the harness
// set into a property of the thing the harness executed.
func busSymbolsIn(path string) ([]string, int, error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("reading build info from %s: %w", path, err)
	}
	found := []string{}
	for _, dep := range info.Deps {
		if strings.HasPrefix(dep.Path, "github.com/nats-io/") {
			found = append(found, dep.Path+"@"+dep.Version)
		}
	}
	return found, len(info.Deps), nil
}

// rotatedFrom returns urls starting at index start, wrapping around.
func rotatedFrom(urls []string, start int) []string {
	if len(urls) == 0 {
		return nil
	}
	out := make([]string, 0, len(urls))
	for i := 0; i < len(urls); i++ {
		out = append(out, urls[(start+i)%len(urls)])
	}
	return out
}

// withSpreadBalancers gives every worker its OWN balancer, preferring frontend
// i%Frontends and falling through to the rest.
//
// It replaces two things that cannot be combined. Options.SpreadWorkerRegistrations
// spreads workers across replicas but pins each one at a replica, so a worker
// whose replica dies has nowhere to reconnect to and the re-home this feature
// is built on cannot happen. withBalancer gives every worker somewhere to go
// but hands them ONE balancer that forwards to the first live target, so both
// workers land on the same replica and the relay path is never live at the same
// time as the owner path. One balancer per worker gives both: a spread that
// really is a spread, and a worker that survives its replica.
//
// Blocking is per worker too, which is what lets the negative control lift the
// block for ONE worker and require exactly one recovery.
func withSpreadBalancers(into *[]*frontendBalancer, arm ...func(int, *frontendBalancer)) func(*cluster.Options) {
	return func(o *cluster.Options) {
		o.WorkerFrontendURL = func(worker int, _ string, frontends []string) string {
			for len(*into) <= worker {
				*into = append(*into, nil)
			}
			if (*into)[worker] == nil {
				b := newFrontendBalancer(rotatedFrom(frontends, worker%len(frontends))...)
				for _, apply := range arm {
					apply(worker, b)
				}
				(*into)[worker] = b
			}
			return (*into)[worker].URL()
		}
	}
}

// twoByTwo starts the topology this file is about: two frontends, two backend
// workers, one balancer each, both mock models seeded.
func twoByTwo(into *[]*frontendBalancer, arm ...func(int, *frontendBalancer)) (*cluster.Cluster, string) {
	GinkgoHelper()
	return startClusterOnFreshDB(2, 2,
		withMockModel(alphaModel),
		withMockModel(bravoModel),
		withSpreadBalancers(into, arm...))
}

// busless is the state every spec in this file establishes first: both workers
// registered and healthy at both replicas, their node ids, and which replica
// owns each tunnel.
type busless struct {
	c        *cluster.Cluster
	client   *http.Client
	probe    *rosterProbe
	owners   *tunnelOwners
	nodeIDs  [2]string
	ownerIdx [2]int
}

// awaitFleet waits for both workers to be healthy and reads their node ids. It
// does NOT read ownership: the negative control needs the fleet without it,
// because there is no owner to read.
func awaitFleet(c *cluster.Cluster, dsn string) *busless {
	GinkgoHelper()
	// The topology, before anything is read out of it. Every index-taking
	// method on Cluster indexes blindly, so a cluster started with one worker
	// panics somewhere further down with a runtime message that names a slice
	// rather than the arrangement this whole file rests on. WorkerRegistrar is
	// the one indexed method that reports an out-of-range index as an error.
	_, err := c.WorkerRegistrar(1)
	Expect(err).ToNot(HaveOccurred(),
		"this file is about TWO workers on TWO replicas; with fewer, the owner path and the relay path cannot both be live and nothing below means what it says")

	client := inferenceClient(c)
	probe := newRosterProbe(c, client, 0)
	Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
		Should(And(ContainElement(c.WorkerName(0)), ContainElement(c.WorkerName(1))), probe.describe)

	b := &busless{c: c, client: client, probe: probe, owners: newTunnelOwners(openClusterDB(dsn))}
	for i := 0; i < 2; i++ {
		b.nodeIDs[i] = probe.idOf(c.WorkerName(i))
		Expect(b.nodeIDs[i]).ToNot(BeEmpty(), "the roster reported %s without a registration id", c.WorkerName(i))

		// Neither worker publishes an endpoint of any kind. Both halves are
		// needed: an empty value alone is also what a spec sees after the keys
		// are renamed out of the payload, which would leave this reporting
		// "advertises nothing" about a node whose advertisement it can no
		// longer read.
		advertised, carriedKeys := probe.advertisementOf(c.WorkerName(i))
		Expect(carriedKeys).To(BeTrue(),
			"the roster payload no longer carries the address keys, so this spec cannot tell a worker that advertises nothing from one it cannot read")
		Expect(advertised).To(BeEmpty(),
			"%s advertised %q, so no spec here can tell a tunnelled request from a direct dial", c.WorkerName(i), advertised)
	}
	return b
}

// awaitOwners reads which replica holds each worker's tunnel and requires the
// two to differ.
//
// The difference is the precondition of this whole file, asserted rather than
// arranged for: if both tunnels landed on one replica, every request below
// would take the owner path or the relay path but never both at once, and the
// spread this topology exists to create would be a spread in name only.
func (b *busless) awaitOwners() {
	GinkgoHelper()
	for i := 0; i < 2; i++ {
		idx := i
		Eventually(func() int {
			b.ownerIdx[idx] = b.owners.ownerIndexOf(b.c, 2, b.nodeIDs[idx])
			return b.ownerIdx[idx]
		}, buslessOwnershipTimeout, buslessOwnershipPoll).Should(BeNumerically(">=", 0), b.owners.describe)
	}
	Expect(b.ownerIdx[0]).ToNot(Equal(b.ownerIdx[1]),
		"both tunnels landed on frontend %d, so the owner path and the relay path are not live at the same time and this topology proves nothing the one-worker specs do not",
		b.ownerIdx[0])
}

// pinBoth labels each node and pins one model to each, through frontend 0.
//
// Through frontend 0 for BOTH, deliberately: the requests below are made at
// both replicas, so a scheduling rule written at one replica and not visible at
// the other fails here. That rule reaches the other replica over the same
// PostgreSQL fan-out every other broadcast in this deployment travels on, and
// nothing else carries it.
func (b *busless) pinBoth() {
	GinkgoHelper()
	pinModelToNode(b.c, b.client, 0, alphaModel, b.nodeIDs[0], alphaSlot)
	pinModelToNode(b.c, b.client, 0, bravoModel, b.nodeIDs[1], bravoSlot)
}

var _ = Describe("Two frontends and two workers, with no bus", Label("Distributed"), Label("Cluster"), func() {

	// Scenario 1. The headline, on the topology that matters.
	//
	// A WRONG IMPLEMENTATION does one of three things and each is caught by a
	// different assertion here: it still needs a broker, and either refuses to
	// boot or leaves both workers inert; it reaches workers by dialling an
	// address they advertised, which no worker publishes any more; or it serves
	// only the worker whose tunnel the replica taking the request happens to
	// hold, which is the (N-1)/N failure that a one-worker cluster cannot see.
	//
	// ATTACKS THAT REDDEN IT: restoring the "requires --nats-url" validation
	// reddens it at cluster.Start; never starting a worker reddens it at the
	// roster wait; blocking either tunnel reddens the request that worker
	// serves, which is what the last spec in this file does deliberately.
	It("serves both workers from both replicas, over the owner path and the relay path at once", func() {
		var balancers []*frontendBalancer
		c, dsn := twoByTwo(&balancers)
		fleet := awaitFleet(c, dsn)

		// THE ARTIFACT, not the environment. A spec asserting that no process
		// was handed a broker URL is asserting about the harness; this asserts
		// about the binary the harness executed, which cannot dial a broker
		// whatever anyone hands it.
		busModules, depCount, err := busSymbolsIn(localAIBinary())
		Expect(err).ToNot(HaveOccurred())
		Expect(depCount).To(BeNumerically(">", 0),
			"the binary carries no module list at all, so finding no broker module in it proves nothing")
		Expect(busModules).To(BeEmpty(),
			"the binary links %v, so this cluster is not running without a bus by construction", busModules)

		// And neither worker was handed one either, read from the LIVE process
		// rather than from what the harness meant to set. LOCALAI_REGISTER_TO
		// is asserted alongside so an absent broker variable is a fact about
		// the worker rather than a read that returned nothing.
		for i := 0; i < 2; i++ {
			environ, err := c.ProcessEnviron(cluster.ProcWorker, i)
			Expect(err).ToNot(HaveOccurred())
			Expect(environ).To(ContainElement(HavePrefix("LOCALAI_REGISTER_TO=")),
				"worker %d's environment could not be read, so the absence below proves nothing", i)
			for _, entry := range environ {
				for _, banned := range []string{"LOCALAI_NATS_URL=", "LOCALAI_NATS_JWT=", "LOCALAI_NATS_USER_SEED="} {
					Expect(entry).ToNot(HavePrefix(banned),
						"worker %d was handed %s, so this spec is not about a worker that has none", i, banned)
				}
			}
		}
		// The frontends ARE still handed a (dead) broker URL, which is the
		// control on the reads above: a harness that had simply stopped setting
		// the variable anywhere would satisfy them just as well. Nothing dials
		// it, and after the module assertion above nothing could.
		for i := 0; i < 2; i++ {
			frontendEnviron, err := c.ProcessEnviron(cluster.ProcFrontend, i)
			Expect(err).ToNot(HaveOccurred())
			Expect(frontendEnviron).To(ContainElement(HavePrefix("LOCALAI_NATS_URL=")),
				"the harness stopped handing a broker URL to anything, so the workers' lack of one proves nothing")
		}

		// One roster, read at both replicas, by identity and not by name.
		at1 := newRosterProbe(c, fleet.client, 1)
		Eventually(at1.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(And(ContainElement(c.WorkerName(0)), ContainElement(c.WorkerName(1))), at1.describe)
		for i := 0; i < 2; i++ {
			Expect(at1.idOf(c.WorkerName(i))).To(Equal(fleet.nodeIDs[i]),
				"frontend 1 resolved a different row for %s, so the replicas are not reading one roster", c.WorkerName(i))
		}

		fleet.awaitOwners()
		fleet.pinBoth()
		owner0, owner1 := fleet.ownerIdx[0], fleet.ownerIdx[1]
		AddReportEntry("2x2 tunnel ownership",
			fmt.Sprintf("%s owned by frontend %d, %s owned by frontend %d",
				c.WorkerName(0), owner0, c.WorkerName(1), owner1))

		// THE OWNER PATH. The request goes to the replica the database says
		// holds worker 0's tunnel, so it is served without a relay.
		expectMockedInference(fleet.client, c.FrontendURL(owner0), alphaModel,
			fmt.Sprintf("frontend %d owns %s's tunnel and must serve it directly", owner0, c.WorkerName(0)))
		servedBy(c, fleet.client, owner0, alphaModel, fleet.nodeIDs[0], fleet.nodeIDs[1])

		// THE RELAY PATH, at the same instant, on the same cluster, through the
		// same replica. owner0 does not hold worker 1's tunnel, so this request
		// can only be answered by relaying it to owner1.
		//
		// It is the FIRST request for this model, so the backend install, the
		// model file staging over the http tag and the gRPC load and predict
		// all cross the relay. Warming it at its owner first would leave only
		// the predict on the relayed path.
		Expect(fleet.owners.ownerIndexOf(c, 2, fleet.nodeIDs[1])).ToNot(Equal(owner0),
			"frontend %d owns %s's tunnel after all, so the request below would not be relayed and would prove nothing",
			owner0, c.WorkerName(1))
		expectMockedInference(fleet.client, c.FrontendURL(owner0), bravoModel,
			fmt.Sprintf("frontend %d must relay to frontend %d, which owns %s's tunnel",
				owner0, owner1, c.WorkerName(1)))

		// THIS is what makes the request above a relayed one, and it is the
		// assertion rather than the pre-read. Ownership can move between the
		// pre-read and the reply, and if it had moved TO owner0 the request
		// would have been served directly and still come back 200. That leaves
		// the owner CHANGED, and this read reddens on it. The only window left
		// is a move away and back inside one request, which takes two claims,
		// and no replica dies in this scenario to prompt either.
		Expect(fleet.owners.ownerIndexOf(c, 2, fleet.nodeIDs[1])).To(Equal(owner1),
			"%s's tunnel is no longer held by frontend %d, so the request to frontend %d was not necessarily relayed",
			c.WorkerName(1), owner1, owner0)
		servedBy(c, fleet.client, owner0, bravoModel, fleet.nodeIDs[1], fleet.nodeIDs[0])

		// And the mirror image, so neither replica is the one that happens to
		// work. owner1 serves its own worker directly and relays for the other.
		expectMockedInference(fleet.client, c.FrontendURL(owner1), bravoModel,
			fmt.Sprintf("frontend %d owns %s's tunnel and must serve it directly", owner1, c.WorkerName(1)))
		expectMockedInference(fleet.client, c.FrontendURL(owner1), alphaModel,
			fmt.Sprintf("frontend %d must relay to frontend %d, which owns %s's tunnel",
				owner1, owner0, c.WorkerName(0)))
		Expect(fleet.owners.ownerIndexOf(c, 2, fleet.nodeIDs[0])).To(Equal(owner0),
			"%s's tunnel moved during the relayed request, so it was not necessarily relayed", c.WorkerName(0))

		// The control plane crosses the same two paths. Each worker's backend
		// listing is the WORKER's own answer to a control verb, relayed through
		// the owner when the replica asked is not it, so a listing that comes
		// back at all came back over a tunnel. The last spec in this file is
		// what says so: with the tunnel gone, this same call fails naming the
		// missing route.
		for worker := 0; worker < 2; worker++ {
			for frontend := 0; frontend < 2; frontend++ {
				_, err := nodeBackendNames(c, fleet.client, frontend, fleet.nodeIDs[worker])
				Expect(err).ToNot(HaveOccurred(),
					"frontend %d could not list %s's backends (that worker's tunnel is on frontend %d)",
					frontend, c.WorkerName(worker), fleet.ownerIdx[worker])
			}
		}
	})

	// Scenario 2. One replica dies. Its worker re-homes; the other worker never
	// notices; and in between, absence is a fact NOBODY may act on.
	//
	// A WRONG IMPLEMENTATION reaps the fleet in the seconds before the re-home,
	// which is what the last five master commits before this branch were
	// fixing, or it takes the surviving worker down with the replica that died,
	// or it leaves the dead replica's connection row in place so the survivor
	// relays into a corpse.
	//
	// WHY THE LEGS ARE ORDERED THE WAY THEY ARE. An earlier scenario in this
	// programme passed vacuously, with the reconnect grace set to a NANOSECOND,
	// because its window sat entirely inside cluster.InstanceLiveness (30s):
	// the killed replica still read as a LIVE owner throughout, so the absence
	// branch was never entered. Leg 1 here asserts NOTHING and only waits for
	// the killed instance to leave the live set. Only then does leg 2 begin,
	// and every second of leg 2 is spent in the branch it is about.
	//
	// ATTACK THAT REDDENS IT: Options.ReconnectGrace set to one nanosecond must
	// redden LEG 2. A green leg 2 under that attack means the window is back
	// inside the liveness period and the scenario asserts nothing.
	It("re-homes only the dead replica's worker, keeps the other serving, and reaps nothing inside the grace", func() {
		var balancers []*frontendBalancer
		c, dsn := twoByTwo(&balancers)
		fleet := awaitFleet(c, dsn)
		fleet.awaitOwners()
		fleet.pinBoth()

		doomed, survivor := fleet.ownerIdx[0], fleet.ownerIdx[1]
		Expect(doomed).ToNot(Equal(survivor))

		// Both workers serve before anything is killed, or the recovery below
		// proves nothing.
		expectMockedInference(fleet.client, c.FrontendURL(doomed), alphaModel,
			"alpha must serve before its owner is killed")
		servedBy(c, fleet.client, doomed, alphaModel, fleet.nodeIDs[0], fleet.nodeIDs[1])
		expectMockedInference(fleet.client, c.FrontendURL(survivor), bravoModel,
			"bravo must serve before the other replica is killed")
		servedBy(c, fleet.client, survivor, bravoModel, fleet.nodeIDs[1], fleet.nodeIDs[0])

		// Take worker 0's tunnel away and keep it away, then kill the replica
		// holding the live session. Without the block the worker re-homes onto
		// the survivor within its capped 30s backoff, which is FASTER than leg
		// 1 takes: there would then be a live owner throughout leg 2, the
		// absence branch would never be entered, and leg 2 would be the vacuous
		// window this scenario exists not to repeat. Registration and
		// heartbeats keep flowing through the same balancer, so the worker
		// stays registered, approved and heartbeating with no route at all,
		// which is exactly the condition the invariant is about.
		//
		// Only worker 0's balancer is touched. Worker 1's tunnel is on the
		// survivor and nothing in this paragraph reaches it.
		balancers[0].blockTunnel.Store(true)
		Expect(c.KillFrontend(doomed)).To(Succeed())
		Eventually(func() bool { return c.FrontendAlive(doomed) }, frontendExitTimeout, frontendExitPoll).
			Should(BeFalse(), "frontend %d did not die, so nothing below is a failover assertion", doomed)

		atSurvivor := newRosterProbe(c, fleet.client, survivor)
		roster := newInstanceRoster(openClusterDB(dsn))

		// LEG 1. No assertion about the fleet, only a wait for the killed
		// replica to stop reading as live. Timed, and the number is reported,
		// because "it passed" is exactly what the vacuous version also said.
		leg1Started := time.Now()
		Eventually(roster.addresses, deadInstanceTimeout, deadInstancePoll).
			ShouldNot(ContainElement(hostPortOf(c.FrontendURL(doomed))), roster.describe)
		leg1 := time.Since(leg1Started)

		// The ownership query joins against live instances, so with the killed
		// replica out of the live set there is now no owner at all. This is the
		// state leg 2 asserts over, and it is READ rather than inferred.
		Expect(fleet.owners.ownerOf(fleet.nodeIDs[0])).To(BeEmpty(),
			"a live replica still holds %s's tunnel, so leg 2 would be watching a worker that has a route", c.WorkerName(0))

		// LEG 2. The worker is heartbeating, its tunnel is genuinely gone, and
		// the departure is inside config.DefaultWorkerReconnectGrace. Nothing
		// may act on that: not the health monitor, not the reaper, not the
		// model evictor. The node_models row is the sharper of the two, because
		// evicting it is the step that makes a later request fail for a reason
		// nobody can trace.
		Expect(config.DefaultWorkerReconnectGrace).To(BeNumerically(">", 60*time.Second),
			"the reconnect grace is shorter than this scenario assumes, so leg 2's window may no longer sit inside it")
		fleet.client.Timeout = absenceProbeTimeout
		leg2Started := time.Now()
		Consistently(func() []string {
			return absenceViolations(c, fleet.client, atSurvivor, survivor, fleet.nodeIDs[0], 0)
		}, absenceHoldWindow, absenceHoldPoll).Should(BeEmpty(),
			"something acted on %s's absence while its tunnel was gone INSIDE the grace, which is a routing fact being read as a departure",
			c.WorkerName(0))
		leg2 := time.Since(leg2Started)
		fleet.client.Timeout = tunnelInferenceTimeout

		// The two legs together have to have fitted inside the grace, or leg 2
		// spent part of its window in the branch where acting on the departure
		// is CORRECT. Without this, a slow runner turns a legitimate expiry
		// into a report that the fleet was reaped early.
		Expect(leg1+leg2).To(BeNumerically("<", config.DefaultWorkerReconnectGrace),
			"leg 1 took %s and leg 2 took %s, which is past the %s grace, so leg 2 was not entirely inside the window it claims to assert over",
			leg1, leg2, config.DefaultWorkerReconnectGrace)

		// The OTHER worker never noticed, which is the half a one-worker
		// cluster cannot state at all. Its status held through the same window
		// and it still serves, through the survivor, on its own tunnel.
		Expect(atSurvivor.statusOf(c.WorkerName(1))).To(Equal("healthy"),
			"%s was demoted because a replica it does not depend on died: %s", c.WorkerName(1), atSurvivor.describe())
		Expect(fleet.owners.ownerIndexOf(c, 2, fleet.nodeIDs[1])).To(Equal(survivor),
			"%s's tunnel moved when the other replica died, so it did depend on it after all", c.WorkerName(1))
		expectMockedInference(fleet.client, c.FrontendURL(survivor), bravoModel,
			fmt.Sprintf("%s must keep serving while %s has no route at all", c.WorkerName(1), c.WorkerName(0)))
		servedBy(c, fleet.client, survivor, bravoModel, fleet.nodeIDs[1], fleet.nodeIDs[0])

		// LEG 3. Give worker 0 its route back and require it to land on the
		// survivor and serve again. The re-home is the assertion, not the
		// inference: a frontend that answered without the tunnel moving would
		// satisfy an inference-only leg while the worker stayed stranded.
		balancers[0].blockTunnel.Store(false)
		leg3Started := time.Now()
		Eventually(func() int { return fleet.owners.ownerIndexOf(c, 2, fleet.nodeIDs[0]) },
			buslessOwnershipTimeout, buslessOwnershipPoll).
			Should(Equal(survivor), fleet.owners.describe)
		leg3 := time.Since(leg3Started)

		Eventually(atSurvivor.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), atSurvivor.describe)
		Expect(atSurvivor.idOf(c.WorkerName(0))).To(Equal(fleet.nodeIDs[0]),
			"%s re-registered rather than re-homing, so this says nothing about a tunnel moving", c.WorkerName(0))

		eventuallyMockedInference(fleet.client, c.FrontendURL(survivor), alphaModel,
			"the survivor must serve the re-homed worker")
		servedBy(c, fleet.client, survivor, alphaModel, fleet.nodeIDs[0], fleet.nodeIDs[1])

		AddReportEntry("2x2 replica churn, observed windows", fmt.Sprintf(
			"killed frontend %d (owner of %s), survivor frontend %d (owner of %s)\n"+
				"  leg 1, killed instance leaves the live set: %s (bound %s, cluster.InstanceLiveness is %s)\n"+
				"  leg 2, fleet survives with no owner inside the grace: %s (window %s, grace %s)\n"+
				"  leg 3, worker re-homes onto the survivor: %s",
			doomed, c.WorkerName(0), survivor, c.WorkerName(1),
			leg1.Round(time.Millisecond), deadInstanceTimeout, clustersvc.InstanceLiveness,
			leg2.Round(time.Millisecond), absenceHoldWindow, config.DefaultWorkerReconnectGrace,
			leg3.Round(time.Millisecond)))
	})

	// Scenario 3. THE NEGATIVE CONTROL FOR THIS WHOLE FILE, and the only spec
	// here that can tell the two above from a pair of specs that pass with the
	// tunnels doing nothing.
	//
	// Frontends and workers share a host, so every backend port a frontend
	// names in a stream target is a port it could have dialled itself. This
	// spec refuses BOTH tunnel dials at the balancers while proxying
	// registration and heartbeats, so both workers are registered, approved,
	// healthy and holding no tunnel, and then requires both to be unreachable.
	//
	// It cannot use LOCALAI_WORKER_TUNNEL=false: that is a fatal startup error
	// now, and a worker that never started says nothing about a worker
	// reachable by some other path.
	//
	// THE FAILURE MUST BE THE ROUTING FACT AND NOT A CLAIM OF ABSENCE. Four
	// conditions have to stay apart and no code may report any as another: a
	// routing fact, which the scheduler may act on; an absent connection inside
	// the grace, which nobody may act on; an unreachable peer, which nobody may
	// act on; and the worker's own answer. So the refusal is required to name
	// the missing ROUTE, and the roster is required to go on reporting both
	// workers healthy with fresh heartbeats while it does.
	//
	// ATTACK: lift the block for ONE worker. Exactly one set of legs must go
	// green and the other must stay red, which is what proves these are
	// separate assertions about separate tunnels rather than one assertion
	// about the cluster.
	It("cannot reach either worker with both tunnels refused, recovers exactly the one whose tunnel returns", func() {
		var balancers []*frontendBalancer
		c, dsn := twoByTwo(&balancers, func(_ int, b *frontendBalancer) { b.blockTunnel.Store(true) })
		fleet := awaitFleet(c, dsn)
		fleet.pinBoth()

		models := [2]string{alphaModel, bravoModel}

		// In every respect the workers the specs above used, except the
		// tunnels, and both halves of that are asserted rather than assumed:
		// each dialled, and no replica holds either.
		for i := 0; i < 2; i++ {
			idx := i
			Eventually(balancers[idx].tunnelDials.Load, "60s", "500ms").Should(BeNumerically(">", 0),
				"%s never dialled its tunnel, so blocking the dial is not what makes it unreachable below", c.WorkerName(idx))
			Consistently(func() string { return fleet.owners.ownerOf(fleet.nodeIDs[idx]) }, "5s", "500ms").
				Should(BeEmpty(), "a replica holds %s's tunnel, so the blocker is not blocking", c.WorkerName(idx))
		}

		fleet.client.Timeout = buslessRefusalTimeout
		refuseAt := func(frontend, worker int) {
			GinkgoHelper()
			refused, err := chat(fleet.client, c.FrontendURL(frontend), models[worker], "ping")
			Expect(err).ToNot(HaveOccurred(),
				"the request never came back; a worker with no route must be REFUSED, not left parked")
			Expect(refused.status).ToNot(Equal(http.StatusOK),
				"frontend %d served %s for a worker that holds no tunnel, so something other than the tunnel reaches %s: %s",
				frontend, models[worker], c.WorkerName(worker), refused.body)
			// One substring, not a disjunction. "no route" is what
			// cluster.ErrNoRoute reads as and nothing else on this path
			// produces it. It is in particular NOT what an absent worker
			// produces, and that distinction is the whole invariant: a worker
			// this replica cannot reach must never be reported as one that has
			// gone.
			Expect(refused.body).To(ContainSubstring("no route"),
				"the refusal does not name the missing route, so this cannot tell a worker with no tunnel from a request that failed for one of the ordinary reasons: %s",
				refused.body)
		}

		// Both workers, asked at BOTH replicas: with no tunnel anywhere,
		// neither the owner path nor the relay path exists, and a frontend that
		// had some other way in would show up as exactly one of these four
		// succeeding.
		for worker := 0; worker < 2; worker++ {
			for frontend := 0; frontend < 2; frontend++ {
				refuseAt(frontend, worker)
			}
			_, listErr := nodeBackendNames(c, fleet.client, 0, fleet.nodeIDs[worker])
			Expect(listErr).To(HaveOccurred(),
				"a frontend answered a backend listing for %s, which holds no tunnel, so something other than the tunnel reaches it",
				c.WorkerName(worker))
		}

		// AND NOTHING WAS REAPED FOR ANY OF IT. Both workers kept
		// heartbeating, so a request that could not be routed must not have
		// cost either its row, its health or its heartbeat. This is the leg
		// that separates "unreachable" from "absent"; without it the refusals
		// above would be satisfied by a deployment that had simply declared
		// both workers gone.
		Consistently(fleet.probe.healthyNames, "15s", "1s").
			Should(And(ContainElement(c.WorkerName(0)), ContainElement(c.WorkerName(1))),
				fleet.probe.explain("a worker was demoted or removed because requests to it could not be routed"))
		for i := 0; i < 2; i++ {
			Expect(fleet.probe.heartbeatOf(c.WorkerName(i))).To(BeTemporally(">", time.Now().Add(-1*time.Minute)),
				"%s's heartbeat is stale, so the refusals above are about a worker that went away rather than one with no route", c.WorkerName(i))
		}

		// THE DIFFERENTIAL. Put back ONE tunnel and change nothing else.
		balancers[0].blockTunnel.Store(false)
		Eventually(func() int { return fleet.owners.ownerIndexOf(c, 2, fleet.nodeIDs[0]) },
			buslessOwnershipTimeout, buslessOwnershipPoll).
			Should(BeNumerically(">=", 0), fleet.owners.describe)

		fleet.client.Timeout = tunnelInferenceTimeout
		eventuallyMockedInference(fleet.client, c.FrontendURL(0), alphaModel,
			"the only thing that changed is one tunnel, so the refusal above was that missing tunnel and nothing else")
		servedBy(c, fleet.client, 0, alphaModel, fleet.nodeIDs[0], fleet.nodeIDs[1])

		// And the other worker is STILL unreachable, for the same routing
		// reason, at both replicas. This is what makes the four legs above four
		// assertions about two tunnels rather than one assertion about the
		// cluster: a recovery that had come from anything other than worker 0's
		// own tunnel would have carried worker 1 with it.
		fleet.client.Timeout = buslessRefusalTimeout
		Expect(fleet.owners.ownerOf(fleet.nodeIDs[1])).To(BeEmpty(),
			"%s acquired a tunnel, so the refusals below would not be about a worker that has none", c.WorkerName(1))
		refuseAt(0, 1)
		refuseAt(1, 1)

		// Then the second one, so the spec ends with the whole fleet proven
		// reachable and the refusals proven reversible in both directions.
		balancers[1].blockTunnel.Store(false)
		Eventually(func() int { return fleet.owners.ownerIndexOf(c, 2, fleet.nodeIDs[1]) },
			buslessOwnershipTimeout, buslessOwnershipPoll).
			Should(BeNumerically(">=", 0), fleet.owners.describe)

		fleet.client.Timeout = tunnelInferenceTimeout
		eventuallyMockedInference(fleet.client, c.FrontendURL(1), bravoModel,
			"the second tunnel came back and its worker must serve again")
		servedBy(c, fleet.client, 1, bravoModel, fleet.nodeIDs[1], fleet.nodeIDs[0])
	})
})
