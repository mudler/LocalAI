package jina_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJina(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Jina endpoint test suite")
}
