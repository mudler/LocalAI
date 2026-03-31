package galleryop_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGalleryOp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GalleryOp Suite")
}
