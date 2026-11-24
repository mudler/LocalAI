package slotcache_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSlotCache(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Slot cache test suite")
}
