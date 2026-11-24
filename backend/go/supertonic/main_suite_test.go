package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSupertonic(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Supertonic backend test suite")
}
