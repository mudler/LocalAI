package preview_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPreview(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "File previews")
}
