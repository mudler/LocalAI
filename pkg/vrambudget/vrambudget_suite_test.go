package vrambudget_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVRAMBudget(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "VRAMBudget test suite")
}
