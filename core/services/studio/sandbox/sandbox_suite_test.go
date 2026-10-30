package sandbox_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSandbox(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox Suite")
}
