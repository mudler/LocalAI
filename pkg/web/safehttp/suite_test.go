package safehttp_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSafeHTTP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "safehttp suite")
}
