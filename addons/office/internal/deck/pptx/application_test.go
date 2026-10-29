package pptx

import (
	"archive/zip"
	"bytes"
	"io"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// appProps writes a one-slide deck and returns docProps/app.xml from it.
func appProps(application string) string {
	GinkgoHelper()
	slides := []deck.StudioSlide{{Layout: "title", Title: "Hello"}}
	var buf bytes.Buffer
	Expect(Write(&buf, slides, Options{Title: "Hello", Application: application}, nil)).To(Succeed())
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	Expect(err).NotTo(HaveOccurred())
	for _, f := range zr.File {
		if f.Name != "docProps/app.xml" {
			continue
		}
		rc, err := f.Open()
		Expect(err).NotTo(HaveOccurred())
		defer rc.Close()
		b, err := io.ReadAll(rc)
		Expect(err).NotTo(HaveOccurred())
		return string(b)
	}
	Fail("docProps/app.xml missing")
	return ""
}

var _ = Describe("application name", func() {
	It("is the caller's name", func() {
		Expect(appProps("Example App")).To(ContainSubstring("<Application>Example App</Application>"), "app.xml does not carry the caller's name")
	})

	It("is omitted when empty", func() {
		Expect(appProps("")).NotTo(ContainSubstring("<Application>"), "app.xml names an application without a caller value")
	})
})
