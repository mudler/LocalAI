package wellknown_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWellKnown(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Studio well-known files")
}
