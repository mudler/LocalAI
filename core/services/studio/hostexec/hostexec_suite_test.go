package hostexec_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHostExec(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Host commands Suite")
}
