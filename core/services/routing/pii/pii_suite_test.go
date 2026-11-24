package pii

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPii(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pii test suite")
}
