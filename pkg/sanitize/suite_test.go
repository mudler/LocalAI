package sanitize_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSanitize(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sanitize Suite")
}
