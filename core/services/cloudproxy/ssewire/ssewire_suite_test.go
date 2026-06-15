package ssewire

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSsewire(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ssewire test suite")
}
