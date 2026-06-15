package launcher_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLauncher(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Launcher Suite")
}
