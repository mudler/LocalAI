//go:build linux || darwin

package host_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHost(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Host Backend Suite")
}
