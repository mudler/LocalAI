package vllmmultinode_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVLLMMultinode(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "vLLM Multi-node DP Suite")
}
