package pages_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPages(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Studio pages")
}
