package inproc

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestInproc(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "localaitools/inproc test suite")
}
