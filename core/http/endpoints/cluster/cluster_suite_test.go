package cluster_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestClusterEndpoints(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cluster Endpoints Suite")
}
