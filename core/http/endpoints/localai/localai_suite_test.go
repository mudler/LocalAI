package localai_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLocalAIEndpoints(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LocalAI Endpoints test suite")
}
