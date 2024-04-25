package templates_test

import (
	"os"
	"path/filepath"

	"github.com/go-skynet/LocalAI/pkg/templates" // Update with your module path
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TemplateCache", func() {
	var (
		templateCache *templates.TemplateCache
		tempDir       string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "templates")
		Expect(err).NotTo(HaveOccurred())

		// Writing example template files
		err = os.WriteFile(filepath.Join(tempDir, "example.tmpl"), []byte("Hello, {{.Name}}!"), 0600)
		Expect(err).NotTo(HaveOccurred())
		err = os.WriteFile(filepath.Join(tempDir, "empty.tmpl"), []byte(""), 0600)
		Expect(err).NotTo(HaveOccurred())

		templateCache = templates.NewTemplateCache(tempDir)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir) // Clean up
	})

	Describe("EvaluateTemplate", func() {
		Context("when template is loaded successfully", func() {
			It("should evaluate the template correctly", func() {
				result, err := templateCache.EvaluateTemplate(1, "example", map[string]string{"Name": "Gopher"})
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal("Hello, Gopher!"))
			})
		})

		Context("when template isn't a file", func() {
			It("should parse from string", func() {
				result, err := templateCache.EvaluateTemplate(1, "{{.Name}}", map[string]string{"Name": "Gopher"})
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal("Gopher"))
			})
		})

		Context("when template is empty", func() {
			It("should return an empty string", func() {
				result, err := templateCache.EvaluateTemplate(1, "empty", nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(""))
			})
		})
	})

	Describe("concurrency", func() {
		It("should handle multiple concurrent accesses", func(done Done) {
			go func() {
				_, _ = templateCache.EvaluateTemplate(1, "example", map[string]string{"Name": "Gopher"})
			}()
			go func() {
				_, _ = templateCache.EvaluateTemplate(1, "example", map[string]string{"Name": "Gopher"})
			}()
			close(done)
		}, 0.1) // timeout in seconds
	})
})
