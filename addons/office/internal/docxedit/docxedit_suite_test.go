package docxedit

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDocxedit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Office docxedit suite")
}
