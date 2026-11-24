// SPDX-License-Identifier: MIT
package addons

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("browser protection", func() {
	type guardCase struct {
		name    string
		headers map[string]string
		session bool
		want    int
	}
	const host = "192.0.2.10:8080"
	cases := []guardCase{
		{name: "Sec-Fetch-Site same-origin passes", headers: map[string]string{"Sec-Fetch-Site": "same-origin"}, session: true, want: 200},
		{name: "Sec-Fetch-Site cross-site is refused even with a Referer on the same host", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Referer": "http://" + host + "/app/addons"}, session: true, want: 403},
		{name: "Sec-Fetch-Site same-site is refused", headers: map[string]string{"Sec-Fetch-Site": "same-site"}, session: true, want: 403},
		{name: "no Sec-Fetch-Site with a Referer on the same host passes", headers: map[string]string{"Referer": "http://" + host + "/app/addons"}, session: true, want: 200},
		{name: "no Sec-Fetch-Site with an Origin on the same host passes", headers: map[string]string{"Origin": "http://" + host}, session: true, want: 200},
		{name: "no Sec-Fetch-Site with a foreign Referer is refused", headers: map[string]string{"Referer": "http://evil.example/page"}, session: true, want: 403},
		{name: "no Sec-Fetch-Site with a foreign Origin is refused", headers: map[string]string{"Origin": "http://evil.example"}, session: true, want: 403},
		{name: "no Sec-Fetch-Site with a foreign Origin is refused even with a Referer on the same host", headers: map[string]string{"Origin": "http://evil.example", "Referer": "http://" + host + "/"}, session: true, want: 403},
		{name: "no Sec-Fetch-Site with the same host on another port is refused", headers: map[string]string{"Origin": "http://192.0.2.10:9999"}, session: true, want: 403},
		{name: "no Sec-Fetch-Site with a null Origin is refused", headers: map[string]string{"Origin": "null"}, session: true, want: 403},
		{name: "no Sec-Fetch-Site and no Origin or Referer is refused", headers: map[string]string{}, session: true, want: 403},
		{name: "a request without a session cookie is not guarded", headers: map[string]string{}, session: false, want: 200},
	}
	for _, tc := range cases {
		It(tc.name, func() {
			e := echo.New()
			e.POST("/x", func(c echo.Context) error { return c.NoContent(200) }, BrowserProtection())
			r := httptest.NewRequest(http.MethodPost, "/x", nil)
			r.Host = host
			for k, v := range tc.headers {
				if v != "" {
					r.Header.Set(k, v)
				}
			}
			if tc.session {
				r.AddCookie(&http.Cookie{Name: "session", Value: "s"})
			}
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			Expect(w.Code).To(Equal(tc.want))
		})
	}

	It("matches the host without regard to letter case", func() {
		e := echo.New()
		e.POST("/x", func(c echo.Context) error { return c.NoContent(200) }, BrowserProtection())
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		r.Host = "Box.Example:8080"
		r.Header.Set("Origin", "http://box.example:8080")
		r.AddCookie(&http.Cookie{Name: "session", Value: "s"})
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		Expect(w.Code).To(Equal(200))
	})
})
