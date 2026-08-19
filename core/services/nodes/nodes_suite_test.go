package nodes

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestNodes(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Nodes test suite")
}
