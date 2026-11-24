package downloader_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/mudler/LocalAI/pkg/downloader"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CleanupStalePartialFiles", func() {
	var root string

	BeforeEach(func() {
		var err error
		root, err = os.MkdirTemp("", "partials")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		_ = os.RemoveAll(root)
	})

	It("removes stale .partial files (recursively) while keeping fresh ones and completed files", func() {
		nested := filepath.Join(root, "llama-cpp", "models", "foo")
		Expect(os.MkdirAll(nested, 0755)).To(Succeed())

		stale := filepath.Join(nested, "model.gguf.partial")
		fresh := filepath.Join(root, "fresh.gguf.partial")
		completed := filepath.Join(root, "done.gguf")
		for _, f := range []string{stale, fresh, completed} {
			Expect(os.WriteFile(f, []byte("data"), 0644)).To(Succeed())
		}
		old := time.Now().Add(-2 * time.Hour)
		Expect(os.Chtimes(stale, old, old)).To(Succeed())

		removed, err := CleanupStalePartialFiles(root, time.Hour)
		Expect(err).ToNot(HaveOccurred())
		Expect(removed).To(Equal(1))

		Expect(stale).ToNot(BeAnExistingFile())
		Expect(fresh).To(BeAnExistingFile())
		Expect(completed).To(BeAnExistingFile())
	})

	It("returns no error when the root directory does not exist", func() {
		removed, err := CleanupStalePartialFiles(filepath.Join(root, "does-not-exist"), time.Hour)
		Expect(err).ToNot(HaveOccurred())
		Expect(removed).To(Equal(0))
	})
})
