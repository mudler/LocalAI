package explorer_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestExplorer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Explorer test suite")
}
