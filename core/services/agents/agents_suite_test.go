package agents

import (
	"testing"

	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAgents(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agents test suite")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	return []byte(testutil.StartSharedTestDB())
}, func(endpoint []byte) {
	testutil.SetSharedTestDBEndpoint(string(endpoint))
})

var _ = SynchronizedAfterSuite(func() {}, func() {
	testutil.StopSharedTestDB()
})
