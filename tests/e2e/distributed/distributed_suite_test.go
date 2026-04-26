package distributed_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDistributed(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Distributed Architecture E2E Suite")
}
