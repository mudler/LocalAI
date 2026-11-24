package peg_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPeg(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PEG Parser test suite")
}
