package hostaddr_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHostaddr(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "hostaddr test suite")
}
