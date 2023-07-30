package gallery_test

import (
	"os"
	"path/filepath"

	. "github.com/go-skynet/LocalAI/pkg/gallery"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("Model test", func() {
	Context("Downloading", func() {
		It("applies model correctly", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile(filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			err = InstallModel(tempdir, "", c, map[string]interface{}{}, func(string, string, string, float64) {})
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
				Name: "bert",
				URL:  "https://raw.githubusercontent.com/go-skynet/model-gallery/main/bert-embeddings.yaml",
			}}
			out, err := yaml.Marshal(gallery)
			Expect(err).ToNot(HaveOccurred())
			err = os.WriteFile(filepath.Join(tempdir, "gallery_simple.yaml"), out, 0644)
			Expect(err).ToNot(HaveOccurred())

			galleries := []Gallery{
				{
					Name: "test",
					URL:  "file://" + filepath.Join(tempdir, "gallery_simple.yaml"),
				},
			}

			models, err := AvailableGalleryModels(galleries, tempdir)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models)).To(Equal(1))
			Expect(models[0].Name).To(Equal("bert"))
			Expect(models[0].URL).To(Equal("https://raw.githubusercontent.com/go-skynet/model-gallery/main/bert-embeddings.yaml"))
			Expect(models[0].Installed).To(BeFalse())

			err = InstallModelFromGallery(galleries, "test@bert", tempdir, GalleryModel{}, func(s1, s2, s3 string, f float64) {})
			Expect(err).ToNot(HaveOccurred())

			dat, err := os.ReadFile(filepath.Join(tempdir, "bert.yaml"))
			Expect(err).ToNot(HaveOccurred())

			content := map[string]interface{}{}
			err = yaml.Unmarshal(dat, &content)
			Expect(err).ToNot(HaveOccurred())
			Expect(content["backend"]).To(Equal("bert-embeddings"))

			models, err = AvailableGalleryModels(galleries, tempdir)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(models)).To(Equal(1))
			Expect(models[0].Installed).To(BeTrue())
		})

		It("renames model correctly", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile(filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			err = InstallModel(tempdir, "foo", c, map[string]interface{}{}, func(string, string, string, float64) {})
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
			c, err := ReadConfigFile(filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			err = InstallModel(tempdir, "foo", c, map[string]interface{}{"backend": "foo"}, func(string, string, string, float64) {})
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
			c, err := ReadConfigFile(filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			err = InstallModel(tempdir, "../../../foo", c, map[string]interface{}{}, func(string, string, string, float64) {})
			Expect(err).To(HaveOccurred())
		})
	})
})
