package cosignverify_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCosignVerify(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cosignverify test suite")
}
