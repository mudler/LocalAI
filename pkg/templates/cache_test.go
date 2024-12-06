package templates_test

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/templates" // Update with your module path
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const jinjaTemplate = `{{ '<|begin_of_text|>' }}{% if messages[0]['role'] == 'system' %}{% set system_message = messages[0]['content'] %}{% endif %}{% if system_message is defined %}{{ '<|start_header_id|>system<|end_header_id|>

' + system_message + '<|eot_id|>' }}{% endif %}{% for message in messages %}{% set content = message['content'] %}{% if message['role'] == 'user' %}{{ '<|start_header_id|>user<|end_header_id|>

' + content + '<|eot_id|><|start_header_id|>assistant<|end_header_id|>

' }}{% elif message['role'] == 'assistant' %}{{ content + '<|eot_id|>' }}{% endif %}{% endfor %}`

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

		Context("when template is jinja2", func() {
			It("should parse from string", func() {
				result, err := templateCache.EvaluateJinjaTemplate(1, jinjaTemplate, map[string]interface{}{"messages": []map[string]interface{}{{"role": "user", "content": "Hello, Gopher!"}}})
				Expect(err).ToNot(HaveOccurred())
				Expect(result).To(Equal("<|begin_of_text|><|start_header_id|>user<|end_header_id|>\n\nHello, Gopher!<|eot_id|><|start_header_id|>assistant<|end_header_id|>\n\n"))
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
