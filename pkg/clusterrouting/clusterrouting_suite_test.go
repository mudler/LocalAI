package clusterrouting

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestClusterRouting(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ClusterRouting Suite")
}
