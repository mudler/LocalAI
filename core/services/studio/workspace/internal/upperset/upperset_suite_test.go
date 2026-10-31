package upperset_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestUpperset(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Upperset Suite")
}
