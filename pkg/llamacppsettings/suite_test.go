package llamacppsettings_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLlamaCppSettings(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "llama.cpp settings suite")
}
