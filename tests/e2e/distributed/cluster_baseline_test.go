package distributed_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/LocalAI/tests/e2e/distributed/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	// nodeRosterTimeout bounds the wait for a worker to appear healthy in
	// /api/nodes. Registration is an HTTP call the worker retries, followed by a
	// heartbeat that has to land before the frontend calls the node healthy, so
	// the budget covers several retry intervals rather than a single round trip.
	nodeRosterTimeout = "90s"
	nodeRosterPoll    = "1s"
	// authProbeTimeout bounds the single unauthenticated request that checks the
	// admin gate is actually closed. One round trip against a ready local
	// process; anything slower is a defect, not slowness.
	authProbeTimeout = 30 * time.Second
)

// node is the subset of the /api/nodes payload these specs assert on. ID is the
// registration identity the worker minted, which is what distinguishes "the
// same node row seen from a second replica" from "a second registration that
// happens to share a name".
type node struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// requireBinaries reports whether a missing binary must fail the spec instead of
// skipping it. It defaults to ON under CI.
//
// Skipping is the right courtesy locally: someone who has not run `make build`
// should get a clear note, not a wall of red. In CI it is the opposite. The
// whole Cluster label partition is these two specs, so if the workflow's build
// step breaks or moves its output, a skip would leave the job reporting
// "0 Passed | 2 Skipped" and exiting 0. Ginkgo exits 0 on skips, so that job
// goes green having never started a cluster, which is precisely the silent pass
// this suite exists to make impossible.
//
// Hence the polarity: the safe behaviour is the default, keyed off CI (GitHub
// Actions always sets it), and LOCALAI_E2E_REQUIRE_BINARIES exists to be forced
// OFF rather than to be remembered ON. A future workflow author cannot reach
// the green-on-nothing state by forgetting a line, only by writing one that
// explicitly asks for it. A local developer sees no change: CI is unset in an
// ordinary shell, so a missing binary still skips.
func requireBinaries() bool {
	value := strings.TrimSpace(os.Getenv("LOCALAI_E2E_REQUIRE_BINARIES"))
	if value == "" {
		return os.Getenv("CI") != ""
	}
	// ParseBool rejects these, and the fallback below reads anything it rejects
	// as ON. Someone writing "off" plainly means off, and silently inverting
	// them would be a worse trap than the one this flag removes.
	switch strings.ToLower(value) {
	case "off", "no", "n", "disabled":
		return false
	}
	if parsed, err := strconv.ParseBool(value); err == nil {
		return parsed
	}
	// Set to something meaningless means someone meant to turn this on. Reading
	// it as false would quietly restore the silent skip the flag guards against.
	return true
}

// missingBinary skips or fails, naming the path and how to produce it.
func missingBinary(what, path, remedy string) {
	GinkgoHelper()
	message := fmt.Sprintf("%s not found at %s; %s", what, path, remedy)
	if requireBinaries() {
		Fail(message + " (binaries are required here, either under CI or via " +
			"LOCALAI_E2E_REQUIRE_BINARIES, so this fails rather than skips: a skipped " +
			"cluster spec is indistinguishable from a passing one)")
	}
	Skip(message)
}

// localAIBinary resolves the built binary.
func localAIBinary() string {
	GinkgoHelper()
	path := os.Getenv("LOCALAI_E2E_BINARY")
	if path == "" {
		wd, err := os.Getwd()
		Expect(err).ToNot(HaveOccurred())
		path = filepath.Join(wd, "..", "..", "..", "local-ai")
	}
	if _, err := os.Stat(path); err != nil {
		missingBinary("local-ai binary", path, "run `make build` or set LOCALAI_E2E_BINARY")
	}
	return path
}

func mockBackendBinary() string {
	GinkgoHelper()
	wd, err := os.Getwd()
	Expect(err).ToNot(HaveOccurred())
	path := filepath.Join(wd, "..", "mock-backend", "mock-backend")
	if _, err := os.Stat(path); err != nil {
		missingBinary("mock-backend", path, "run `make build-mock-backend`")
	}
	return path
}

// startCluster brings up a cluster against a freshly provisioned database and
// registers cleanup, including a log dump on failure.
//
// customise runs against the assembled Options immediately before Start, for
// the one spec that needs a non-default topology. It is variadic so every
// existing caller keeps the plain two-argument form and the default shape.
func startCluster(frontends, workers int, customise ...func(*cluster.Options)) *cluster.Cluster {
	GinkgoHelper()

	// Resolved before SetupInfra so a missing binary skips without having paid
	// for a database that the skip would then leave to DeferCleanup.
	binary := localAIBinary()
	mockBackend := mockBackendBinary()

	infra := SetupInfra("cluster")

	// The log directory must be predictable so CI can upload it as an artifact.
	// GinkgoT().TempDir() lands under TMPDIR, which on a GitHub runner is not
	// /tmp, so an artifact glob would silently match nothing.
	logDir := os.Getenv("LOCALAI_E2E_LOG_DIR")
	if logDir == "" {
		logDir = GinkgoT().TempDir()
	} else {
		logDir = filepath.Join(logDir, sanitizeDBName(CurrentSpecReport().LeafNodeText))
		Expect(os.MkdirAll(logDir, 0o755)).To(Succeed())
	}

	options := cluster.Options{
		Binary:      binary,
		MockBackend: mockBackend,
		PGDSN:       infra.PGURL,
		NatsURL:     infra.NatsURL,
		LogDir:      logDir,
		Frontends:   frontends,
		Workers:     workers,
	}
	for _, apply := range customise {
		apply(&options)
	}

	c, err := cluster.Start(options)
	Expect(err).ToNot(HaveOccurred())

	DeferCleanup(func() {
		if CurrentSpecReport().Failed() {
			c.DumpLogs()
		}
		c.Stop()
	})
	return c
}

