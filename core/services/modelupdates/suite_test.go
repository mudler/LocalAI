package modelupdates_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestModelUpdates(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Model updates test suite")
}
