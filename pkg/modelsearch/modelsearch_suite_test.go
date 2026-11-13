package modelsearch_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestModelSearch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Model search suite")
}
