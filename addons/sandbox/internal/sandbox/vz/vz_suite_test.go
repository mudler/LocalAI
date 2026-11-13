package vz

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVz(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Virtualization.framework sandbox backend")
}
