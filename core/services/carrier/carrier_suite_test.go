package carrier_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCarrier(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Carrier holders test suite")
}
