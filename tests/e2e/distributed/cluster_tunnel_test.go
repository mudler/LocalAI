package distributed_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	clustersvc "github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/tests/e2e/distributed/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// End-to-end proof that a worker with no inbound port is reachable, and only
// through its tunnel.
//
// Every other spec for this feature drives the tunnel, the relay and the
// ownership fence in isolation. These run the real binaries: a frontend replica
// per process, a worker that binds nothing routable, and a real inference over
// the result.
//
// Read the fourth scenario before trusting the first three. Frontend and worker
// are the same host here, so the backend port the frontend names in a stream
// target is a port the frontend could also have dialled directly; if it did,
// the first three would pass with the tunnel doing nothing at all. The fourth
// is what rules that out, and it is why the others mean anything.
//
// A fifth spec, in its own container below, measures what one session does when
// a large message and ordinary inference share it.

const (
	// tunnelInferenceTimeout bounds one chat completion that has to install a
	// backend on a worker, stage the model file over the tunnel and load it.
	// Generous because the first request to a model pays for all of that.
	tunnelInferenceTimeout = 3 * time.Minute

	// tunnelOwnershipTimeout bounds the wait for a worker's tunnel to be
	// claimed, or re-claimed after its owner died. A re-claim waits for the
	// worker's own reconnect backoff, which is capped at tunnelBackoffMax
	// (30s), plus the dial and the claim.
	tunnelOwnershipTimeout = 90 * time.Second
	tunnelOwnershipPoll    = 500 * time.Millisecond

	// tunnelRefusalTimeout bounds a request to a worker that has no tunnel, and
	// is what tells a REFUSED request from a PARKED one: resolving the route
	// fails on a table read, so a request that has not come back by now is not
	// slow, it is waiting on something that will never happen.
	//
	// It says nothing about the refusal being the RIGHT one. A 503 saying the
	// model is still loading would come back inside it too; what rules that out
	// is the assertion on what the body says.
	tunnelRefusalTimeout = 60 * time.Second

	// mockedReply is what the mock backend answers a prompt carrying no
	// directive. Asserted rather than merely "some content", so a frontend that
	// answered from a cache, an error template or a local backend of its own
	// cannot satisfy these specs.
	mockedReply = "This is a mocked response."

	// tunnelRestoredTimeout bounds the wait for inference to work again after a
	// route came back. It has to clear loadJobFailureGrace, which replays a
	// failed cold load's error to every caller for 15 seconds.
	tunnelRestoredTimeout = 90 * time.Second
	tunnelRestoredPoll    = 2 * time.Second
)

// mockModelYAML is a model configuration served by the mock backend. The
// artifact is a real file so the frontend's file staging has something to send
// over the tunnel, which is the http-tagged half of this feature.
func mockModelYAML(name string) string {
	return fmt.Sprintf("name: %s\nbackend: mock-backend\nparameters:\n  model: %s.bin\n", name, name)
}

// chatResult is one completion attempt: what the frontend answered and how long
// it took. Both halves are used, the status by the correctness specs and the
// duration by the head-of-line measurement.
type chatResult struct {
	status  int
	body    string
	content string
	elapsed time.Duration
}

// chat posts one non-streaming completion and reports what came back. It never
// fails the spec itself: a refusal is the expected answer in two of these
// specs, so the caller decides what the status means.
func chat(client *http.Client, baseURL, model, prompt string) (chatResult, error) {
	body, err := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	if err != nil {
		return chatResult{}, err
	}
	started := time.Now()
	resp, err := client.Post(baseURL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		return chatResult{elapsed: time.Since(started)}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return chatResult{status: resp.StatusCode, elapsed: time.Since(started)}, err
	}
	result := chatResult{status: resp.StatusCode, body: string(raw), elapsed: time.Since(started)}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &parsed) == nil && len(parsed.Choices) > 0 {
		result.content = parsed.Choices[0].Message.Content
	}
	return result, nil
}

