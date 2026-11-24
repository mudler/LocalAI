package billing

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBilling(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "billing test suite")
}
