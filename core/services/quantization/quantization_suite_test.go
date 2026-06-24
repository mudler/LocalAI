package quantization

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestQuantization(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Quantization Suite")
}
