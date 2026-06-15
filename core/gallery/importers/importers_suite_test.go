package importers_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestImporters(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Importers test suite")
}
