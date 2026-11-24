package xsysinfo

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestXsysinfo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "xsysinfo test suite")
}
