package startup_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStartup(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LocalAI startup test")
}