// eventuallyMockedInference retries one completion until the worker answers.
//
// It exists for the two specs that restore a route and then assert it works
// again. A failed cold load is REPLAYED to every caller for loadJobFailureGrace
// (15s, core/services/nodes/model_load_job.go) so that a failure does not turn
// into a retry storm, which means the first request after a route comes back
// gets the stale reason rather than a fresh attempt. Retrying against the real
// condition is what a client does, and it keeps the spec off a sleep.
func eventuallyMockedInference(client *http.Client, baseURL, model, why string) {
	GinkgoHelper()
	last := ""
	Eventually(func() string {
		result, err := chat(client, baseURL, model, "ping")
		if err != nil {
			last = err.Error()
			return ""
		}
		last = fmt.Sprintf("status %d: %s", result.status, result.body)
		if result.status != http.StatusOK {
			return ""
		}
		return result.content
	}, tunnelRestoredTimeout, tunnelRestoredPoll).Should(Equal(mockedReply),
		func() string { return why + ": the last attempt answered " + last })
}

// inferenceClient is ONE admin session for the whole cluster, with a budget
// long enough for a cold model load across a tunnel.
//
// One per spec, never one per frontend. The auth routes share a limiter of five
// requests per minute per client IP and every request here comes from
// 127.0.0.1, so a helper that minted a session per frontend would spend that
// budget and start failing setup in specs that touch three replicas. The client
// is good at every replica anyway: sessions live in the shared Postgres, the
// harness pins one HMAC secret across replicas, and Go's cookie jar keys by
// host without the port. It is also safe to use from several goroutines, which
// the load measurement needs.
func inferenceClient(c *cluster.Cluster) *http.Client {
	GinkgoHelper()
	client, err := c.AdminSession(0)
	Expect(err).ToNot(HaveOccurred())
	client.Timeout = tunnelInferenceTimeout
	return client
}

// expectMockedInference runs one completion and requires the worker's own
// answer. It is the assertion every positive scenario ends on.
func expectMockedInference(client *http.Client, baseURL, model, why string) chatResult {
	GinkgoHelper()
	result, err := chat(client, baseURL, model, "ping")
	Expect(err).ToNot(HaveOccurred(), why)
	Expect(result.status).To(Equal(http.StatusOK), "%s: %s", why, result.body)
	Expect(result.content).To(Equal(mockedReply),
		"%s: the frontend answered 200 but not with the worker's reply: %s", why, result.body)
	return result
}

// tunnelOwners reads which replica holds which worker's tunnel.
//
// It reads the node_connections table through the production Owner query, which
// joins against live instances, so a row left behind by a dead replica is not
// reported as an owner. Nothing serves this over HTTP; it is replica-to-replica
// state.
type tunnelOwners struct {
	registry *clustersvc.Registry
	roster   *instanceRoster
	ctx      context.Context

	lastErr error
}

func newTunnelOwners(db *gorm.DB) *tunnelOwners {
	return &tunnelOwners{
		registry: clustersvc.NewRegistry(db),
		roster:   newInstanceRoster(db),
		ctx:      context.Background(),
	}
}

// ownerOf returns the instance ID of the live replica holding nodeID's tunnel,
// or "" when there is none. Errors are kept rather than raised so an Eventually
// can name the last one.
func (o *tunnelOwners) ownerOf(nodeID string) string {
	owner, _, err := o.registry.Owner(o.ctx, nodeID)
	if err != nil {
		o.lastErr = err
		return ""
	}
	o.lastErr = nil
	return owner
}

// ownerIndexOf is ownerOf expressed as a frontend index of c, or -1 when no
// live replica holds the tunnel.
//
// The mapping goes through the advertised address, which the harness pins to
// each replica's own loopback port, so it is exact rather than a guess. A
// spec asserting "this request went to the replica that does not own the
// worker" needs the index and not the opaque instance ID.
func (o *tunnelOwners) ownerIndexOf(c *cluster.Cluster, frontends int, nodeID string) int {
	owner := o.ownerOf(nodeID)
	if owner == "" {
		return -1
	}
	// Refreshes o.roster.lastSaw, which idAt reads.
	o.roster.addresses()
	for i := 0; i < frontends; i++ {
		if o.roster.idAt(hostPortOf(c.FrontendURL(i))) == owner {
			return i
		}
	}
	return -1
}

func (o *tunnelOwners) describe() string {
	if o.lastErr != nil {
		return fmt.Sprintf("the last read of the tunnel owner failed: %v", o.lastErr)
	}
	return fmt.Sprintf("live replicas: %s", o.roster.describe())
}

