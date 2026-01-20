package reasoning_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestReasoning(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Reasoning Suite")
}
