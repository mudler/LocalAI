package distributed_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/tests/e2e/distributed/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// End-to-end proof that phase 3's control plane works under real processes.
//
// Phase 3 moved fourteen control verbs off NATS and onto the tunnel the worker
// already dials, made absence a database fact rather than a bus timeout, and
// took the backend worker off the bus entirely. Every one of those changes is
// proven by unit and integration specs only; these are the first that run the
// binaries an operator runs and let the real transports fail.
//
// Read the LAST spec before trusting the others. Frontend and worker share a
// host here, so almost anything that looks like it went over the tunnel could
// have gone some other way. The last one takes the tunnel away and requires the
// control plane to become unreachable, with the refusal naming the missing
// route rather than claiming the worker is absent; without it the rest could
// pass with the tunnel doing nothing.

const (
	// controlRPCTimeout bounds one admin control call that has to cross a
	// relay and reach a worker. Generous because the install verb behind it
	// copies an artifact and starts a process.
	controlRPCTimeout = 3 * time.Minute

	// installJobTimeout bounds one node-scoped backend install from accepted to
	// terminal. The gated spec spends most of it deliberately blocked.
	installJobTimeout = "150s"
	installJobPoll    = "250ms"

	// gateHoldWindow is how long a spec requires an install to stay
	// unfinished while its gallery fetch is blocked.
	//
	// It is not a tolerance. The worker is stopped inside an HTTP read that
	// only this spec can complete, so a terminal reply arriving inside this
	// window is a reply written before the work it reports on, which is the
	// exact defect the envelope ordering exists to prevent.
	gateHoldWindow = "3s"
	gateHoldPoll   = "250ms"

	// departedTimeout bounds the wait for a worker whose tunnel is gone past
	// the grace to stop being reported healthy. It has to clear the grace the
	// spec sets plus one health-monitor tick (15s by default).
	departedTimeout = "90s"
	departedPoll    = "1s"

	// churnGrace is the reconnect grace the churn spec runs with: long enough
	// that the whole scenario, including the worker's capped 30s reconnect
	// backoff and the re-home after it, happens INSIDE the window. The claim
	// under test is "nothing is reaped inside the grace", so the window has to
	// be the thing that is generous, not the assertion.
	churnGrace = 10 * time.Minute

	// churnHoldWindow is how long the churn spec requires the fleet to survive
	// with its tunnel genuinely gone. It starts only after the tunnel has been
	// OBSERVED unowned, and it is past the health monitor's 15s tick, so at
	// least one sweep runs against a worker nothing can reach and finds nothing
	// to reap.
	churnHoldWindow = "20s"
	churnHoldPoll   = "1s"

	// churnDropTimeout bounds the wait for a killed replica's claim to stop
	// reading as a live owner.
	//
	// It has to outlast cluster.InstanceLiveness (30s): the ownership query
	// joins against instances the DATABASE still considers live, so for the
	// half minute after a SIGKILL a dead replica is still reported as holding
	// every tunnel it held. That window is the reason this spec waits for the
	// drop instead of assuming the kill produced one, and it is what an earlier
	// version of this spec got wrong: its hold window sat entirely inside the
	// liveness window, so it never reached the branch it claims to test and
	// stayed green with the reconnect grace set to a nanosecond.
	churnDropTimeout = "90s"
	churnDropPoll    = "1s"

	// probeBackend is the backend a control spec installs on a worker.
	//
	// A name nothing else knows, and that is the point: the frontends' own
	// backends directory is empty and the worker's holds only mock-backend, so
	// a node backend listing that names this one can only have come from the
	// worker, and only after an install that reached it.
	probeBackend = "relay-probe"

	// probeGalleryName is the gallery the probe backend is served from.
	probeGalleryName = "e2e-control"
)

// nodeModel is the subset of a /api/nodes/:id/models row these specs assert on.
type nodeModel struct {
	ModelName string `json:"model_name"`
	State     string `json:"state"`
	Address   string `json:"address"`
}

// nodeBackend is the subset of a /api/nodes/:id/backends row these specs assert
// on. It is the worker's own answer to the backend.list control verb, relayed
// back through whichever replica took the request.
type nodeBackend struct {
	Name string `json:"name"`
}

