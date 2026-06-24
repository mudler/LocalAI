package turncoord

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTurncoord(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "turncoord (realtime M2) Suite")
}
