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

	Describe("a wildcard tail", func() {
		tailed := []ClientRoute{
			{ID: "file", Method: "GET", Path: "/files/{path...}"},
			{ID: "put", Method: "PUT", Path: "/files/{path...}", MaxBodyBytes: 4096},
			{ID: "index", Method: "GET", Path: "/files/index"},
			{ID: "one", Method: "GET", Path: "/files/{name}/meta"},
			{ID: "deep", Method: "GET", Path: "/files/a/{rest...}"},
			{ID: "root", Method: "GET", Path: "/{any...}"},
		}
		match := func(method, p string) (string, map[string]string, bool) {
			r, params, found, _ := MatchClientRoute(tailed, method, p)
			return r.ID, params, found
		}

		It("needs feature level 8, and a level 7 host refuses the manifest with the newer-host reason", func() {
			m := withClient(ClientRoute{ID: "a", Method: "GET", Path: "/f/{p...}"})
			Expect(ValidateManifest(m, compatible())).To(MatchError(ContainSubstring("must declare featureLevel 8")))
			m.FeatureLevel = 8
			Expect(ValidateManifest(m, compatible())).To(Succeed())
			old := compatible()
			old.FeatureLevel = 7
			e := ValidateManifest(m, old)
			Expect(e).To(MatchError("needs a newer LocalAI"))
			var refusal *FeatureLevelRefusal
			Expect(e).To(BeAssignableToTypeOf(refusal))
			Expect(HostFeatureLevel).To(BeNumerically(">=", 8))
			// A manifest without a tail still runs on a level 7 host.
			plain := withClient(ok...)
			Expect(ValidateManifest(plain, old)).To(Succeed())
		})

		DescribeTable("refuses a bad tail declaration",
			func(routes ...ClientRoute) {
				m := withClient(routes...)
				m.FeatureLevel = 8
				Expect(ValidateManifest(m, compatible())).To(HaveOccurred())
			},
			Entry("a tail that is not last", ClientRoute{ID: "a", Method: "GET", Path: "/{p...}/x"}),
			Entry("two tails", ClientRoute{ID: "a", Method: "GET", Path: "/a/{p...}/{q...}"}),
			Entry("an upper case name", ClientRoute{ID: "a", Method: "GET", Path: "/a/{P...}"}),
			Entry("a tail with no name", ClientRoute{ID: "a", Method: "GET", Path: "/a/{...}"}),
			Entry("two dots", ClientRoute{ID: "a", Method: "GET", Path: "/a/{p..}"}),
			Entry("a tail name used twice", ClientRoute{ID: "a", Method: "GET", Path: "/{p}/{p...}"}),
			Entry("the same shape twice", ClientRoute{ID: "a", Method: "GET", Path: "/a/{p...}"}, ClientRoute{ID: "b", Method: "GET", Path: "/a/{q...}"}),
			Entry("a tail path that is too deep", ClientRoute{ID: "a", Method: "GET", Path: "/a/b/c/d/e/f/{p...}"}),
		)

		It("allows the same shape for another method, and a tail beside a literal and a variable route", func() {
			m := withClient(tailed...)
			m.FeatureLevel = 8
			Expect(ValidateManifest(m, compatible())).To(Succeed())
		})

		It("matches one or more segments and joins them with a slash", func() {
			id, params, found := match("GET", "/files/notes.md")
			Expect(found).To(BeTrue())
			Expect(id).To(Equal("file"))
			Expect(params).To(Equal(map[string]string{"path": "notes.md"}))
			id, params, _ = match("GET", "/files/2026/11/trip-plan.md")
			Expect(id).To(Equal("file"))
			Expect(params).To(Equal(map[string]string{"path": "2026/11/trip-plan.md"}))
			id, _, _ = match("PUT", "/files/a/b.md")
			Expect(id).To(Equal("put"))
		})

		It("needs at least one segment for the tail", func() {
			_, _, found, _ := MatchClientRoute([]ClientRoute{{ID: "f", Method: "GET", Path: "/files/{path...}"}}, "GET", "/files")
			Expect(found).To(BeFalse())
			_, _, found, _ = MatchClientRoute([]ClientRoute{{ID: "f", Method: "GET", Path: "/files/{path...}"}}, "GET", "/files/")
			Expect(found).To(BeFalse())
		})

		It("lets a route without a tail win over a tail, whatever the order", func() {
			id, _, _ := match("GET", "/files/index")
			Expect(id).To(Equal("index"))
			id, params, _ := match("GET", "/files/abc/meta")
			Expect(id).To(Equal("one"))
			Expect(params).To(Equal(map[string]string{"name": "abc"}))
			reversed := []ClientRoute{tailed[0], tailed[2]}
			r, _, found, _ := MatchClientRoute(reversed, "GET", "/files/index")
			Expect(found).To(BeTrue())
			Expect(r.ID).To(Equal("index"))
			// Anything else under the folder reaches the tail.
			id, _, _ = match("GET", "/files/index/more")
			Expect(id).To(Equal("file"))
		})

		It("decides precedence for each method: a literal route of another method does not shadow the tail", func() {
			mixed := []ClientRoute{
				{ID: "get-tail", Method: "GET", Path: "/files/{path...}"},
				{ID: "put-index", Method: "PUT", Path: "/files/index", MaxBodyBytes: 10},
			}
			r, params, found, mismatch := MatchClientRoute(mixed, "GET", "/files/index")
			Expect(found).To(BeTrue())
			Expect(mismatch).To(BeFalse())
			Expect(r.ID).To(Equal("get-tail"), "the literal route is a PUT; a GET falls to the tail")
			Expect(params).To(Equal(map[string]string{"path": "index"}))
			r, _, found, _ = MatchClientRoute(mixed, "PUT", "/files/index")
			Expect(found).To(BeTrue())
			Expect(r.ID).To(Equal("put-index"))
			_, _, found, mismatch = MatchClientRoute(mixed, "PUT", "/files/other")
			Expect(found).To(BeFalse())
			Expect(mismatch).To(BeTrue(), "no PUT route matches, and a GET route does")
			// The same two routes in the other order give the same answers.
			rev := []ClientRoute{mixed[1], mixed[0]}
			r, _, _, _ = MatchClientRoute(rev, "GET", "/files/index")
			Expect(r.ID).To(Equal("get-tail"))
		})

		It("prefers the tail with the longer fixed part", func() {
			id, params, _ := match("GET", "/files/a/b/c")
			Expect(id).To(Equal("deep"), "both /files/{path...} and /files/a/{rest...} match; the second has the longer fixed part")
			Expect(params).To(Equal(map[string]string{"rest": "b/c"}))
			id, params, _ = match("GET", "/files/z/b")
			Expect(id).To(Equal("file"))
			Expect(params).To(Equal(map[string]string{"path": "z/b"}))
			id, params, _ = match("GET", "/files/a/b")
			Expect(id).To(Equal("deep"))
			Expect(params).To(Equal(map[string]string{"rest": "b"}))
			id, params, _ = match("GET", "/other/x/y")
			Expect(id).To(Equal("root"))
			Expect(params).To(Equal(map[string]string{"any": "other/x/y"}))
		})

		It("tells a wrong method from an unknown path", func() {
			_, _, found, mismatch := MatchClientRoute(tailed, "DELETE", "/files/a")
			Expect(found).To(BeFalse())
			Expect(mismatch).To(BeTrue())
		})

		DescribeTable("matches nothing for a tail that tries to escape",
			func(p string) {
				only := []ClientRoute{{ID: "f", Method: "GET", Path: "/files/{path...}"}}
				_, _, found, _ := MatchClientRoute(only, "GET", p)
				Expect(found).To(BeFalse())
			},
			Entry("dot segment", "/files/."),
			Entry("dot dot", "/files/.."),
			Entry("dot dot in the middle", "/files/a/../b"),
			Entry("dot at the end", "/files/a/b/."),
			Entry("dot dot at the end", "/files/a/.."),
			Entry("empty segment", "/files/a//b"),
			Entry("trailing slash", "/files/a/b/"),
			Entry("percent escape", "/files/a%2fb"),
			Entry("encoded dot", "/files/%2e%2e/b"),
			Entry("backslash", "/files/a\\b"),
			Entry("nul byte", "/files/a\x00b"),
			Entry("space", "/files/a b"),
			Entry("non ascii", "/files/é.md"),
			Entry("a segment over 128 bytes", "/files/"+strings.Repeat("a", 129)),
			Entry("more segments than the tail limit", "/files/"+strings.Repeat("a/", MaxClientTailPathSegments)+"a"),
			Entry("a path over the tail limit", "/files/"+strings.Repeat("a", 100)+"/"+strings.Repeat("b", 100)+"/"+strings.Repeat("c", 100)),
		)

		It("takes a longer path for a tail than for a fixed route, within the tail limits", func() {
			deep := "/files/" + strings.Repeat("a", 60) + "/" + strings.Repeat("b", 60) + "/" + strings.Repeat("c", 60)
			Expect(len(deep)).To(BeNumerically(">", MaxClientPathBytes))
			Expect(len(deep)).To(BeNumerically("<=", MaxClientTailPathBytes))
			id, params, found := match("GET", deep)
			Expect(found).To(BeTrue())
			Expect(id).To(Equal("file"))
			Expect(params["path"]).To(HaveLen(len(deep) - len("/files/")))
			// A fixed route keeps the old limit.
			_, _, found, _ = MatchClientRoute(ok, "GET", "/jobs/"+strings.Repeat("a", 125))
			Expect(found).To(BeFalse())
			_, _, found, _ = MatchClientRoute(tailed, "GET", "/files/a/b/c/d/e/f/g/h/i/j/k")
			Expect(found).To(BeTrue(), "eleven segments below /files, twelve in all")
			_, _, found, _ = MatchClientRoute([]ClientRoute{{ID: "m", Method: "GET", Path: "/files/{name}/meta"}}, "GET", "/files/a/b/c/d/e/f/g")
			Expect(found).To(BeFalse(), "a route without a tail keeps six segments")
		})
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
