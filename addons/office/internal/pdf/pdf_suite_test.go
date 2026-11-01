package pdf

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPDF(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Office PDF Suite")
}
