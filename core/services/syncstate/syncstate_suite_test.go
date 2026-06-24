package syncstate_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSyncstate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Syncstate Suite")
}