// frontendBalancer stands in for the load balancer a worker dials in
// production.
//
// It exists because LOCALAI_REGISTER_TO is BOTH the registration endpoint and
// the tunnel endpoint, and the worker resolves it once at boot and never again.
// Pointed straight at a replica, a worker whose replica dies can never come
// back, so the re-home this feature is built on cannot happen; and there is no
// other way to let a worker register normally while its tunnel dial fails,
// because LOCALAI_WORKER_TUNNEL=false is refused at startup.
//
// Two behaviours, both needed:
//
//   - It forwards to the FIRST target that accepts a connection, which is what
//     re-homes a worker onto the survivor after its replica is killed.
//   - With blockTunnel set it answers the tunnel connect path itself, with the
//     status a frontend that holds no tunnels gives, while still forwarding
//     registration and heartbeats. That is the suite's negative control.
//
// tunnelDials counts what it saw on that path, so a spec can assert the worker
// really tried and really was refused rather than assuming it.
type frontendBalancer struct {
	server      *httptest.Server
	targets     []*url.URL
	blockTunnel atomic.Bool
	tunnelDials atomic.Int64
}

// balancerProbeTimeout bounds the liveness probe the director makes per
// request. Every target is a local process, so a refused connection comes back
// at once and this only bounds the pathological case.
const balancerProbeTimeout = 2 * time.Second

