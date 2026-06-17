package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chat command wiring", func() {
	Describe("chatAPIBaseURL", func() {
		It("adds /v1 to a root endpoint", func() {
			Expect(chatAPIBaseURL("http://127.0.0.1:8080")).To(Equal("http://127.0.0.1:8080/v1"))
		})

		It("keeps endpoints that already include /v1", func() {
			Expect(chatAPIBaseURL("http://127.0.0.1:8080/v1")).To(Equal("http://127.0.0.1:8080/v1"))
			Expect(chatAPIBaseURL("http://127.0.0.1:8080/v1/")).To(Equal("http://127.0.0.1:8080/v1"))
		})

		It("adds a default http scheme", func() {
			Expect(chatAPIBaseURL("127.0.0.1:8080")).To(Equal("http://127.0.0.1:8080/v1"))
		})

		It("preserves non-root paths before /v1", func() {
			Expect(chatAPIBaseURL("http://127.0.0.1:8080/localai")).To(Equal("http://127.0.0.1:8080/localai/v1"))
		})
	})
})
