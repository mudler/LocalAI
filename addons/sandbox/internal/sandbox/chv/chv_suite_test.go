package chv

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestChv(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cloud Hypervisor sandbox backend")
}
