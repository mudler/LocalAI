package piiadapter

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPiiAdapter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PII Adapter test suite")
}
