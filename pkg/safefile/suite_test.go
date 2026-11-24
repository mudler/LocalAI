package safefile_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSafeFile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Safe file Suite")
}