// installJob is the subset of galleryop.OpStatus these specs assert on.
//
// It is read from GET /backends/jobs/:uuid and not from the UI's
// /api/backends/job/:uid, because only the former carries the per-node
// breakdown, and the per-node breakdown is where a worker's progress line
// lands. The UI endpoint drops it.
type installJob struct {
	Processed bool             `json:"processed"`
	Message   string           `json:"message"`
	Error     string           `json:"error"`
	Nodes     []installJobNode `json:"nodes"`
}

type installJobNode struct {
	NodeID string `json:"node_id"`
	Status string `json:"status"`
	Phase  string `json:"phase"`
	Error  string `json:"error"`
}

// nodeEntry returns the per-node row for nodeID, or nil.
func (j installJob) nodeEntry(nodeID string) *installJobNode {
	for i := range j.Nodes {
		if j.Nodes[i].NodeID == nodeID {
			return &j.Nodes[i]
		}
	}
	return nil
}

// controlSession is one admin session for a whole cluster, with a budget long
// enough for a control RPC that crosses a relay.
//
// One per spec and never one per frontend, for the reason inferenceClient gives:
// the auth routes share a five-per-minute-per-IP budget and every request here
// comes from 127.0.0.1.
func controlSession(c *cluster.Cluster) *http.Client {
	GinkgoHelper()
	client, err := c.AdminSession(0)
	Expect(err).ToNot(HaveOccurred())
	client.Timeout = controlRPCTimeout
	return client
}

// nodeBackendNames lists the backends a worker reports installed, asked at one
// specific frontend.
//
// The frontend answers this by issuing the backend.list control verb over that
// worker's tunnel, relayed through the owner when this replica is not it, so
// the returned names are the WORKER's and not this process's.
func nodeBackendNames(c *cluster.Cluster, client *http.Client, frontend int, nodeID string) ([]string, error) {
	var listed []nodeBackend
	if err := c.GetJSON(client, frontend, "/api/nodes/"+nodeID+"/backends", &listed); err != nil {
		return nil, err
	}
	names := []string{}
	for _, b := range listed {
		names = append(names, b.Name)
	}
	return names, nil
}

// nodeModelNames lists the models a frontend records as loaded on a worker.
func nodeModelNames(c *cluster.Cluster, client *http.Client, frontend int, nodeID string) ([]string, error) {
	var rows []nodeModel
	if err := c.GetJSON(client, frontend, "/api/nodes/"+nodeID+"/models", &rows); err != nil {
		return nil, err
	}
	names := []string{}
	for _, m := range rows {
		names = append(names, m.ModelName)
	}
	return names, nil
}

// gatedGallery serves one backend gallery index, and can hold the request open
// until a spec lets it go.
//
// The gate is what makes the ordering assertion deterministic rather than
// tolerant. A worker install that finishes in milliseconds gives a poller no
// window to observe progress in, and a spec that "usually" sees a tick before
// the reply is a spec that passes for timing reasons. Here the worker is
// stopped inside the gallery fetch, which happens AFTER it emits its first
// progress line and BEFORE it can produce any reply, so the two are separated
// by something the spec controls instead of by luck.
//
// fetches counts what the worker actually asked for, so a spec can prove the
// gate is on the path it thinks it is rather than assuming it.
type gatedGallery struct {
	server  *httptest.Server
	index   string
	open    chan struct{}
	once    sync.Once
	fetches atomic.Int64
}

// newGatedGallery starts a gallery serving index. When gated, the first and
// every subsequent fetch blocks until release is called.
func newGatedGallery(index string, gated bool) *gatedGallery {
	GinkgoHelper()
	g := &gatedGallery{index: index, open: make(chan struct{})}
	if !gated {
		g.release()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/index.yaml", func(w http.ResponseWriter, r *http.Request) {
		g.fetches.Add(1)
		select {
		case <-g.open:
		case <-r.Context().Done():
			// The worker gave up, or the spec ended. Answering nothing is
			// right: writing a body here would let a spec that failed its own
			// gate assertion still see a successful install.
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = io.WriteString(w, g.index)
	})
	g.server = httptest.NewServer(mux)
	DeferCleanup(func() {
		// Released before close so a handler still parked on the gate returns
		// instead of leaking until the test binary exits.
		g.release()
		g.server.Close()
	})
	return g
}

func (g *gatedGallery) release() { g.once.Do(func() { close(g.open) }) }

// URL is the index URL a worker fetches.
func (g *gatedGallery) URL() string { return g.server.URL + "/index.yaml" }

