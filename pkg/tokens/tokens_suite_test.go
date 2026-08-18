package tokens

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTokens(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Token counting test suite")
}
