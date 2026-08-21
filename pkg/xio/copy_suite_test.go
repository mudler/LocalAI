package xio_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestXIO(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "XIO Suite")
}
