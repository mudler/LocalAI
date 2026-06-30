package coordinator

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCoordinator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "coordinator (shared runtime) Suite")
}