// rosterProbe polls one frontend's node roster.
//
// It keeps the last error and the last roster it saw so a failing Eventually can
// name the cause. Returning a bare nil on error makes a 401 at the second
// replica, a JSON decode failure and "the worker never registered" all present
// identically as an empty list, which is the least useful thing a failover
// suite can say when it goes red.
type rosterProbe struct {
	cluster  *cluster.Cluster
	client   *http.Client
	frontend int

	lastErr  error
	lastSeen []node
}

func newRosterProbe(c *cluster.Cluster, client *http.Client, frontend int) *rosterProbe {
	return &rosterProbe{cluster: c, client: client, frontend: frontend}
}

// healthyNames returns nil on any error so Eventually keeps retrying: the roster
// is unreachable for the first moments of a replica's life, and failing hard
// there would only re-report a startup race.
func (p *rosterProbe) healthyNames() []string {
	var roster []node
	if err := p.cluster.GetJSON(p.client, p.frontend, "/api/nodes", &roster); err != nil {
		p.lastErr = err
		return nil
	}
	p.lastErr = nil
	p.lastSeen = roster
	names := []string{}
	for _, n := range roster {
		if n.Status == "healthy" {
			names = append(names, n.Name)
		}
	}
	return names
}

// idOf returns the registration ID the roster last reported for a node name.
func (p *rosterProbe) idOf(name string) string {
	for _, n := range p.lastSeen {
		if n.Name == name {
			return n.ID
		}
	}
	return ""
}

// describe is handed to Should as the failure message. Gomega calls a
// func() string description lazily, so this runs only on failure and reports
// whichever of the two distinct causes actually occurred.
func (p *rosterProbe) describe() string {
	if p.lastErr != nil {
		return fmt.Sprintf("frontend %d: the last GET /api/nodes failed: %v", p.frontend, p.lastErr)
	}
	return fmt.Sprintf("frontend %d: GET /api/nodes succeeded but the roster held %d node(s): %+v",
		p.frontend, len(p.lastSeen), p.lastSeen)
}

var _ = Describe("Cluster baseline", Label("Distributed"), Label("Cluster"), func() {
	It("brings up a frontend and a worker, and the worker appears in the roster", func() {
		c := startCluster(1, 1)

		client, err := c.AdminSession(0)
		Expect(err).ToNot(HaveOccurred())

		probe := newRosterProbe(c, client, 0)
		Eventually(probe.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), probe.describe)
	})

	It("runs two frontends against one database and both see the same worker", func() {
		c := startCluster(2, 1)

		// Observe that frontend 1 really is gated before proving a session opens
		// it. Without this, "the cookie minted at frontend 0 works here" is
		// indistinguishable from "this endpoint needs no auth at all". The probe
		// is free: it touches no auth route, so it spends nothing from the
		// five-per-minute-per-IP budget those routes share.
		anonymous := httpclient.NewWithTimeout(authProbeTimeout)
		refused, err := anonymous.Get(c.FrontendURL(1) + "/api/nodes")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = refused.Body.Close() }()
		Expect(refused.StatusCode).To(Equal(http.StatusUnauthorized),
			"an unauthenticated GET /api/nodes must be refused, otherwise this spec proves nothing about sessions")

		// One session for the whole cluster, minted at frontend 0. Registering or
		// logging in again per frontend would spend from the same five-per-minute
		// budget, and every request here comes from 127.0.0.1. The single client
		// is valid at both replicas: sessions live in the shared Postgres, the
		// harness pins one HMAC secret so the row resolves anywhere, and Go's
		// cookie jar keys by host without port.
		client, err := c.AdminSession(0)
		Expect(err).ToNot(HaveOccurred())

		// The worker is pointed at frontend 0 alone (the harness sets
		// LOCALAI_REGISTER_TO to frontend 0), so read its identity there first.
		at0 := newRosterProbe(c, client, 0)
		Eventually(at0.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), at0.describe)
		registeredID := at0.idOf(c.WorkerName(0))
		Expect(registeredID).ToNot(BeEmpty(), "frontend 0 reported the worker without a registration ID")

		// Then assert frontend 1 serves the same row, by id and not merely by name.
		//
		// Be precise about what this proves. It does NOT pin the topology:
		// NodeRegistry.Register looks a node up by name and preserves the
		// existing id (core/services/nodes/registry.go:522-527), and both
		// replicas read one Postgres, so a harness that registered the worker
		// with every frontend would yield identical ids here too. What it does
		// catch is a frontend answering from its own registry or its own
		// database rather than the shared one, which is a different regression
		// and just as silent. The topology fact is not asserted anywhere; it is
		// recorded next to LOCALAI_REGISTER_TO in cluster.go.
		at1 := newRosterProbe(c, client, 1)
		Eventually(at1.healthyNames, nodeRosterTimeout, nodeRosterPoll).
			Should(ContainElement(c.WorkerName(0)), at1.describe)
		Expect(at1.idOf(c.WorkerName(0))).To(Equal(registeredID),
			"frontend 1 must resolve the same node row as frontend 0; a differing id means it is not reading the shared state")
	})
})
