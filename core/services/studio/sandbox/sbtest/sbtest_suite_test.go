package sbtest

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSbtest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox sbtest Suite")
}
