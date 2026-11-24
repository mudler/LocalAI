package gallery_test

import (
	"os"
	"runtime"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGallery(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Gallery test suite")
}

// A Windows host reports its own capability and only accepts backends built
// for Windows. The specs of this suite exercise the generic selection logic
// with Linux style backend names, so they run under the default capability.
// The specs that test the Windows capability set it themselves.
// Specs that force a capability unset it when they end, so it is set again
// before each spec.
var _ = BeforeEach(func() {
	if runtime.GOOS == "windows" && os.Getenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY") == "" {
		Expect(os.Setenv("LOCALAI_FORCE_META_BACKEND_CAPABILITY", "default")).To(Succeed())
	}
})
