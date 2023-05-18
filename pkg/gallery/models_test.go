package gallery_test

import (
	"os"
	"path/filepath"

	. "github.com/go-skynet/LocalAI/pkg/gallery"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Model test", func() {
	Context("Downloading", func() {
		It("applies model correctly", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile(filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			err = Apply(tempdir, "", c)
			Expect(err).ToNot(HaveOccurred())

			for _, f := range []string{"cerebras", "cerebras-completion.tmpl", "cerebras-chat.tmpl", "cerebras.yaml"} {
				_, err = os.Stat(filepath.Join(tempdir, f))
				Expect(err).ToNot(HaveOccurred())
			}
		})
		It("renames model correctly", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile(filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			err = Apply(tempdir, "foo", c)
			Expect(err).ToNot(HaveOccurred())

			for _, f := range []string{"cerebras", "cerebras-completion.tmpl", "cerebras-chat.tmpl", "foo.yaml"} {
				_, err = os.Stat(filepath.Join(tempdir, f))
				Expect(err).ToNot(HaveOccurred())
			}
		})
		It("catches path traversals", func() {
			tempdir, err := os.MkdirTemp("", "test")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempdir)
			c, err := ReadConfigFile(filepath.Join(os.Getenv("FIXTURES"), "gallery_simple.yaml"))
			Expect(err).ToNot(HaveOccurred())

			err = Apply(tempdir, "../../../foo", c)
			Expect(err).To(HaveOccurred())
		})
	})
})
