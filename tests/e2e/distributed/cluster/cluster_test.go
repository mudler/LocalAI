package cluster_test

import (
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/LocalAI/tests/e2e/distributed/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Cluster options", Label("Distributed"), func() {
	It("rejects a binary path that does not exist", func() {
		_, err := cluster.Start(cluster.Options{
			Binary:    filepath.Join(os.TempDir(), "definitely-not-local-ai"),
			PGDSN:     "postgres://test:test@127.0.0.1:5432/x?sslmode=disable",
			NatsURL:   "nats://127.0.0.1:4222",
			LogDir:    GinkgoT().TempDir(),
			Frontends: 1,
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("local-ai binary"))
	})

	It("rejects a cluster with no frontends", func() {
		_, err := cluster.Start(cluster.Options{
			Binary:    "/bin/true",
			PGDSN:     "postgres://test:test@127.0.0.1:5432/x?sslmode=disable",
			NatsURL:   "nats://127.0.0.1:4222",
			LogDir:    GinkgoT().TempDir(),
			Frontends: 0,
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("at least one frontend"))
	})
})

// The HTTP flow inside AdminSession and GetJSON cannot run here: it needs a
// built local-ai plus real Postgres and NATS, which arrive with the failover
// suites. These specs cover the argument validation that would otherwise panic
// on an out-of-range slice index inside a helper every later spec calls.
var _ = Describe("Admin session", Label("Distributed"), func() {
	It("reports a clear error when the frontend index is out of range", func() {
		c := cluster.ForTestingEmpty()
		_, err := c.AdminSession(3)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("frontend 3"))
	})

	It("reports a clear error when GetJSON names a frontend that does not exist", func() {
		c := cluster.ForTestingEmpty()
		err := c.GetJSON(httpclient.NewWithTimeout(time.Second), 1, "/api/nodes", &struct{}{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("frontend 1"))
	})
})
