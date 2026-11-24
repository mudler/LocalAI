package sse

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSSE(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SSE test suite")
}
