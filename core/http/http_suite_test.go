package http_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLocalAI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LocalAI HTTP test suite")
}
