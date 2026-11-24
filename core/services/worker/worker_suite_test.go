package worker

import (
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWorker(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Worker Suite")
}

// Capacity guards reject symlink components, including macOS /var -> /private/var.
func canonicalWorkerTempDir() string {
	GinkgoHelper()
	dir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
	Expect(err).NotTo(HaveOccurred())
	return dir
}
