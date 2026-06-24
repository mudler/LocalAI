package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLocalStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "local-store test suite")
}
