package conncoord

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestConncoord(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "conncoord (realtime M1) Suite")
}
