package auth_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SameOriginFetch", func() {
	req := func(h map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://lan.example:8080/api/x", nil)
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	DescribeTable("tells a same-origin fetch of the page",
		func(h map[string]string, want bool) { Expect(auth.SameOriginFetch(req(h))).To(Equal(want)) },
		Entry("fetch metadata of a same-origin fetch", map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Dest": "empty", "Sec-Fetch-Mode": "cors"}, true),
		Entry("a cross-site fetch", map[string]string{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Dest": "empty", "Sec-Fetch-Mode": "cors"}, false),
		Entry("a navigation", map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate"}, false),
		Entry("no metadata, Origin of this host", map[string]string{"Origin": "http://lan.example:8080"}, true),
		Entry("no metadata, Origin of another host", map[string]string{"Origin": "http://other.example"}, false),
		Entry("no metadata and no Origin or Referer", map[string]string{}, false),
		Entry("no site, a stray destination", map[string]string{"Origin": "http://lan.example:8080", "Sec-Fetch-Dest": "document"}, false),
		Entry("no site, a stray mode", map[string]string{"Origin": "http://lan.example:8080", "Sec-Fetch-Mode": "navigate"}, false),
		Entry("no site, the destination and mode of a fetch", map[string]string{"Origin": "http://lan.example:8080", "Sec-Fetch-Dest": "empty", "Sec-Fetch-Mode": "cors"}, true),
	)
})
