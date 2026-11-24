//go:build darwin && arm64

package vmm

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVmmDarwin(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Virtualization.framework binding on macOS")
}
