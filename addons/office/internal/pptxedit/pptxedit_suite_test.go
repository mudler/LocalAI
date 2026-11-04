package pptxedit

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPptxedit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Pptxedit Suite")
}
