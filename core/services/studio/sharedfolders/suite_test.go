package sharedfolders_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSharedFolders(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Shared folders Suite")
}
