package openai

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOpenAI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "OpenAI Endpoints Suite")
}
