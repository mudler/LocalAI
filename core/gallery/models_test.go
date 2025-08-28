package gallery_test

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

const bertEmbeddingsURL = `https://gist.githubusercontent.com/mudler/0a080b166b87640e8644b09c2aee6e3b/raw/f0e8c26bb72edc16d9fbafbfd6638072126ff225/bert-embeddings-gallery.yaml`

var _ = Describe("Model test", func() {

	BeforeEach(func() {
		if os.Getenv("FIXTURES") == "" {
			Skip("FIXTURES env var not set, skipping model tests")
		}
	})

	Context("Downloading", func() {
		It("applies model correctly", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile[ModelConfig](filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())
			systemState, err := system.GetSystemState(
				system.WithModelPath(tempdir),
			)
			Expect(err).ToNot(HaveOccurred())
			_, err = InstallModel(systemState, "", c, map[string]interface{}{}, func(string, string, string, float64) {}, true)
			Expect(err).ToNot(HaveOccurred())

			for _, f := range []string{"cerebras", "cerebras-completion.tmpl", "cerebras-chat.tmpl", "cerebras.yaml"} {
				_, err = os.Stat(filepath.Join(tempdir, f))
				Expect(err).ToNot(HaveOccurred())
			}

			content := map[string]interface{}{}

			dat, err := os.ReadFile(filepath.Join(tempdir, "cerebras.yaml"))
			Expect(err).ToNot(HaveOccurred())

			err = yaml.Unmarshal(dat, content)
			Expect(err).ToNot(HaveOccurred())

			Expect(content["context_size"]).To(Equal(1024))
		})

		It("applies model from gallery correctly", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)

			gallery := []GalleryModel{{
				Metadata: Metadata{
					Name: "bert",
					URL:  bertEmbeddingsURL,
				},
			}}
			out, err := yaml.Marshal(gallery)
			Expect(err).ToNot(HaveOccurred())
			galleryFilePath := filepath.Join(tempdir, "gallery_simple.yaml")
			err = os.WriteFile(galleryFilePath, out, 0600)
			Expect(err).ToNot(HaveOccurred())
			Expect(filepath.IsAbs(galleryFilePath)).To(BeTrue(), galleryFilePath)
			galleries := []config.Gallery{
				{
					Name: "test",
					URL:  "file://" + galleryFilePath,
				},
			}
			systemState, err := system.GetSystemState(
				system.WithModelPath(tempdir),
			)
			Expect(err).ToNot(HaveOccurred())

			models, err := AvailableGalleryModels(galleries, systemState)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models)).To(Equal(1))
			Expect(models[0].Name).To(Equal("bert"))
			Expect(models[0].URL).To(Equal(bertEmbeddingsURL))
			Expect(models[0].Installed).To(BeFalse())

			err = InstallModelFromGallery(galleries, []config.Gallery{}, systemState, nil, "test@bert", GalleryModel{}, func(s1, s2, s3 string, f float64) {}, true, true)
			Expect(err).ToNot(HaveOccurred())

			dat, err := os.ReadFile(filepath.Join(tempdir, "bert.yaml"))
			Expect(err).ToNot(HaveOccurred())

			content := map[string]interface{}{}
			err = yaml.Unmarshal(dat, &content)
			Expect(err).ToNot(HaveOccurred())
			Expect(content["usage"]).To(ContainSubstring("You can test this model with curl like this"))

			models, err = AvailableGalleryModels(galleries, systemState)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models)).To(Equal(1))
			Expect(models[0].Installed).To(BeTrue())

			// delete
			err = DeleteModelFromSystem(systemState, "bert")
			Expect(err).ToNot(HaveOccurred())

			models, err = AvailableGalleryModels(galleries, systemState)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models)).To(Equal(1))
			Expect(models[0].Installed).To(BeFalse())

			_, err = os.Stat(filepath.Join(tempdir, "bert.yaml"))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
		})

		It("renames model correctly", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile[ModelConfig](filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			systemState, err := system.GetSystemState(
				system.WithModelPath(tempdir),
			)
			Expect(err).ToNot(HaveOccurred())
			_, err = InstallModel(systemState, "foo", c, map[string]interface{}{}, func(string, string, string, float64) {}, true)
			Expect(err).ToNot(HaveOccurred())

			for _, f := range []string{"cerebras", "cerebras-completion.tmpl", "cerebras-chat.tmpl", "foo.yaml"} {
				_, err = os.Stat(filepath.Join(tempdir, f))
				Expect(err).ToNot(HaveOccurred())
			}
		})

		It("overrides parameters", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile[ModelConfig](filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			systemState, err := system.GetSystemState(
				system.WithModelPath(tempdir),
			)
			Expect(err).ToNot(HaveOccurred())
			_, err = InstallModel(systemState, "foo", c, map[string]interface{}{"backend": "foo"}, func(string, string, string, float64) {}, true)
			Expect(err).ToNot(HaveOccurred())

			for _, f := range []string{"cerebras", "cerebras-completion.tmpl", "cerebras-chat.tmpl", "foo.yaml"} {
				_, err = os.Stat(filepath.Join(tempdir, f))
				Expect(err).ToNot(HaveOccurred())
			}

			content := map[string]interface{}{}

			dat, err := os.ReadFile(filepath.Join(tempdir, "foo.yaml"))
			Expect(err).ToNot(HaveOccurred())

			err = yaml.Unmarshal(dat, content)
			Expect(err).ToNot(HaveOccurred())

			Expect(content["backend"]).To(Equal("foo"))
		})

		It("catches path traversals", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile[ModelConfig](filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			systemState, err := system.GetSystemState(
				system.WithModelPath(tempdir),
			)
			Expect(err).ToNot(HaveOccurred())
			_, err = InstallModel(systemState, "../../../foo", c, map[string]interface{}{}, func(string, string, string, float64) {}, true)
			Expect(err).To(HaveOccurred())
		})
	})
})
