package llamacpp_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLlamaCppEngine(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "llama.cpp engine settings suite")
}
