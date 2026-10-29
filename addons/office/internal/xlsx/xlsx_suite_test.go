package xlsx

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestXlsx(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Office xlsx writer suite")
}
