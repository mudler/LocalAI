package buildmatrix_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBuildMatrix(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Build matrix test suite")
}
