package startup_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/pkg/startup"
	"github.com/mudler/LocalAI/pkg/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Preload test", func() {

	Context("Preloading from strings", func() {
		It("loads from remote url", func() {
			tmpdir, err := os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			libraryURL := "https://raw.githubusercontent.com/mudler/LocalAI/master/embedded/model_library.yaml"
			fileName := fmt.Sprintf("%s.yaml", "phi-2")

			InstallModels([]config.Gallery{}, libraryURL, tmpdir, true, nil, "phi-2")

			resultFile := filepath.Join(tmpdir, fileName)

			content, err := os.ReadFile(resultFile)
			Expect(err).ToNot(HaveOccurred())

			Expect(string(content)).To(ContainSubstring("name: phi-2"))
		})

		It("loads from embedded full-urls", func() {
			tmpdir, err := os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			url := "https://raw.githubusercontent.com/mudler/LocalAI-examples/main/configurations/phi-2.yaml"
			fileName := fmt.Sprintf("%s.yaml", "phi-2")

			InstallModels([]config.Gallery{}, "", tmpdir, true, nil, url)

			resultFile := filepath.Join(tmpdir, fileName)

			content, err := os.ReadFile(resultFile)
			Expect(err).ToNot(HaveOccurred())

			Expect(string(content)).To(ContainSubstring("name: phi-2"))
		})
		It("loads from embedded short-urls", func() {
			tmpdir, err := os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			url := "phi-2"

			InstallModels([]config.Gallery{}, "", tmpdir, true, nil, url)

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

			InstallModels([]config.Gallery{}, "", tmpdir, true, nil, url)

			resultFile := filepath.Join(tmpdir, fileName)

			content, err := os.ReadFile(resultFile)
			Expect(err).ToNot(HaveOccurred())

			Expect(string(content)).To(ContainSubstring("name: mistral-openorca"))
		})
		It("downloads from urls", func() {
			tmpdir, err := os.MkdirTemp("", "")
			Expect(err).ToNot(HaveOccurred())
			url := "huggingface://TheBloke/TinyLlama-1.1B-Chat-v0.3-GGUF/tinyllama-1.1b-chat-v0.3.Q2_K.gguf"
			fileName := fmt.Sprintf("%s.gguf", "tinyllama-1.1b-chat-v0.3.Q2_K")

			err = InstallModels([]config.Gallery{}, "", tmpdir, false, nil, url)
			Expect(err).ToNot(HaveOccurred())

			resultFile := filepath.Join(tmpdir, fileName)

			_, err = os.Stat(resultFile)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
