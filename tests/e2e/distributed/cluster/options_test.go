package cluster

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Cluster process environment", func() {
	It("changes staging limits only for conformance workers", func() {
		Expect(workerStagingEnv(Options{}, "/worker")).To(BeEmpty())
		Expect(workerStagingEnv(Options{ConformanceStaging: true}, "/worker")).To(Equal([]string{
			"LOCALAI_EPHEMERAL_STAGING_BYTE_LIMIT=1073741824",
			"LOCALAI_EPHEMERAL_STAGING_MIN_FREE_BYTES=1",
			"LOCALAI_MOCK_EXPECT_STAGING_ROOT=/worker",
		}))
	})

	It("sets authenticated deployment controls explicitly", func() {
		Expect(authenticatedDeploymentEnv(Options{})).To(Equal([]string{
			"LOCALAI_AUTO_APPROVE_NODES=true",
		}))
		Expect(authenticatedDeploymentEnv(Options{
			RequireNodeApproval:    true,
			DistributedRequireAuth: true,
		})).To(Equal([]string{
			"LOCALAI_AUTO_APPROVE_NODES=false",
			"LOCALAI_DISTRIBUTED_REQUIRE_AUTH=true",
		}))
	})
})
