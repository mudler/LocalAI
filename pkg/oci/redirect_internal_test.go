package oci

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("dropAuthorizationOnCrossOriginRedirect", func() {
	// redirect runs the rule for a chain of URLs, the last one being the
	// request about to be sent, and reports the Authorization header left.
	redirect := func(chain ...string) (string, error) {
		GinkgoHelper()
		var via []*http.Request
		for _, u := range chain[:len(chain)-1] {
			r, err := http.NewRequest(http.MethodGet, u, nil)
			Expect(err).NotTo(HaveOccurred())
			via = append(via, r)
		}
		req, err := http.NewRequest(http.MethodGet, chain[len(chain)-1], nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Authorization", "Basic x")
		err = dropAuthorizationOnCrossOriginRedirect(req, via)
		return req.Header.Get("Authorization"), err
	}

	DescribeTable("removes the header when the target leaves the origin",
		func(chain ...string) {
			h, err := redirect(chain...)
			Expect(err).NotTo(HaveOccurred())
			Expect(h).To(BeEmpty())
		},
		Entry("other port", "https://registry.example/v2/a", "https://registry.example:8443/v2/a"),
		Entry("subdomain", "https://registry.example/v2/a", "https://cdn.registry.example/v2/a"),
		Entry("other host", "https://registry.example/v2/a", "https://storage.example/v2/a"),
		Entry("downgrade to http", "https://registry.example/v2/a", "http://registry.example/v2/a"),
		Entry("back to the origin after another host",
			"https://registry.example/v2/a", "https://storage.example/x", "https://registry.example/v2/b"),
	)

	DescribeTable("keeps the header on the same origin",
		func(chain ...string) {
			h, err := redirect(chain...)
			Expect(err).NotTo(HaveOccurred())
			Expect(h).To(Equal("Basic x"))
		},
		Entry("other path", "https://registry.example/v2/a", "https://registry.example/v2/b"),
		Entry("explicit default port", "https://registry.example/v2/a", "https://REGISTRY.example:443/v2/b"),
	)

	It("keeps the net/http redirect cap", func() {
		chain := make([]string, maxRedirects+1)
		for i := range chain {
			chain[i] = "https://registry.example/v2/a"
		}
		_, err := redirect(chain...)
		Expect(err).To(HaveOccurred())
	})
})
