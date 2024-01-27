package startup_test

import (
	"fmt"
	"os"
	"path/filepath"

	. "github.com/go-skynet/LocalAI/pkg/startup"
	"github.com/go-skynet/LocalAI/pkg/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Preload test", func() {

	Context("Preloading from strings", func() {
		It("loads from remote url", func() {
			tmpdir, err := os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			libraryURL := "https://raw.githubusercontent.com/mudler/LocalAI/master/embedded/model_library.yaml"
			fileName := fmt.Sprintf("%s.yaml", "1701d57f28d47552516c2b6ecc3cc719")

			PreloadModelsConfigurations(libraryURL, tmpdir, "phi-2")

			resultFile := filepath.Join(tmpdir, fileName)

			content, err := os.ReadFile(resultFile)
			Expect(err).ToNot(HaveOccurred())

			Expect(string(content)).To(ContainSubstring("name: phi-2"))
		})

		It("loads from embedded full-urls", func() {
			tmpdir, err := os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			url := "https://raw.githubusercontent.com/mudler/LocalAI/master/examples/configurations/phi-2.yaml"
			fileName := fmt.Sprintf("%s.yaml", utils.MD5(url))

			PreloadModelsConfigurations("", tmpdir, url)

			resultFile := filepath.Join(tmpdir, fileName)

			content, err := os.ReadFile(resultFile)
			Expect(err).ToNot(HaveOccurred())

			Expect(string(content)).To(ContainSubstring("name: phi-2"))
		})
		It("loads from embedded short-urls", func() {
			tmpdir, err := os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			url := "phi-2"

			PreloadModelsConfigurations("", tmpdir, url)

			entry, err := os.ReadDir(tmpdir)
			Expect(err).ToNot(HaveOccurred())
			Expect(entry).To(HaveLen(1))
			resultFile := entry[0].Name()

			content, err := os.ReadFile(filepath.Join(tmpdir, resultFile))
			Expect(err).ToNot(HaveOccurred())

			Expect(string(content)).To(ContainSubstring("name: phi-2"))
		})
		It("loads from embedded models", func() {
			tmpdir, err := os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			url := "mistral-openorca"
			fileName := fmt.Sprintf("%s.yaml", utils.MD5(url))

			PreloadModelsConfigurations("", tmpdir, url)

			resultFile := filepath.Join(tmpdir, fileName)

			content, err := os.ReadFile(resultFile)
			Expect(err).ToNot(HaveOccurred())

			Expect(string(content)).To(ContainSubstring("name: mistral-openorca"))
		})
	})
})
