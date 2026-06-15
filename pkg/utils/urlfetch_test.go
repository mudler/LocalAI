package utils_test

import (
	. "github.com/mudler/LocalAI/pkg/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("utils/urlfetch tests", func() {
	Context("ValidateExternalURL", func() {
		It("allows valid external HTTPS URLs", func() {
			err := ValidateExternalURL("https://example.com/image.png")
			Expect(err).To(BeNil())
		})

		It("allows valid external HTTP URLs", func() {
			err := ValidateExternalURL("http://example.com/image.png")
			Expect(err).To(BeNil())
		})

		It("blocks localhost", func() {
			err := ValidateExternalURL("http://localhost/secret")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("internal"))
		})

		It("blocks 127.0.0.1", func() {
			err := ValidateExternalURL("http://127.0.0.1/secret")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("internal"))
		})

		It("blocks private 10.x.x.x range", func() {
			err := ValidateExternalURL("http://10.0.0.1/secret")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("internal"))
		})

		It("blocks private 172.16.x.x range", func() {
			err := ValidateExternalURL("http://172.16.0.1/secret")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("internal"))
		})

		It("blocks private 192.168.x.x range", func() {
			err := ValidateExternalURL("http://192.168.1.1/secret")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("internal"))
		})

		It("blocks link-local 169.254.x.x (AWS metadata)", func() {
			err := ValidateExternalURL("http://169.254.169.254/latest/meta-data/")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("internal"))
		})

		It("blocks unsupported schemes", func() {
			err := ValidateExternalURL("ftp://example.com/file")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("unsupported URL scheme"))
		})

		It("blocks file:// scheme", func() {
			err := ValidateExternalURL("file:///etc/passwd")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("unsupported URL scheme"))
		})

		It("blocks URLs with no hostname", func() {
			err := ValidateExternalURL("http:///path")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("no hostname"))
		})

		It("blocks .local hostnames", func() {
			err := ValidateExternalURL("http://myservice.local/api")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("internal"))
		})

		It("blocks metadata.google.internal", func() {
			err := ValidateExternalURL("http://metadata.google.internal/computeMetadata/v1/")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("metadata"))
		})

		It("blocks 0.0.0.0", func() {
			err := ValidateExternalURL("http://0.0.0.0/")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("internal"))
		})

		It("blocks IPv6 loopback ::1", func() {
			err := ValidateExternalURL("http://[::1]/secret")
			Expect(err).ToNot(BeNil())
			Expect(err.Error()).To(ContainSubstring("internal"))
		})
	})
})
