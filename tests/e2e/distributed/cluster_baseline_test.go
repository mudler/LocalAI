package distributed_test

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/tests/e2e/distributed/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// nodeRosterTimeout bounds the wait for a worker to appear healthy in
// /api/nodes. Registration is an HTTP call the worker retries, followed by a
// heartbeat that has to land before the frontend calls the node healthy, so the
// budget covers several retry intervals rather than a single round trip.
const (
	nodeRosterTimeout = "90s"
	nodeRosterPoll    = "1s"
)

// node is the subset of the /api/nodes payload these specs assert on.
type node struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// localAIBinary resolves the built binary, skipping rather than failing when it
// is absent so `make test-e2e-distributed` still runs for someone who has not
// built it. CI always builds it, so CI never skips.
func localAIBinary() string {
	GinkgoHelper()
	path := os.Getenv("LOCALAI_E2E_BINARY")
	if path == "" {
		wd, err := os.Getwd()
		Expect(err).ToNot(HaveOccurred())
		path = filepath.Join(wd, "..", "..", "..", "local-ai")
	}
	if _, err := os.Stat(path); err != nil {
		Skip("local-ai binary not found at " + path + "; run `make build` or set LOCALAI_E2E_BINARY")
	}
	return path
}

func mockBackendBinary() string {
	GinkgoHelper()
	wd, err := os.Getwd()
	Expect(err).ToNot(HaveOccurred())
	path := filepath.Join(wd, "..", "mock-backend", "mock-backend")
	if _, err := os.Stat(path); err != nil {
		Skip("mock-backend not found at " + path + "; run `make build-mock-backend`")
	}
	return path
}

// startCluster brings up a cluster against a freshly provisioned database and
// registers cleanup, including a log dump on failure.
func startCluster(frontends, workers int) *cluster.Cluster {
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

	c, err := cluster.Start(cluster.Options{
		Binary:      binary,
		MockBackend: mockBackend,
		PGDSN:       infra.PGURL,
		NatsURL:     infra.NatsURL,
		LogDir:      logDir,
		Frontends:   frontends,
		Workers:     workers,
	})
	Expect(err).ToNot(HaveOccurred())

	DeferCleanup(func() {
		if CurrentSpecReport().Failed() {
			c.DumpLogs()
		}
		c.Stop()
	})
	return c
}

// healthyNodeNames polls one frontend's roster. It returns nil on any error so
// Eventually keeps retrying: the roster is unreachable for the first moments of
// a replica's life, and a hard failure there would only re-report a startup race.
func healthyNodeNames(c *cluster.Cluster, client *http.Client, frontend int) []string {
	var nodes []node
	if err := c.GetJSON(client, frontend, "/api/nodes", &nodes); err != nil {
		return nil
	}
	names := []string{}
	for _, n := range nodes {
		if n.Status == "healthy" {
			names = append(names, n.Name)
		}
	}
	return names
}

var _ = Describe("Cluster baseline", Label("Distributed"), Label("Cluster"), func() {
	It("brings up a frontend and a worker, and the worker appears in the roster", func() {
		c := startCluster(1, 1)

		client, err := c.AdminSession(0)
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() []string {
			return healthyNodeNames(c, client, 0)
		}, nodeRosterTimeout, nodeRosterPoll).Should(ContainElement(c.WorkerName(0)))
	})

	It("runs two frontends against one database and both see the same worker", func() {
		c := startCluster(2, 1)

		// One session for the whole cluster, minted at frontend 0. Registering or
		// logging in again per frontend would spend from the same five-per-minute
		// budget the auth routes share per client IP, and every request here comes
		// from 127.0.0.1. The single client is valid at both replicas: sessions
		// live in the shared Postgres, the harness pins one HMAC secret so the
		// row resolves anywhere, and Go's cookie jar keys by host without port.
		client, err := c.AdminSession(0)
		Expect(err).ToNot(HaveOccurred())

		for _, frontend := range []int{0, 1} {
			Eventually(func() []string {
				return healthyNodeNames(c, client, frontend)
			}, nodeRosterTimeout, nodeRosterPoll).Should(ContainElement(c.WorkerName(0)),
				"frontend %d should see the worker registered through frontend 0", frontend)
		}
	})
})
