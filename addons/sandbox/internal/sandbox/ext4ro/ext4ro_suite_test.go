package ext4ro

import (
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestExt4ro(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ext4 read-only reader")
}

// suiteDir holds the images that the specs build with mke2fs. The images are
// large, so the specs share them and the suite removes the folder at the end.
var suiteDir string

var _ = BeforeSuite(func() {
	var err error
	suiteDir, err = os.MkdirTemp("", "ext4ro-suite-")
	Expect(err).To(Succeed())
})

var _ = AfterSuite(func() {
	if suiteDir != "" {
		_ = os.RemoveAll(suiteDir)
	}
})
