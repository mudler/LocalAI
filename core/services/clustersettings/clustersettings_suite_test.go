package clustersettings_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestClusterSettings(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cluster settings test suite")
}
