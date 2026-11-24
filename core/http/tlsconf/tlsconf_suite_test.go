package tlsconf_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTLSConf(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "tlsconf test suite")
}
