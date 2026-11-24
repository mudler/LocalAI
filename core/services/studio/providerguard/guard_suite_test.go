package providerguard_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProviderGuard(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "provider guard suite")
}
