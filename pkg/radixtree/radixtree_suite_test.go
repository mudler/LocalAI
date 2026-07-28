package radixtree_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRadixTree(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RadixTree Suite")
}
