package xlsxedit

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestXlsxedit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Office xlsxedit suite")
}
