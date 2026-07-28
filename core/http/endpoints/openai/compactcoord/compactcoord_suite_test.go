package compactcoord

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCompactcoord(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "compactcoord (realtime M4) Suite")
}