// galleriesJSON is the backend_galleries override an install request carries.
// It is a JSON string INSIDE the request body, which is the shape
// InstallBackendOnNodeEndpoint binds.
func (g *gatedGallery) galleriesJSON() string {
	return fmt.Sprintf(`[{"name":%q,"url":%q}]`, probeGalleryName, g.URL())
}

// probeBackendSource lays out a directory a worker can install as a backend and
// then RUN, and returns its path.
//
// It is a real backend by the two rules core/gallery enforces on a directory
// URI: a run.sh, which is the validation gate, and an executable named after
// the backend, which is what the worker's findBackend resolves and starts. The
// executable is the mock backend, so the install ends with a live gRPC process
// rather than with a process that dies and turns a relay spec into a spec about
// a broken artifact.
func probeBackendSource() string {
	GinkgoHelper()
	dir := filepath.Join(GinkgoT().TempDir(), probeBackend)
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())

	binary, err := os.ReadFile(mockBackendBinary())
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(dir, probeBackend), binary, 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "run.sh"),
		[]byte("#!/bin/sh\nexec \"$(dirname \"$0\")/"+probeBackend+"\" \"$@\"\n"), 0o755)).To(Succeed())
	return dir
}

// probeGalleryIndex is the one-entry gallery index that points at src.
func probeGalleryIndex(src string) string {
	return fmt.Sprintf("- name: %s\n  uri: %s\n  description: e2e control-plane probe\n", probeBackend, src)
}

// startNodeInstall posts a node-scoped backend install at one frontend and
// returns the job id it was given.
//
// The install is asynchronous by design (202 plus a job id), so this is where
// the request stops and the job polling below takes over.
func startNodeInstall(c *cluster.Cluster, client *http.Client, frontend int, nodeID, backend, galleriesJSON string) string {
	GinkgoHelper()
	var accepted struct {
		JobID string `json:"jobID"`
	}
	status, err := c.PostJSON(client, frontend, "/api/nodes/"+nodeID+"/backends/install", map[string]string{
		"backend":           backend,
		"backend_galleries": galleriesJSON,
	}, &accepted)
	Expect(err).ToNot(HaveOccurred())
	Expect(status).To(Equal(http.StatusAccepted),
		"frontend %d refused the install outright, so nothing was ever sent to the worker", frontend)
	Expect(accepted.JobID).ToNot(BeEmpty())
	return accepted.JobID
}

// jobProbe polls one install job at one frontend and keeps what it last saw, so
// a failing Eventually can name it.
type jobProbe struct {
	cluster  *cluster.Cluster
	client   *http.Client
	frontend int
	jobID    string
	nodeID   string

	lastErr error
	last    installJob
}

func newJobProbe(c *cluster.Cluster, client *http.Client, frontend int, jobID, nodeID string) *jobProbe {
	return &jobProbe{cluster: c, client: client, frontend: frontend, jobID: jobID, nodeID: nodeID}
}

// read fetches the job once, keeping the error rather than raising it.
func (p *jobProbe) read() installJob {
	var job installJob
	if err := p.cluster.GetJSON(p.client, p.frontend, "/backends/jobs/"+p.jobID, &job); err != nil {
		p.lastErr = err
		return installJob{}
	}
	p.lastErr = nil
	p.last = job
	return job
}

// nodePhase is the phase the worker last reported for this node, or "" when the
// job carries no row for it yet.
func (p *jobProbe) nodePhase() string {
	job := p.read()
	if entry := job.nodeEntry(p.nodeID); entry != nil {
		return entry.Phase
	}
	return ""
}

// nodeStatus is the status the job last recorded for this node.
func (p *jobProbe) nodeStatus() string {
	job := p.read()
	if entry := job.nodeEntry(p.nodeID); entry != nil {
		return entry.Status
	}
	return ""
}

// processed reports whether the job has reached a terminal state.
func (p *jobProbe) processed() bool { return p.read().Processed }

// jobError is the job's error text, empty when it has none.
func (p *jobProbe) jobError() string { return p.read().Error }

// explain builds a lazy failure description.
//
// Gomega formats a (string, args...) description when the assertion is
// CONSTRUCTED, which for an Eventually or a Consistently is before anything has
// gone wrong: the job it quoted would be the one from before the wait. A
// func() string is called only on failure. The same trap, and the same fix, as
// rosterProbe.explain.
func (p *jobProbe) explain(format string, args ...any) func() string {
	return func() string {
		return fmt.Sprintf(format, args...) + ": " + p.describe()
	}
}

