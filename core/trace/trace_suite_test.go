package trace_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTrace(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Trace test suite")
}
