package docx

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDocx(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Office docx writer suite")
}
