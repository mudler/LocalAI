// SPDX-License-Identifier: MIT
//go:build auth

package addons

import (
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("humanSurface fetch metadata", Label("proposals"), func() {
	const host = "192.168.1.20:8080"
	decide := func(headers map[string]string) bool {
		r := httptest.NewRequest("POST", "http://"+host+"/api/addons/resources", nil)
		r.Host = host
		r.Header.Set(ConfirmHeader, "op-1")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		c := echo.New().NewContext(r, httptest.NewRecorder())
		return humanSurface(c, settings{mode: auth.AddonModeNamedUser}, contract.Actor{CredentialID: "session:abc"}, "op-1")
	}

	DescribeTable("without or with Sec-Fetch-Site",
		func(headers map[string]string, want bool) { Expect(decide(headers)).To(Equal(want)) },
		Entry("strict same-origin fetch", map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"}, true),
		Entry("same-origin without destination", map[string]string{"Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors"}, false),
		Entry("same-host Referer, no Sec-Fetch headers", map[string]string{"Referer": "http://192.168.1.20:8080/app/tables"}, true),
		Entry("same-host Origin, no Sec-Fetch headers", map[string]string{"Origin": "http://192.168.1.20:8080"}, true),
		Entry("foreign Referer", map[string]string{"Referer": "http://evil.example/page"}, false),
		Entry("same host, other port", map[string]string{"Origin": "http://192.168.1.20:9999"}, false),
		Entry("foreign Origin wins over same-host Referer", map[string]string{"Origin": "http://evil.example", "Referer": "http://192.168.1.20:8080/x"}, false),
		Entry("no Origin and no Referer", map[string]string{}, false),
		Entry("cross-site with same-host Referer", map[string]string{"Sec-Fetch-Site": "cross-site", "Referer": "http://192.168.1.20:8080/x"}, false),
		Entry("same-site with same-host Origin", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "http://192.168.1.20:8080"}, false),
		Entry("same-host Referer but navigation destination", map[string]string{"Referer": "http://192.168.1.20:8080/x", "Sec-Fetch-Dest": "document"}, false),
		Entry("same-host Referer but no-cors mode", map[string]string{"Referer": "http://192.168.1.20:8080/x", "Sec-Fetch-Mode": "navigate"}, false),
	)

	It("still refuses a same-host Referer with an API key", func() {
		Expect(decide(map[string]string{"Referer": "http://192.168.1.20:8080/x", "Authorization": "Bearer k"})).To(BeFalse())
	})
})
