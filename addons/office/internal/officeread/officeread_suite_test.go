package officeread

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOfficeRead(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Office Read Suite")
}
