package mitm

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMitm(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "mitm test suite")
}
