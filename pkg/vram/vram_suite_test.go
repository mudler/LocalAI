package vram_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVram(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Vram test suite")
}
