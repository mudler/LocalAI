package anthropic

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAnthropic(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Anthropic test suite")
}
