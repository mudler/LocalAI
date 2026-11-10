package bgjob_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBgjob(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "bgjob suite")
}
