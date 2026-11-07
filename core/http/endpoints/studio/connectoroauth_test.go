package studio

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
)

var _ = Describe("the login state of a chat's add-on", func() {
	out_ := func(a studiosvc.AddonInfo) schema.ChatsAddon {
		c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
		return addonOut(c, a)
	}
	row := func(items ...studiosvc.Connection) studiosvc.AddonInfo {
		return studiosvc.AddonInfo{ID: "providerA", Credentialed: true, Connections: items, Enabled: studiosvc.ConnectionSet{Items: items, By: studiosvc.ConnectionByChat}}
	}
	token := studiosvc.Connection{ID: "t", Name: "Token", Handle: "token"}
	ok := studiosvc.Connection{ID: "a", Name: "Work", Handle: "work", State: studiosvc.OAuthConnected}
	ended := studiosvc.Connection{ID: "b", Name: "Home", Handle: "home", State: studiosvc.OAuthReconnectNeeded}

	It("asks for a reconnect when an enabled connection's login ended", func() {
		out := out_(row(ok, ended))
		Expect(out.ConnectionState).To(Equal("reconnect_needed"))
		Expect(out.Connections[0].ConnectionState).To(Equal("connected"))
		Expect(out.Connections[1].ConnectionState).To(Equal("reconnect_needed"))
	})

	It("says connected when the logins work and nothing for tokens", func() {
		Expect(out_(row(ok, token)).ConnectionState).To(Equal("connected"))
		Expect(out_(row(token)).ConnectionState).To(BeEmpty())
		Expect(out_(row(token)).Connections[0].ConnectionState).To(BeEmpty())
	})

	It("looks only at the connections the chat may use", func() {
		r := row(ok)
		r.Connections = append(r.Connections, ended)
		Expect(out_(r).ConnectionState).To(Equal("connected"))
	})
})

var _ = Describe("the return page of a login", func() {
	DescribeTable("accepts pages of the web app only",
		func(p string, want bool) { Expect(validReturnTo(p)).To(Equal(want)) },
		Entry("the app", "/app", true),
		Entry("a page", "/app/chats/connectors", true),
		Entry("an add-on page with a query", "/app/addons/providerA/settings?tab=login", true),
		Entry("a trailing slash", "/app/chats/", true),
		Entry("empty", "", false),
		Entry("another host", "https://elsewhere.example.test/app", false),
		Entry("protocol-relative", "//elsewhere.example.test/app", false),
		Entry("a lookalike", "/apps", false),
		Entry("dot segments", "/app/../api", false),
		Entry("encoded dot segments", "/app/%2E%2E/api", false),
		Entry("a bad escape", "/app/%zz", false),
		Entry("a fragment", "/app#x", false),
		Entry("spaces", "/app/a b", false),
		Entry("a long query", "/app?"+string(make([]byte, 1025)), false),
	)
})
