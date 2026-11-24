package distsync_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDistsync(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Distsync test suite")
}