// newFrontendBalancer starts a balancer in front of the given frontend URLs, in
// the order it should prefer them.
func newFrontendBalancer(urls ...string) *frontendBalancer {
	GinkgoHelper()
	b := &frontendBalancer{}
	for _, raw := range urls {
		parsed, err := url.Parse(raw)
		Expect(err).ToNot(HaveOccurred())
		b.targets = append(b.targets, parsed)
	}

	proxy := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			target := b.pick()
			r.URL.Scheme = target.Scheme
			r.URL.Host = target.Host
			r.Host = target.Host
		},
		// A dead target is the ordinary case here, not an incident: the
		// director picked it and it died between the probe and the dial.
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, fmt.Sprintf("balancer: no frontend answered: %v", err), http.StatusBadGateway)
		},
	}

	b.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, clustersvc.ConnectPath) {
			b.tunnelDials.Add(1)
			if b.blockTunnel.Load() {
				// The status a frontend not running in distributed mode gives.
				// The worker retries it with backoff and stays otherwise
				// healthy, which is precisely the state the negative control
				// needs: registered, heartbeating, and holding no tunnel.
				http.Error(w, "balancer: worker tunnels are blocked for this spec", http.StatusServiceUnavailable)
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	DeferCleanup(b.server.Close)
	return b
}

// URL is what a worker should be given as its frontend.
func (b *frontendBalancer) URL() string { return b.server.URL }

// pick returns the first target that accepts a connection, falling back to the
// first so a request during a total outage fails at the proxy with a status
// rather than panicking in the director.
func (b *frontendBalancer) pick() *url.URL {
	for _, target := range b.targets {
		conn, err := net.DialTimeout("tcp", target.Host, balancerProbeTimeout)
		if err == nil {
			_ = conn.Close()
			return target
		}
	}
	return b.targets[0]
}

var _ = Describe("Worker tunnel end to end", Label("Distributed"), Label("Cluster"), func() {
	// Scenario 1. A wrong implementation reaches the worker some other way, or
	// cannot reach it at all. The worker binds nothing routable and advertises
	// nothing, so the assertion on its empty advertisement is what says there
	// is nothing else the frontend could have been given.
	It("reaches a worker that advertises no address, through its tunnel", func() {
		c, dsn := startClusterOnFreshDB(1, 1, withMockModel("mock-model"))
		client := inferenceClient(c)

		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), probe.describe)
		nodeID := probe.idOf(c.WorkerName(0))
		Expect(nodeID).ToNot(BeEmpty())

		// The worker publishes no endpoint of any kind. Without this the
		// inference below would be satisfied by a frontend that dialled an
		// advertised address, which is the path this phase removed.
		advertised := probe.advertisementOf(c.WorkerName(0))
		Expect(advertised).To(BeEmpty(),
			"the worker advertised %q, so this spec cannot tell a tunnelled request from a direct dial", advertised)

		// And its tunnel is held HERE, so the request below is served by the
		// owner rather than relayed. The relay is the next spec's subject.
		owners := newTunnelOwners(openClusterDB(dsn))
		Eventually(func() int { return owners.ownerIndexOf(c, 1, nodeID) }, tunnelOwnershipTimeout, tunnelOwnershipPoll).
			Should(Equal(0), owners.describe)

		expectMockedInference(client, c.FrontendURL(0), "mock-model",
			"a worker with no advertised address must still serve inference over its tunnel")
	})

	// Scenario 2. With N replicas behind round robin this is (N-1)/N of
	// production traffic. A wrong implementation answers it by dialling the
	// worker from the replica that took the request, which works on one host
	// and nowhere else, or refuses it as a worker that is not connected.
	It("serves a request that landed on the replica which does not own the worker", func() {
		c, dsn := startClusterOnFreshDB(2, 1, withMockModel("relayed-model"))
		client := inferenceClient(c)

		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), probe.describe)
		nodeID := probe.idOf(c.WorkerName(0))
		Expect(nodeID).ToNot(BeEmpty())

		// Which replica owns the tunnel is READ, not assumed. The harness sends
		// the worker to frontend 0 by default, but that is a harness default
		// and a spec that assumed it would keep passing after the default
		// changed while silently testing the owner path instead.
		owners := newTunnelOwners(openClusterDB(dsn))
		var owner int
		Eventually(func() int {
			owner = owners.ownerIndexOf(c, 2, nodeID)
			return owner
		}, tunnelOwnershipTimeout, tunnelOwnershipPoll).Should(BeNumerically(">=", 0), owners.describe)

		// The one replica that is not the owner. With two frontends there is
		// exactly one, and it is derived from the reading above rather than
		// written down, so this spec exercises the relay whichever replica the
		// worker landed on.
		nonOwner := 1 - owner
		Expect(nonOwner).ToNot(Equal(owner))

		// This is the whole point of the spec, so it is asserted rather than
		// arranged: the request is about to go to a replica the database says
		// does not hold this worker's tunnel.
		Expect(owners.ownerIndexOf(c, 2, nodeID)).ToNot(Equal(nonOwner),
			"frontend %d owns the worker's tunnel, so a request to it would not be relayed and this spec would prove nothing", nonOwner)

		// The FIRST request for this model goes to the non-owner, so the
		// backend install, the model file staging over the http tag and the
		// gRPC load and predict all cross the relay. Warming the model up at
		// the owner first would leave only the predict on the relayed path.
		expectMockedInference(client, c.FrontendURL(nonOwner), "relayed-model",
			fmt.Sprintf("frontend %d must relay to frontend %d, which owns the worker's tunnel", nonOwner, owner))

		// The relay did not move ownership. A replica that answered by taking
		// the tunnel for itself would also have returned 200 above.
		Expect(owners.ownerIndexOf(c, 2, nodeID)).To(Equal(owner),
			"serving a relayed request moved the tunnel to frontend %d, which is not relaying", nonOwner)
	})

	// Scenario 3. Kills the replica holding the tunnel. The worker must land on
	// the survivor and serve again. A wrong implementation leaves the dead
	// replica's connection row in place, so the survivor relays into a corpse,
	// or lets the re-claim be fenced out by its own stale epoch.
	It("re-homes a worker onto the survivor when the replica holding its tunnel dies", func() {
		// The worker dials a balancer rather than a replica: in production
		// LOCALAI_REGISTER_TO is the load balancer, and a worker pointed at one
		// replica has nowhere to reconnect to when that replica dies.
		var balancer *frontendBalancer
		c, dsn := startClusterOnFreshDB(2, 1, withMockModel("failover-model"), withBalancer(&balancer))

		client := inferenceClient(c)
		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), probe.describe)
		nodeID := probe.idOf(c.WorkerName(0))
		Expect(nodeID).ToNot(BeEmpty())

		owners := newTunnelOwners(openClusterDB(dsn))
		var owner int
		Eventually(func() int {
			owner = owners.ownerIndexOf(c, 2, nodeID)
			return owner
		}, tunnelOwnershipTimeout, tunnelOwnershipPoll).Should(BeNumerically(">=", 0), owners.describe)

		survivor := 1 - owner
		expectMockedInference(client, c.FrontendURL(owner), "failover-model",
			"inference must work before the owner is killed, or the recovery below proves nothing")

		Expect(c.KillFrontend(owner)).To(Succeed())
		Eventually(func() bool { return c.FrontendAlive(owner) }, "20s", "500ms").Should(BeFalse())

		// The re-home is the assertion, not the inference. A frontend that
		// answered without the tunnel moving would satisfy an inference-only
		// spec while the worker stayed stranded on a dead replica.
		Eventually(func() int { return owners.ownerIndexOf(c, 2, nodeID) }, tunnelOwnershipTimeout, tunnelOwnershipPoll).
			Should(Equal(survivor), owners.describe)

		// Read at the SURVIVOR. The probe above is bound to the replica that was
		// just killed, and a roster read against a dead process reports nothing
		// rather than reporting a node that went away.
		atSurvivor := newRosterProbe(c, client, survivor)
		Eventually(atSurvivor.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), atSurvivor.describe)

		// Same worker process, not a replacement: the node ID is the identity
		// registration minted, and a worker that had restarted and re-registered
		// would be a different story with the same ending.
		Expect(atSurvivor.idOf(c.WorkerName(0))).To(Equal(nodeID),
			"the worker re-registered rather than re-homing, so this proves nothing about the tunnel moving")

		eventuallyMockedInference(client, c.FrontendURL(survivor), "failover-model",
			"the survivor must serve the re-homed worker")
	})

	// Scenario 4. THE NEGATIVE CONTROL FOR THE WHOLE SUITE.
	//
	// Frontend and worker share a host here, so every backend port named in a
	// stream target is one the frontend could dial directly. If it did, the
	// three specs above would pass with the tunnel doing nothing. This one
	// takes the tunnel away and requires the worker to become unreachable,
	// while leaving registration, heartbeats and the roster untouched.
	//
	// It cannot use LOCALAI_WORKER_TUNNEL=false: that is refused at startup
	// now, and a worker that never started says nothing about a worker that is
	// reachable by some other path. The balancer refuses the tunnel dial
	// instead, which leaves a worker that is registered, healthy and holding
	// no tunnel.
	It("cannot reach a worker whose tunnel is refused, and can as soon as it is not", func() {
		var balancer *frontendBalancer
		c, dsn := startClusterOnFreshDB(1, 1, withMockModel("controlled-model"),
			withBalancer(&balancer, func(b *frontendBalancer) { b.blockTunnel.Store(true) }))

		client := inferenceClient(c)
		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), probe.describe)
		nodeID := probe.idOf(c.WorkerName(0))
		Expect(nodeID).ToNot(BeEmpty())

		// The worker is in every respect the one the specs above used: same
		// binary, same environment, registered and healthy. The only difference
		// is the tunnel, and both halves of that are asserted rather than
		// assumed: it tried, and no replica holds it.
		Eventually(balancer.tunnelDials.Load, "60s", "500ms").Should(BeNumerically(">", 0),
			"the worker never dialled its tunnel, so blocking the dial is not what makes it unreachable below")
		owners := newTunnelOwners(openClusterDB(dsn))
		Consistently(func() string { return owners.ownerOf(nodeID) }, "5s", "500ms").Should(BeEmpty(),
			"a replica holds this worker's tunnel, so the blocker is not blocking")

		client.Timeout = tunnelRefusalTimeout
		refused, err := chat(client, c.FrontendURL(0), "controlled-model", "ping")
		Expect(err).ToNot(HaveOccurred(),
			"the request never came back; a worker with no route must be refused, not left parked")
		Expect(refused.status).ToNot(Equal(http.StatusOK),
			"the frontend served an inference for a worker that holds no tunnel, so something other than the tunnel reaches it: %s", refused.body)

		// And it fails for the RIGHT reason. A frontend that was refusing for
		// any other cause (a missing model, a backend it could not install, an
		// unhealthy node) would satisfy the assertion above just as well, and
		// would leave the three specs before this one unproven.
		Expect(refused.body).To(Or(
			ContainSubstring("no route"),
			ContainSubstring("not connected"),
			ContainSubstring("unroutable"),
			ContainSubstring("tunnel"),
		), "the refusal does not name the missing route: %s", refused.body)

		// The control's own control: put the tunnel back, change nothing else,
		// and the same request must now succeed. Without this the refusal above
		// could be any of the ordinary reasons an e2e inference fails.
		balancer.blockTunnel.Store(false)
		Eventually(func() int { return owners.ownerIndexOf(c, 1, nodeID) }, tunnelOwnershipTimeout, tunnelOwnershipPoll).
			Should(Equal(0), owners.describe)

		client.Timeout = tunnelInferenceTimeout
		eventuallyMockedInference(client, c.FrontendURL(0), "controlled-model",
			"the only thing that changed is the tunnel, so the refusal above was the missing tunnel and nothing else")
	})
})

