// Package e2econtainerusers boots the real container image under the user
// setups that people run it with, and checks in each one that LocalAI starts,
// reports the folders it cannot write, and can install a signed add-on and a
// backend from the official libraries.
//
// It needs Docker and network access to the official registries. Set
// LOCALAI_IMAGE to the image to test (for example local-ai:usermodes).
package e2econtainerusers_test

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	// image is the container image under test, as name:tag.
	image = os.Getenv("LOCALAI_IMAGE")
	// logDir, when set, receives the full log of each container.
	logDir = os.Getenv("LOCALAI_USERMODES_LOG_DIR")
)

func TestContainerUsers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LocalAI container user setups")
}

var _ = BeforeSuite(func() {
	Expect(image).NotTo(BeEmpty(), "set LOCALAI_IMAGE to the image to test")
	if logDir != "" {
		Expect(os.MkdirAll(logDir, 0o755)).To(Succeed())
	}
})
