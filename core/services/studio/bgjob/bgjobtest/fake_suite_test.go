package bgjobtest_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBgjobtest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "bgjob fake suite")
}
