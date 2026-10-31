package studiohistory_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStudioHistory(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "StudioHistory test suite")
}
