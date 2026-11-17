package p2prelay

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestP2PRelay(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "p2prelay suite")
}
