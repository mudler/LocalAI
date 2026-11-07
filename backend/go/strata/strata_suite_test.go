package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStrata(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "strata backend test suite")
}
