package harnessctx_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHarnessCtx(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Harness context windows")
}
