package p2p

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestP2P(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "P2P Suite")
}
