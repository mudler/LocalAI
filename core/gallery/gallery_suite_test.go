package gallery_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGallery(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Gallery test suite")
}
