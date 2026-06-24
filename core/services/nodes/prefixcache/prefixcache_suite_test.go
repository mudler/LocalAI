package prefixcache_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPrefixCache(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PrefixCache Suite")
}