// withMockModel seeds one mock-backend model configuration and its artifact
// into every frontend.
func withMockModel(name string) func(*cluster.Options) {
	return func(o *cluster.Options) {
		if o.Models == nil {
			o.Models = map[string]string{}
		}
		o.Models[name+".yaml"] = mockModelYAML(name)
		o.Models[name+".bin"] = tinyArtifact()
	}
}

// withBalancer sends every worker through one balancer in front of all the
// frontends, and publishes it at into so the spec can drive it.
//
// The balancer is built inside the hook rather than beside the cluster because
// it needs the frontends' ports, and those exist only once Start has brought
// them up; the hook runs per worker, after that. arm runs on the balancer the
// moment it exists, which is before the worker process is spawned, so a spec
// that needs the tunnel blocked from the very first dial can say so without
// racing the worker's first attempt.
func withBalancer(into **frontendBalancer, arm ...func(*frontendBalancer)) func(*cluster.Options) {
	return func(o *cluster.Options) {
		o.WorkerFrontendURL = func(_ int, _ string, frontends []string) string {
			if *into == nil {
				*into = newFrontendBalancer(frontends...)
				for _, apply := range arm {
					apply(*into)
				}
			}
			return (*into).URL()
		}
	}
}

// percentile returns the p'th percentile of durations, which must be sorted.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

