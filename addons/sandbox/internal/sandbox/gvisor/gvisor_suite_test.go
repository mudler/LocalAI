package gvisor

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGvisor(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "gVisor sandbox backend suite")
}
