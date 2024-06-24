package gallery_test

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGallery(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Gallery test suite")
}

var _ = BeforeSuite(func() {
	if os.Getenv("FIXTURES") == "" {
		Fail("FIXTURES env var not set")
	}
})