// summarise reports the shape of a latency sample.
func summarise(label string, samples []time.Duration) string {
	if len(samples) == 0 {
		return label + ": no samples"
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var total time.Duration
	for _, d := range sorted {
		total += d
	}
	return fmt.Sprintf("%s: n=%d mean=%s p50=%s p90=%s p99=%s max=%s",
		label, len(sorted), total/time.Duration(len(sorted)),
		percentile(sorted, 0.50), percentile(sorted, 0.90), percentile(sorted, 0.99), sorted[len(sorted)-1])
}

// The deferred question of this phase: what one yamux session does when a large
// message and ordinary inference share it, with the relay adding a second hop
// for most requests.
//
// It is MEASURED here rather than asserted to be fine. Both sides' yamux
// windows are left at their defaults by a deliberate decision, pending these
// numbers, and the numbers are printed as a report entry so a future change to
// those windows has something to be compared against.
const (
	// bulkArtifactSize is the model artifact staged over the tunnel while
	// probes run. It stands in for the 50MB-class message this feature has to
	// carry: real model files are far larger, and if a session cannot interleave
	// at this size it certainly cannot at theirs.
	//
	// It is sized against the CONTROL below rather than for realism alone. A
	// cold load costs a few hundred milliseconds before a byte moves (backend
	// install, then the load itself), so at a smaller size the transfer is a
	// minority of the window being measured and a spec could report a clean
	// bill from a window that was mostly not a transfer. That is not
	// hypothetical: the first version of this spec used 64MiB and still passed
	// with the artifact cut to 4KiB, which is the definition of measuring
	// nothing.
	bulkArtifactSize = 128 << 20

	// tinyArtifactSize is the same cold load with nothing to transfer. It is
	// what the bulk window is measured AGAINST, so that the part of the window
	// attributable to moving bytes is a number this spec holds rather than an
	// assumption about the load path.
	tinyArtifactSize = 4 << 10

	// minTransferWindow is how much longer the bulk cold load must take than
	// the tiny one before the numbers below mean anything. It is THE control on
	// this measurement: without it a bulk artifact that shrank, or a staging
	// path that stopped transferring, would leave the spec reporting that a
	// large message does not block inference having sent no large message.
	minTransferWindow = 100 * time.Millisecond

	// holProbeCount is how many completions the baseline is measured over.
	holProbeCount = 40

	// minOverlappingProbes is the measurement's own negative control. A bulk
	// transfer that finishes before any probe ran would report "no head-of-line
	// blocking" having measured nothing at all, which is the shape of vacuous
	// result this phase keeps producing. Below this the spec fails rather than
	// reporting.
	minOverlappingProbes = 5

	// holStallCeiling is the coarse absolute backstop, for the case where the
	// bulk transfer is itself pathologically slow and the transfer-window
	// comparison would admit a probe latency no deployment would tolerate.
	//
	// The comparison is the assertion that bites; this is only the floor under
	// it. A session that head-of-line blocks parks a probe until the transfer
	// releases the session, so the stalled probe's latency is on the order of
	// the transfer window's; one that interleaves answers a warm mock
	// completion in the milliseconds it takes with nothing else on the wire.
	// An ABSOLUTE ceiling would have to sit between those two, and on this
	// hardware they are 30ms and 250ms, which is too narrow a gap to hold on a
	// loaded box. The comparison holds whatever the box's speed, because both
	// numbers move with it.
	holStallCeiling = 5 * time.Second
)

// bulkArtifact is the large model artifact, built once. Both bulk models share
// the string: two copies of it would be two more allocations of
// bulkArtifactSize in the test process for no gain, since what is measured is
// the transfer and not the bytes.
var bulkArtifact = sync.OnceValue(func() string {
	block := strings.Repeat("localai-tunnel-payload-", 45) + "\n" // ~1KiB
	return strings.Repeat(block, bulkArtifactSize/len(block)+1)[:bulkArtifactSize]
})

// tinyArtifact is the artifact of a model that costs a cold load and no
// transfer.
func tinyArtifact() string {
	return strings.Repeat("x", tinyArtifactSize)
}

// probeOnce runs one completion against a warm model and reports how long it
// took, failing on anything but the worker's own answer: a probe that measured
// an error response would report a latency for work that never crossed the
// tunnel.
func probeOnce(client *http.Client, baseURL, model string) (time.Duration, error) {
	result, err := chat(client, baseURL, model, "ping")
	if err != nil {
		return 0, err
	}
	if result.status != http.StatusOK {
		return 0, fmt.Errorf("probe answered %d: %s", result.status, result.body)
	}
	if result.content != mockedReply {
		return 0, fmt.Errorf("probe answered 200 but not with the worker's reply: %s", result.body)
	}
	return result.elapsed, nil
}

// probeN runs n completions back to back. This is the baseline: one request in
// flight at a time, nothing else on the session.
func probeN(client *http.Client, baseURL, model string, n int) ([]time.Duration, error) {
	samples := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		elapsed, err := probeOnce(client, baseURL, model)
		if err != nil {
			return samples, err
		}
		samples = append(samples, elapsed)
	}
	return samples, nil
}

