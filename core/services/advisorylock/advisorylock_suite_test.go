package advisorylock

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAdvisoryLock(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AdvisoryLock test suite")
}
