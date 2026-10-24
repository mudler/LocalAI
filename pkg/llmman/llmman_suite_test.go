package llmman

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLlmman(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "llmman client test suite")
}
