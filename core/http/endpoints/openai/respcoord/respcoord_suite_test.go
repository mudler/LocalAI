package respcoord

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRespcoord(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "respcoord (realtime M3) Suite")
}
