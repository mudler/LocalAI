package cluster_test

import (
	"os"
	"path/filepath"

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
