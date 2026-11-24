package p2p_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestP2PEndpoints(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "P2P endpoints test suite")
}