// probeUntil runs completions back to back until stop closes. Same shape as
// probeN, so the two samples differ only in what else was on the session.
func probeUntil(client *http.Client, baseURL, model string, stop <-chan struct{}) ([]time.Duration, error) {
	var samples []time.Duration
	for {
		select {
		case <-stop:
			return samples, nil
		default:
		}
		elapsed, err := probeOnce(client, baseURL, model)
		if err != nil {
			return samples, err
		}
		samples = append(samples, elapsed)
	}
}

var _ = Describe("Worker tunnel under load", Label("Distributed"), Label("Cluster"), func() {
	It("interleaves inference with a bulk transfer on one session, direct and relayed", func() {
		c, dsn := startClusterOnFreshDB(2, 1,
			withMockModel("hol-probe"),
			withMockModel("hol-tiny-direct"),
			withMockModel("hol-tiny-relayed"),
			withBulkModel("hol-bulk-direct"),
			withBulkModel("hol-bulk-relayed"))

		client := inferenceClient(c)
		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), probe.describe)
		nodeID := probe.idOf(c.WorkerName(0))
		Expect(nodeID).ToNot(BeEmpty())

		owners := newTunnelOwners(openClusterDB(dsn))
		var owner int
		Eventually(func() int {
			owner = owners.ownerIndexOf(c, 2, nodeID)
			return owner
		}, tunnelOwnershipTimeout, tunnelOwnershipPoll).Should(BeNumerically(">=", 0), owners.describe)
		nonOwner := 1 - owner

		// Warm the probe model on the worker. Everything measured below is the
		// warm path, so that a probe's latency is the session's and not a cold
		// load's.
		expectMockedInference(client, c.FrontendURL(owner), "hol-probe",
			"the probe model must load before anything is measured")

		report := []string{}

		// coldLoadUnderProbes runs one cold load of model, keeps probing the
		// warm model until it finishes, and reports both.
		coldLoadUnderProbes := func(at, model string) (time.Duration, []time.Duration) {
			GinkgoHelper()
			done := make(chan struct{})
			var result chatResult
			var loadErr error
			started := time.Now()
			go func() {
				defer close(done)
				result, loadErr = chat(client, at, model, "ping")
			}()

			samples, err := probeUntil(client, at, "hol-probe", done)
			elapsed := time.Since(started)
			Expect(err).ToNot(HaveOccurred(), "a probe failed while %s was loading", model)
			Expect(loadErr).ToNot(HaveOccurred())
			Expect(result.status).To(Equal(http.StatusOK),
				"loading %s failed, so nothing measured beside it is a measurement of contention: %s", model, result.body)
			return elapsed, samples
		}

		measure := func(label string, through int, tinyModel, bulkModel string) {
			at := c.FrontendURL(through)

			baseline, err := probeN(client, at, "hol-probe", holProbeCount)
			Expect(err).ToNot(HaveOccurred())

			// The same cold load twice: once with nothing to transfer, once
			// with the bulk artifact. The difference between the two windows is
			// the transfer, which is what this spec is about; everything else
			// about the two loads is identical.
			tinyElapsed, underTiny := coldLoadUnderProbes(at, tinyModel)
			bulkElapsed, underBulk := coldLoadUnderProbes(at, bulkModel)

			transferWindow := bulkElapsed - tinyElapsed
			Expect(transferWindow).To(BeNumerically(">=", minTransferWindow),
				"%s: the %d MiB load took %s and the empty one took %s, so at most %s of the window was spent moving bytes; nothing below would be a measurement of a large message on the session",
				label, bulkArtifactSize>>20, bulkElapsed, tinyElapsed, transferWindow)

			// The second control, on the sample rather than on the window. A
			// transfer nothing ran beside would report a clean bill from a
			// window in which no probe was measured.
			Expect(len(underBulk)).To(BeNumerically(">=", minOverlappingProbes),
				"%s: only %d probes overlapped a transfer window of %s, which is too few to say anything about head-of-line blocking",
				label, len(underBulk), transferWindow)

			line := fmt.Sprintf("%s\n  %s\n  %s\n  %s\n  transfer window: %s of a %s load (%d MiB), empty load %s",
				label,
				summarise("baseline           ", baseline),
				summarise("under empty load   ", underTiny),
				summarise("under bulk load    ", underBulk),
				transferWindow.Round(time.Millisecond), bulkElapsed.Round(time.Millisecond),
				bulkArtifactSize>>20, tinyElapsed.Round(time.Millisecond))
			report = append(report, line)
			GinkgoWriter.Println(line)

			slowest := time.Duration(0)
			for _, d := range underBulk {
				if d > slowest {
					slowest = d
				}
			}
			Expect(slowest).To(BeNumerically("<", transferWindow),
				"%s: a probe waited %s while %s of bytes were moving, which is the shape of a session that stalled the probe until the transfer let go, not of one that interleaved them",
				label, slowest, transferWindow)
			Expect(slowest).To(BeNumerically("<", holStallCeiling),
				"%s: a probe waited %s while the bulk transfer held the session", label, slowest)
		}

		measure("direct (owner replica holds the tunnel)", owner, "hol-tiny-direct", "hol-bulk-direct")
		measure("relayed (through the replica that does not own the tunnel)", nonOwner, "hol-tiny-relayed", "hol-bulk-relayed")

		AddReportEntry("head-of-line blocking on one worker tunnel", strings.Join(report, "\n"))
	})
})

// withBulkModel seeds a model whose artifact is large enough to keep the tunnel
// busy while probes run.
func withBulkModel(name string) func(*cluster.Options) {
	return func(o *cluster.Options) {
		if o.Models == nil {
			o.Models = map[string]string{}
		}
		o.Models[name+".yaml"] = mockModelYAML(name)
		o.Models[name+".bin"] = bulkArtifact()
	}
}
