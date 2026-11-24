package cloudproxy

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCloudproxy(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "cloudproxy test suite")
}
