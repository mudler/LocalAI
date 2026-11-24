package p2prelaymgr_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestP2PRelayMgr(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "p2prelaymgr suite")
}
