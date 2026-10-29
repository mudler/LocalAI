package pptx

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPptx(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Pptx Suite")
}
