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

			err = Apply(tempdir, "", c, map[string]interface{}{})
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

		It("renames model correctly", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile(filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			err = Apply(tempdir, "foo", c, map[string]interface{}{})
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

			err = Apply(tempdir, "foo", c, map[string]interface{}{"backend": "foo"})
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

			err = Apply(tempdir, "../../../foo", c, map[string]interface{}{})
			Expect(err).To(HaveOccurred())
		})
	})
})
