package backendupdates_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBackendUpdates(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Backend updates suite")
}