func (p *jobProbe) describe() string {
	if p.lastErr != nil {
		return fmt.Sprintf("frontend %d: the last read of job %s failed: %v", p.frontend, p.jobID, p.lastErr)
	}
	return fmt.Sprintf("frontend %d: job %s last read as %+v", p.frontend, p.jobID, p.last)
}

// withReconnectGrace pins how long a lost worker tunnel is read as reconnecting
// rather than gone.
func withReconnectGrace(d time.Duration) func(*cluster.Options) {
	return func(o *cluster.Options) { o.ReconnectGrace = d }
}

// withAgentWorkers adds agent workers, which still speak NATS and hold no
// tunnel, to a cluster.
func withAgentWorkers(n int) func(*cluster.Options) {
	return func(o *cluster.Options) { o.AgentWorkers = n }
}

var _ = Describe("Control plane over the worker tunnel", Label("Distributed"), Label("Cluster"), func() {

	// Scenario 1, the headline. A backend worker that is connected to no bus at
	// all registers, is scheduled onto, and serves inference.
	//
	// A wrong implementation leaves the worker up and inert, because nothing
	// reaches it: before phase 3 every control verb travelled on NATS, so a
	// worker with no NATS URL would register and heartbeat and never be given a
	// backend or a model.
	It("registers, schedules onto and serves a worker that is connected to no bus", func() {
		c, dsn := startClusterOnFreshDB(1, 1, withMockModel("busless-model"))
		client := inferenceClient(c)

		// The environment of the RUNNING process, not the one the harness
		// assembled. LOCALAI_REGISTER_TO is asserted alongside so an absent
		// LOCALAI_NATS_URL is a fact about the worker rather than a read that
		// returned nothing: a broken read would lose both.
		environ, err := c.WorkerEnviron(0)
		Expect(err).ToNot(HaveOccurred())
		Expect(environ).To(ContainElement(HavePrefix("LOCALAI_REGISTER_TO=")),
			"the worker's environment could not be read, so the absence below proves nothing")
		for _, entry := range environ {
			Expect(entry).ToNot(HavePrefix("LOCALAI_NATS_URL="),
				"the worker was handed a bus URL, so this spec is not about a worker that has none")
		}
		// And the deployment it joined DOES have a bus, so "no NATS anywhere"
		// is not what makes this pass.
		Expect(c.NatsURL()).ToNot(BeEmpty())

		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), probe.describe)
		nodeID := probe.idOf(c.WorkerName(0))
		Expect(nodeID).ToNot(BeEmpty())

		// Its tunnel is held here, and it advertises nothing. Both matter: the
		// first says there is a route, the second says there is no other one.
		owners := newTunnelOwners(openClusterDB(dsn))
		Eventually(func() int { return owners.ownerIndexOf(c, 1, nodeID) }, tunnelOwnershipTimeout, tunnelOwnershipPoll).
			Should(Equal(0), owners.describe)
		advertised, carriedKeys := probe.advertisementOf(c.WorkerName(0))
		Expect(carriedKeys).To(BeTrue(),
			"the roster payload no longer carries the advertisement keys, so this spec cannot tell a worker that advertises nothing from one it cannot read")
		Expect(advertised).To(BeEmpty(),
			"the worker advertised %q, so a frontend could have reached it without the tunnel", advertised)

		// The inference is what drives the whole control plane: the frontend
		// installs the backend on the worker, stages the model artifact to it
		// and loads the model, all over the tunnel and all with no bus on the
		// worker's side.
		expectMockedInference(client, c.FrontendURL(0), "busless-model",
			"a worker with no bus connection must still be scheduled onto and serve inference")

		// And the frontend recorded the model as loaded THERE, naming the
		// process the worker started. An empty address would mean the install
		// reply named none, which is refused now rather than substituted.
		var rows []nodeModel
		Expect(c.GetJSON(client, 0, "/api/nodes/"+nodeID+"/models", &rows)).To(Succeed())
		Expect(rows).ToNot(BeEmpty(), "no model is recorded on the worker that just served the request")
		Expect(rows[0].ModelName).To(Equal("busless-model"))
		Expect(rows[0].State).To(Equal("loaded"))
		Expect(rows[0].Address).ToNot(BeEmpty(),
			"the row names no backend process on the worker, so nothing could address it again")
	})

	// Scenario 2, the relay, and scenario 3, the ordering, in one cluster.
	//
	// They are one spec because a relay spec needs something to ask a worker
	// that only the worker can answer, and a fresh worker's backend list is
	// empty: the install is what puts something there. Splitting them would
	// have cost a second cluster to assert less.
	//
	// A wrong implementation answers the control RPC by reaching the worker
	// from the replica that took it, which works on one host and nowhere else,
	// or refuses it as a worker that is not connected. A wrong STREAM
	// implementation writes the terminal reply before the progress it reports
	// on, or drops the progress entirely; both pass every unit spec.
	It("installs a backend, streams its progress in order, and lists it back, all through the replica that does not own the worker", func() {
		c, dsn := startClusterOnFreshDB(2, 1)
		client := controlSession(c)

		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), probe.describe)
		nodeID := probe.idOf(c.WorkerName(0))
		Expect(nodeID).ToNot(BeEmpty())

		// Which replica owns the tunnel is READ through the production Owner
		// query, not assumed. The harness sends the worker to frontend 0 by
		// default, and a spec that wrote that down would keep passing after the
		// default changed while quietly testing the owner path instead.
		owners := newTunnelOwners(openClusterDB(dsn))
		var owner int
		Eventually(func() int {
			owner = owners.ownerIndexOf(c, 2, nodeID)
			return owner
		}, tunnelOwnershipTimeout, tunnelOwnershipPoll).Should(BeNumerically(">=", 0), owners.describe)

		nonOwner := 1 - owner
		Expect(owners.ownerIndexOf(c, 2, nodeID)).ToNot(Equal(nonOwner),
			"frontend %d owns the worker's tunnel, so a request to it would not be relayed and this spec would prove nothing", nonOwner)

		// Nothing is installed on the worker yet, and the request below is the
		// only thing that could change that.
		Expect(nodeBackendNames(c, client, nonOwner, nodeID)).ToNot(ContainElement(probeBackend),
			"the worker already has %q, so a listing that names it later says nothing about this install", probeBackend)

		gallery := newGatedGallery(probeGalleryIndex(probeBackendSource()), true)
		jobID := startNodeInstall(c, client, nonOwner, nodeID, probeBackend, gallery.galleriesJSON())
		job := newJobProbe(c, client, nonOwner, jobID, nodeID)

		// The worker has emitted its first progress line and is now stopped
		// inside the gallery fetch. Seeing the phase here proves a progress
		// envelope crossed the relay and was decoded, and it proves it BEFORE
		// any reply could exist, because the worker cannot produce one until
		// this spec releases the gate.
		Eventually(job.nodePhase, installJobTimeout, installJobPoll).Should(Equal("resolving"), job.describe)
		Eventually(gallery.fetches.Load, installJobTimeout, installJobPoll).Should(BeNumerically(">", 0),
			"the worker never fetched the gated gallery, so the gate is not on the path this spec thinks it is")

		// Held, not sampled. A reply arriving in this window is a reply written
		// ahead of the work it reports on.
		Consistently(job.processed, gateHoldWindow, gateHoldPoll).Should(BeFalse(),
			job.explain("the install reported a terminal result while the worker was still blocked fetching its gallery"))

		gallery.release()

		Eventually(job.processed, installJobTimeout, installJobPoll).Should(BeTrue(), job.describe)
		Expect(job.jobError()).To(BeEmpty(), "the relayed install failed: %s", job.describe())
		Expect(job.nodeStatus()).To(Equal("success"), job.describe)

		// Nothing follows the terminal reply. A worker that appended a late
		// progress line after its reply would move this row back to
		// "downloading"; the guard against that is ndjsonStream.done, and this
		// is the only place it is exercised over a real stream.
		Consistently(job.nodeStatus, gateHoldWindow, gateHoldPoll).Should(Equal("success"),
			job.explain("the node's status moved after the install's terminal reply"))

		// A SECOND control verb across the relay, and the one whose answer
		// could only have come from the worker: the frontends' own backends
		// directories are empty, and this backend was installed on the worker
		// alone.
		Eventually(func() ([]string, error) { return nodeBackendNames(c, client, nonOwner, nodeID) },
			installJobTimeout, installJobPoll).Should(ContainElement(probeBackend))

		// And the frontend that answered does not have it itself, so it cannot
		// have been reporting its own installation as the worker's.
		//
		// Read off the filesystem and not from GET /backends: in distributed
		// mode that endpoint reports the CLUSTER's backends, so it names this
		// one whether the frontend has it or not, and an assertion on it fails
		// for a reason that has nothing to do with what is being proven.
		backendsDir, err := c.FrontendBackendsDir(nonOwner)
		Expect(err).ToNot(HaveOccurred())
		entries, err := os.ReadDir(backendsDir)
		Expect(err).ToNot(HaveOccurred())
		for _, e := range entries {
			Expect(e.Name()).ToNot(Equal(probeBackend),
				"frontend %d installed %q locally, so its answer about the worker may be about itself", nonOwner, probeBackend)
		}

		// THIS is what makes every request above a relayed one. It rules out
		// the two ways they could have succeeded without a relay: a replica
		// that took the tunnel for itself, and the tunnel moving to nonOwner
		// mid-spec so that it served directly. Both leave the owner changed.
		Expect(owners.ownerIndexOf(c, 2, nodeID)).To(Equal(owner),
			"the tunnel is no longer held by frontend %d, so the requests to frontend %d were not necessarily relayed", owner, nonOwner)
	})

	// Scenario 4, absence under replica churn. The catastrophe control.
	//
	// A worker whose tunnel drops and returns INSIDE the grace must not be
	// reaped. A wrong implementation reads a lost tunnel as a departed worker
	// and evicts the models it is serving in the seconds before the re-home,
	// which is the fleet-wide outage the four-valued presence answer exists to
	// prevent.
	It("reaps nothing when the replica holding a worker's tunnel dies and the worker re-homes inside the grace", func() {
		var balancer *frontendBalancer
		c, dsn := startClusterOnFreshDB(2, 1, withMockModel("churn-model"),
			withBalancer(&balancer), withReconnectGrace(churnGrace))

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

		// A model actually loaded on the worker, or there is nothing to reap
		// and the assertions below hold vacuously.
		expectMockedInference(client, c.FrontendURL(owner), "churn-model",
			"inference must work before the owner is killed, or nothing here is at risk")
		Expect(nodeModelNames(c, client, survivor, nodeID)).To(ContainElement("churn-model"))

		// The tunnel is taken away BEFORE the owner is killed, and held away
		// until the assertions below have run.
		//
		// Without the block the worker re-homes onto the survivor within
		// seconds, and a spec that asserted over those seconds would never
		// reach the state it is about: for the first half minute after a
		// SIGKILL the dead replica still reads as a live owner, so presence is
		// "connected" and no rule about a departed worker has anything to act
		// on. Blocking makes the outage last long enough for a departure to be
		// recorded, which is the only way the grace is consulted at all.
		balancer.blockTunnel.Store(true)
		Expect(c.KillFrontend(owner)).To(Succeed())
		Eventually(func() bool { return c.FrontendAlive(owner) }, "20s", "500ms").Should(BeFalse())

		// Read at the SURVIVOR from here on: the probe above is bound to a dead
		// process.
		atSurvivor := newRosterProbe(c, client, survivor)

		// Wait for the drop to be REAL rather than assume the kill produced
		// one. Until this reads empty the ownership query still reports the
		// dead replica, and everything below would be asserting about a worker
		// the deployment believes is connected.
		Eventually(func() string { return owners.ownerOf(nodeID) }, churnDropTimeout, churnDropPoll).
			Should(BeEmpty(), owners.describe)

		// THIS is the window the phase is about: the tunnel is gone, the
		// departure is recorded, and the grace has not run out. Nothing may be
		// reaped in it.
		Consistently(func() []string {
			names, err := nodeModelNames(c, client, survivor, nodeID)
			if err != nil {
				return nil
			}
			return names
		}, churnHoldWindow, churnHoldPoll).Should(ContainElement("churn-model"),
			"the model loaded on the worker was reaped while its tunnel was inside the reconnect grace")
		Consistently(func() string { return atSurvivor.statusOf(c.WorkerName(0)) }, churnHoldWindow, churnHoldPoll).
			ShouldNot(Equal("unhealthy"),
				atSurvivor.explain("the worker was demoted while its tunnel was inside the reconnect grace"))

		// And it comes back: with the block lifted the tunnel re-homes onto the
		// survivor and the same model serves again. Without this the assertions
		// above would be satisfied by a fleet that was simply never touched.
		balancer.blockTunnel.Store(false)
		Eventually(func() int { return owners.ownerIndexOf(c, 2, nodeID) }, tunnelOwnershipTimeout, tunnelOwnershipPoll).
			Should(Equal(survivor), owners.describe)
		Eventually(atSurvivor.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), atSurvivor.describe)
		Expect(atSurvivor.idOf(c.WorkerName(0))).To(Equal(nodeID),
			"the worker re-registered rather than re-homing, so this proves nothing about a tunnel surviving its replica")

		eventuallyMockedInference(client, c.FrontendURL(survivor), "churn-model",
			"the survivor must serve the re-homed worker")
	})

	// Scenario 5, the wedge task 6 fixed, plus the agent-worker control.
	//
	// A worker with a FRESH heartbeat and a tunnel that is gone past the grace
	// must stop being reported healthy. Before task 6 it stayed healthy
	// forever, with every request for a model on it failing "no route", because
	// every reaper keyed on the heartbeat and the heartbeat was fine.
	//
	// The agent worker in the same cluster is the control for the other
	// direction. It still speaks NATS, holds no tunnel and never will, so a
	// rule that read "no tunnel" as "gone" would take the whole agent fleet
	// down with it.
	It("stops reporting a heartbeating worker healthy once its tunnel is gone past the grace, and leaves agent workers alone", func() {
		var balancer *frontendBalancer
		c, dsn := startClusterOnFreshDB(2, 1, withBalancer(&balancer),
			withAgentWorkers(1), withReconnectGrace(10*time.Second))

		client := controlSession(c)
		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(And(ContainElement(c.WorkerName(0)), ContainElement(c.AgentWorkerName(0))), probe.describe)
		nodeID := probe.idOf(c.WorkerName(0))
		Expect(nodeID).ToNot(BeEmpty())

		owners := newTunnelOwners(openClusterDB(dsn))
		var owner int
		Eventually(func() int {
			owner = owners.ownerIndexOf(c, 2, nodeID)
			return owner
		}, tunnelOwnershipTimeout, tunnelOwnershipPoll).Should(BeNumerically(">=", 0), owners.describe)
		survivor := 1 - owner

		// Take the tunnel away permanently: block the dial, then kill the
		// replica holding the live session. The worker keeps registering and
		// heartbeating through the balancer, which still proxies everything
		// except the tunnel connect.
		balancer.blockTunnel.Store(true)
		Expect(c.KillFrontend(owner)).To(Succeed())
		Eventually(func() bool { return c.FrontendAlive(owner) }, "20s", "500ms").Should(BeFalse())

		atSurvivor := newRosterProbe(c, client, survivor)

		// The worker really is trying and really is being refused, so what
		// makes it unreachable below is the tunnel and not the worker dying.
		before := balancer.tunnelDials.Load()
		Eventually(balancer.tunnelDials.Load, "90s", "500ms").Should(BeNumerically(">", before),
			"the worker stopped dialling its tunnel, so blocking the dial is not what keeps it away")

		// The verdict. Fresh heartbeat, no tunnel, past the grace.
		Eventually(func() string { return atSurvivor.statusOf(c.WorkerName(0)) }, departedTimeout, departedPoll).
			Should(Equal("unhealthy"), atSurvivor.describe)

		// The heartbeat was NOT what demoted it. Without this the assertion
		// above is satisfied by a worker that simply died, which says nothing
		// about tunnel departure.
		Expect(atSurvivor.heartbeatOf(c.WorkerName(0))).To(BeTemporally(">", time.Now().Add(-1*time.Minute)),
			"the worker's heartbeat is stale, so it was demoted for being gone rather than for having no route")

		// The agent worker, in the same cluster, under the same grace, on the
		// same health monitor, is untouched. It holds no tunnel either.
		Consistently(func() string { return atSurvivor.statusOf(c.AgentWorkerName(0)) }, "20s", "2s").
			Should(Equal("healthy"),
				atSurvivor.explain("an agent worker was demoted by a rule about tunnels, and agent workers never hold one"))

		// And the demotion reverses when the route comes back, so it is a
		// statement about the route rather than a one-way condemnation.
		balancer.blockTunnel.Store(false)
		Eventually(func() int { return owners.ownerIndexOf(c, 2, nodeID) }, tunnelOwnershipTimeout, tunnelOwnershipPoll).
			Should(Equal(survivor), owners.describe)
		Eventually(func() string { return atSurvivor.statusOf(c.WorkerName(0)) }, departedTimeout, departedPoll).
			Should(Equal("healthy"), atSurvivor.describe)
	})

	// Scenario 6. THE NEGATIVE CONTROL FOR THE WHOLE SUITE.
	//
	// Frontend and worker share a host, so every address the control plane
	// names is one the frontend could also have reached directly. If it did,
	// every spec above would pass with the tunnel doing nothing. This one takes
	// the tunnel away and requires the control plane to become unreachable,
	// with the refusal naming the ROUTE, while registration, heartbeats and the
	// roster stay exactly as they were.
	//
	// It cannot use LOCALAI_WORKER_TUNNEL=false: that is a fatal startup error
	// after phase 2, and a worker that never started says nothing about a
	// worker reachable by some other path.
	It("cannot drive the control plane on a worker whose tunnel is refused, reaps nothing for it, and can as soon as it is not", func() {
		var balancer *frontendBalancer
		c, dsn := startClusterOnFreshDB(1, 1,
			withBalancer(&balancer, func(b *frontendBalancer) { b.blockTunnel.Store(true) }))

		client := controlSession(c)
		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), probe.describe)
		nodeID := probe.idOf(c.WorkerName(0))
		Expect(nodeID).ToNot(BeEmpty())

		// In every respect the worker the specs above used, except the tunnel,
		// and both halves of that are asserted rather than assumed: it tried,
		// and no replica holds it.
		Eventually(balancer.tunnelDials.Load, "60s", "500ms").Should(BeNumerically(">", 0),
			"the worker never dialled its tunnel, so blocking the dial is not what makes it unreachable below")
		owners := newTunnelOwners(openClusterDB(dsn))
		Consistently(func() string { return owners.ownerOf(nodeID) }, "5s", "500ms").Should(BeEmpty(),
			"a replica holds this worker's tunnel, so the blocker is not blocking")

		gallery := newGatedGallery(probeGalleryIndex(probeBackendSource()), false)
		jobID := startNodeInstall(c, client, 0, nodeID, probeBackend, gallery.galleriesJSON())
		job := newJobProbe(c, client, 0, jobID, nodeID)

		Eventually(job.processed, installJobTimeout, installJobPoll).Should(BeTrue(), job.describe)

		// It fails, and it fails for the ROUTING reason. A refusal for any
		// other cause (a gallery it could not read, a node it thought absent, a
		// backend that would not install) would satisfy "it failed" just as
		// well and would leave every spec above unproven.
		//
		// One substring and not a disjunction: "no route" is what
		// cluster.ErrNoRoute reads as, and nothing else on this path produces
		// it. In particular it is NOT what an absent worker produces, and that
		// distinction is the phase's whole absence contract: a worker this
		// replica cannot reach must never be reported as one that has gone.
		Expect(job.jobError()).To(ContainSubstring("no route"),
			"the refusal does not name the missing route, so this spec cannot tell a worker with no tunnel from an ordinary install failure: %s", job.describe())

		// And nothing was reaped for it. The worker never stopped
		// heartbeating, so a control RPC that could not be delivered must not
		// have cost it its row or its health.
		_, listErr := nodeBackendNames(c, client, 0, nodeID)
		Expect(listErr).To(HaveOccurred(),
			"the frontend answered a backend listing for a worker it has no route to, so something other than the tunnel reaches it")
		Consistently(probe.healthyNames, "10s", "1s").
			Should(ContainElement(c.WorkerName(0)),
				probe.explain("the worker was demoted or removed because a control RPC could not be routed to it"))

		// The control's own control: put the tunnel back, change nothing else,
		// and the SAME install must now succeed. Without this the failure above
		// could be any of the ordinary reasons an e2e install fails.
		balancer.blockTunnel.Store(false)
		Eventually(func() int { return owners.ownerIndexOf(c, 1, nodeID) }, tunnelOwnershipTimeout, tunnelOwnershipPoll).
			Should(Equal(0), owners.describe)

		retryID := startNodeInstall(c, client, 0, nodeID, probeBackend, gallery.galleriesJSON())
		retry := newJobProbe(c, client, 0, retryID, nodeID)
		Eventually(retry.processed, installJobTimeout, installJobPoll).Should(BeTrue(), retry.describe)
		Expect(retry.jobError()).To(BeEmpty(),
			"the only thing that changed is the tunnel, so the failure above was the missing tunnel and nothing else: %s", retry.describe())
		Eventually(func() ([]string, error) { return nodeBackendNames(c, client, 0, nodeID) },
			installJobTimeout, installJobPoll).Should(ContainElement(probeBackend))
	})
})
