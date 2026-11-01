package officecheck

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOfficecheck(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Officecheck Suite")
}
