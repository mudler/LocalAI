package windows_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWindowsSmoke(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LocalAI Windows smoke test suite")
}
