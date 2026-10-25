package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestQdrantStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "qdrant-store test suite")
}
