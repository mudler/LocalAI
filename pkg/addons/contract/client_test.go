// SPDX-License-Identifier: MIT
package contract

import (
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Client routes", func() {
	withClient := func(routes ...ClientRoute) Manifest {
		m, e := ParseManifest(fixture())
		Expect(e).NotTo(HaveOccurred())
		m.Capabilities.Service = true
		m.FeatureLevel = 7
		m.Capabilities.Client = &ClientCapability{Routes: routes}
		return m
	}
	ok := []ClientRoute{
		{ID: "ping", Method: "GET", Path: "/ping"},
		{ID: "start", Method: "POST", Path: "/jobs", MaxBodyBytes: 4096},
		{ID: "job", Method: "GET", Path: "/jobs/{id}"},
		{ID: "events", Method: "GET", Path: "/events", Stream: true},
	}

	It("needs feature level 7 and the service capability", func() {
		m := withClient(ok...)
		Expect(ValidateManifest(m, compatible())).To(Succeed())
		m.FeatureLevel = 6
		Expect(ValidateManifest(m, compatible())).To(MatchError(ContainSubstring("must declare featureLevel 7")))
		m = withClient(ok...)
		m.Capabilities.Service = false
		Expect(ValidateManifest(m, compatible())).To(HaveOccurred())
	})

	DescribeTable("refuses a bad declaration",
		func(routes ...ClientRoute) {
			Expect(ValidateManifest(withClient(routes...), compatible())).To(HaveOccurred())
		},
		Entry("no routes"),
		Entry("a path with a dot segment", ClientRoute{ID: "a", Method: "GET", Path: "/a/../b"}),
		Entry("a path with a percent escape", ClientRoute{ID: "a", Method: "GET", Path: "/a%2fb"}),
		Entry("an empty segment", ClientRoute{ID: "a", Method: "GET", Path: "/a//b"}),
		Entry("a trailing slash", ClientRoute{ID: "a", Method: "GET", Path: "/a/"}),
		Entry("a relative path", ClientRoute{ID: "a", Method: "GET", Path: "a"}),
		Entry("an upper case segment", ClientRoute{ID: "a", Method: "GET", Path: "/A"}),
		Entry("a method outside the list", ClientRoute{ID: "a", Method: "TRACE", Path: "/a"}),
		Entry("a stream that is not a GET", ClientRoute{ID: "a", Method: "POST", Path: "/a", Stream: true}),
		Entry("a body on a GET", ClientRoute{ID: "a", Method: "GET", Path: "/a", MaxBodyBytes: 10}),
		Entry("a body over the maximum", ClientRoute{ID: "a", Method: "POST", Path: "/a", MaxBodyBytes: MaxClientBodyBytes + 1}),
		Entry("a timeout over the maximum", ClientRoute{ID: "a", Method: "GET", Path: "/a", TimeoutSeconds: 61}),
		Entry("a repeated id", ClientRoute{ID: "a", Method: "GET", Path: "/a"}, ClientRoute{ID: "a", Method: "GET", Path: "/b"}),
		Entry("two routes for the same request", ClientRoute{ID: "a", Method: "GET", Path: "/j/{x}"}, ClientRoute{ID: "b", Method: "GET", Path: "/j/{y}"}),
		Entry("a repeated variable", ClientRoute{ID: "a", Method: "GET", Path: "/{x}/{x}"}),
		Entry("a path that is too deep", ClientRoute{ID: "a", Method: "GET", Path: "/a/b/c/d/e/f/g"}),
	)

	It("refuses more routes than the limit", func() {
		var many []ClientRoute
		for i := range MaxClientRoutes + 1 {
			many = append(many, ClientRoute{ID: "r" + strings.Repeat("a", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26)), Method: "GET", Path: "/p" + string(rune('a'+i%26)) + string(rune('a'+i/26))})
		}
		Expect(ValidateManifest(withClient(many...), compatible())).To(HaveOccurred())
	})

	Describe("MatchClientRoute", func() {
		It("matches a declared route and binds variables", func() {
			r, params, found, _ := MatchClientRoute(ok, http.MethodGet, "/jobs/abc-1.2")
			Expect(found).To(BeTrue())
			Expect(r.ID).To(Equal("job"))
			Expect(params).To(Equal(map[string]string{"id": "abc-1.2"}))
		})
		It("tells a wrong method from an unknown path", func() {
			_, _, found, mismatch := MatchClientRoute(ok, http.MethodDelete, "/jobs")
			Expect(found).To(BeFalse())
			Expect(mismatch).To(BeTrue())
			_, _, found, mismatch = MatchClientRoute(ok, http.MethodGet, "/nope")
			Expect(found).To(BeFalse())
			Expect(mismatch).To(BeFalse())
		})
		DescribeTable("matches nothing for a path that tries to escape",
			func(p string) {
				_, _, found, _ := MatchClientRoute(ok, http.MethodGet, p)
				Expect(found).To(BeFalse())
			},
			Entry("dot segment as a variable", "/jobs/.."),
			Entry("single dot", "/jobs/."),
			Entry("traversal", "/jobs/../ping"),
			Entry("percent escape", "/jobs/%2e%2e"),
			Entry("encoded slash", "/jobs/a%2fb"),
			Entry("backslash", "/jobs/a\\b"),
			Entry("double slash", "//ping"),
			Entry("empty variable", "/jobs/"),
			Entry("extra segment", "/jobs/a/b"),
			Entry("trailing slash", "/ping/"),
			Entry("nul byte", "/jobs/a\x00b"),
			Entry("non ascii", "/jobs/é"),
			Entry("no leading slash", "ping"),
			Entry("empty", ""),
			Entry("too long", "/jobs/"+strings.Repeat("a", 200)),
		)
	})

	It("passes only a short list of statuses and content types", func() {
		l := DefaultLimits()
		Expect(ClientResponse{Status: 200, ContentType: "application/json", Body: []byte("{}")}.Validate(l)).To(Succeed())
		Expect(ClientResponse{Status: 204}.Validate(l)).To(Succeed())
		Expect(ClientResponse{Status: 302}.Validate(l)).To(HaveOccurred())
		Expect(ClientResponse{Status: 401}.Validate(l)).To(HaveOccurred())
		Expect(ClientResponse{Status: 500}.Validate(l)).To(HaveOccurred())
		Expect(ClientResponse{Status: 200, ContentType: "text/html", Body: []byte("x")}.Validate(l)).To(HaveOccurred())
		Expect(ClientResponse{Status: 200, ContentType: "application/json", Body: make([]byte, l.PayloadBytes+1)}.Validate(l)).To(HaveOccurred())
	})

	It("bounds the query and the event id", func() {
		Expect(ValidateClientQuery(map[string]string{"a": "b"})).To(Succeed())
		Expect(ValidateClientQuery(map[string]string{"a": "b\nc"})).To(HaveOccurred())
		Expect(ValidEventID("abc-123")).To(BeTrue())
		Expect(ValidEventID("a\r\nb")).To(BeFalse())
		Expect(ValidEventID(strings.Repeat("a", 129))).To(BeFalse())
	})
})
