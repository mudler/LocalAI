package pdfread

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPDFRead(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PDF Read Suite")
}
