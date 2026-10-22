// SPDX-License-Identifier: MIT
package tablesadmin_test

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// deadFrontend is a frontend whose owner address is a port nobody listens on.
func (h *host) deadFrontend() *echo.Echo {
	GinkgoHelper()
	cfg := *h.front
	cfg.OwnerAddress = fmt.Sprintf("https://127.0.0.1:%d", freePort())
	carrier, e := gateway.NewCarrier(cfg)
	Expect(e).NotTo(HaveOccurred())
	db := h.app.AuthDB()
	g := &gateway.Gateway{Forward: carrier, Revalidate: auth.AddonRevalidate(db, h.mode())}
	web := echo.New()
	web.Use(auth.Middleware(db, h.app.ApplicationConfig()))
	routes.RegisterAddonRoutesFor(web, g, db, h.mode())
	return web
}

var _ = Describe("replicas: a forwarding frontend gives the owner's own answers", func() {
	It("answers every typed denial exactly as the owner does and carries every mutation to the owner", func() {
		h := ready(false)
		front := h.frontend()
		res := h.database("bob", "f-1", "Replica")
		Expect(h.grant("bob", res, "carol", "viewer").code).To(Equal(200))
		rev := h.revision("bob", res)

		type call struct {
			name, method, path, body string
			header                   func(*http.Request)
		}
		tab := func(user, id string) func(*http.Request) { return h.tab(user, id) }
		calls := []call{
			{"list", "GET", "/api/addons/resources?addonId=tables", "", h.bearer("bob")},
			{"list of a viewer", "GET", "/api/addons/resources?addonId=tables", "", h.bearer("carol")},
			{"list of an administrator", "GET", "/api/addons/resources?addonId=tables", "", h.bearer("alice")},

			{"malformed add-on", "GET", "/api/addons/resources?addonId=Bad%20ID", "", h.bearer("bob")},
			{"grants", "GET", "/api/addons/resources/" + res + "/grants", "", tab("bob", res)},
			{"grants of a viewer", "GET", "/api/addons/resources/" + res + "/grants", "", tab("carol", res)},
			{"users", "GET", "/api/addons/resources/" + res + "/users?q=Name%20of%20d", "", tab("bob", res)},
			{"short query", "GET", "/api/addons/resources/" + res + "/users?q=d", "", tab("bob", res)},
			{"owner role grant", "PUT", "/api/addons/resources/" + res + "/grants/dave", `{"role":"owner"}`, tab("bob", res)},
			{"grant by a viewer", "PUT", "/api/addons/resources/" + res + "/grants/dave", `{"role":"viewer"}`, tab("carol", res)},
			{"grant to a pending account", "PUT", "/api/addons/resources/" + res + "/grants/frank", `{"role":"viewer"}`, tab("bob", res)},
			{"rename stale", "POST", "/api/addons/resources/" + res + "/rename", `{"operationId":"pr-1","generation":1,"revision":{"schema":77,"data":0},"name":"X"}`, tab("bob", res)},
			{"rename stale generation", "POST", "/api/addons/resources/" + res + "/rename", fmt.Sprintf(`{"operationId":"pr-2","generation":5,"revision":{"schema":%v,"data":%v},"name":"X"}`, rev["schema"], rev["data"]), tab("bob", res)},
			{"rename by a key", "POST", "/api/addons/resources/" + res + "/rename", `{"operationId":"pr-3","generation":1,"revision":{"schema":1,"data":0},"name":"X"}`, h.bearer("bob")},
			{"delete wrong confirmation", "POST", "/api/addons/resources/" + res + "/delete", fmt.Sprintf(`{"operationId":"pd-1","generation":1,"revision":{"schema":%v,"data":%v},"confirm":"nope"}`, rev["schema"], rev["data"]), tab("bob", res)},
			{"delete by a viewer", "POST", "/api/addons/resources/" + res + "/delete", `{"operationId":"pd-2","generation":1,"revision":{"schema":1,"data":0},"confirm":"Replica"}`, tab("carol", res)},
			{"status of a viewer", "GET", "/api/addons/resources/" + res + "/operations/pr-1?kind=rename", "", tab("carol", res)},
			{"selection none", "GET", "/api/addons/conversations/chat-1/selection/tables", "", h.bearer("bob")},
			{"selection of an unknown database", "PUT", "/api/addons/conversations/chat-1/selection/tables", `{"resourceId":"r0123456789abcdef0123456789abcde"}`, h.bearer("bob")},
			{"selection malformed", "PUT", "/api/addons/conversations/bad%20conv/selection/tables", `{"resourceId":"x"}`, h.bearer("bob")},
		}
		for _, c := range calls {
			own := serve(h.web, c.method, c.path, c.body, c.header)
			fwd := serve(front, c.method, c.path, c.body, c.header)
			Expect(fwd.code).To(Equal(own.code), c.name+" owner "+own.raw+" frontend "+fwd.raw)
			Expect(fwd.errType()).To(Equal(own.errType()), c.name)
			Expect(fwd.body).To(Equal(own.body), c.name)
		}

		// Forged forwarding headers change nothing on either.
		forged := func(r *http.Request) {
			h.bearer("carol")(r)
			r.Header.Set("X-Forwarded-Actor", "bob")
			r.Header.Set("X-Addon-Actor", "bob")
		}
		got := serve(front, "GET", "/api/addons/resources?addonId=tables", "", forged)
		Expect(got.code).To(Equal(200))
		Expect(resourcesOf(got)[res]["role"]).To(Equal("viewer"))

		// Mutations made through the frontend are the owner's.
		made := serve(front, "POST", "/api/addons/resources", `{"addonId":"tables","operationId":"fr-1","generation":1,"arguments":{"name":"Via frontend"}}`, h.tab("dave", "fr-1"))
		Expect(made.code).To(Equal(200), made.raw)
		id := made.body["id"].(string)
		Expect(resourcesOf(h.list("dave"))).To(HaveKey(id))
		owned := h.revision("dave", id)
		b := fmt.Sprintf(`{"operationId":"fr-2","generation":1,"revision":{"schema":%v,"data":%v},"name":"Via frontend 2"}`, owned["schema"], owned["data"])
		Expect(serve(front, "POST", "/api/addons/resources/"+id+"/rename", b, h.tab("dave", id)).code).To(Equal(200))
		name, e := h.name("dave", id)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Via frontend 2"))
		Expect(serve(front, "PUT", "/api/addons/resources/"+id+"/grants/bob", `{"role":"editor"}`, h.tab("dave", id)).code).To(Equal(200))
		Expect(resourcesOf(h.list("bob"))[id]["role"]).To(Equal("editor"))
		Expect(serve(front, "DELETE", "/api/addons/resources/"+id+"/grants/bob", "", h.tab("dave", id)).code).To(Equal(200))
		Expect(resourcesOf(h.list("bob"))).NotTo(HaveKey(id))
		sel := serve(front, "PUT", "/api/addons/conversations/fr-chat/selection/tables", fmt.Sprintf(`{"resourceId":%q}`, id), h.bearer("dave"))
		Expect(sel.code).To(Equal(200), sel.raw)
		Expect(h.selection("dave", "fr-chat").body["resourceId"]).To(Equal(id))
		d := fmt.Sprintf(`{"operationId":"fr-3","generation":1,"revision":{"schema":%v,"data":%v},"confirm":"Via frontend 2"}`, owned["schema"], owned["data"])
		del := serve(front, "POST", "/api/addons/resources/"+id+"/delete", d, h.tab("dave", id))
		Expect(del.code).To(Equal(200), del.raw)
		Expect(resourcesOf(h.list("dave"))).NotTo(HaveKey(id))
	})

	It("reports an unknown outcome with the original operation ID and leaves reads plainly unavailable when the owner cannot be reached", func() {
		h := ready(false)
		res := h.database("bob", "u-1", "Lost")
		front := h.deadFrontend()
		rev := h.revision("bob", res)

		for name, c := range map[string]struct {
			method, path, body string
			header             func(*http.Request)
			op                 string
		}{
			"create": {"POST", "/api/addons/resources", `{"addonId":"tables","operationId":"lost-create","generation":1,"arguments":{"name":"N"}}`, h.tab("bob", "lost-create"), "lost-create"},
			"rename": {"POST", "/api/addons/resources/" + res + "/rename", fmt.Sprintf(`{"operationId":"lost-rename","generation":1,"revision":{"schema":%v,"data":%v},"name":"N"}`, rev["schema"], rev["data"]), h.tab("bob", res), "lost-rename"},
			"delete": {"POST", "/api/addons/resources/" + res + "/delete", fmt.Sprintf(`{"operationId":"lost-delete","generation":1,"revision":{"schema":%v,"data":%v},"confirm":"Lost"}`, rev["schema"], rev["data"]), h.tab("bob", res), "lost-delete"},
			"grant":  {"PUT", "/api/addons/resources/" + res + "/grants/carol", `{"role":"viewer","operationId":"lost-grant"}`, h.tab("bob", res), "lost-grant"},
			"revoke": {"DELETE", "/api/addons/resources/" + res + "/grants/carol?operationId=lost-revoke", "", h.tab("bob", res), "lost-revoke"},
		} {
			got := serve(front, c.method, c.path, c.body, c.header)
			Expect(got.code).To(Equal(http.StatusServiceUnavailable), name+" "+got.raw)
			Expect(got.errType()).To(Equal("outcome_unknown"), name)
			Expect(got.errOperation()).To(Equal(c.op), name)
		}
		// Reads and the idempotent selection are plainly unavailable.
		for name, got := range map[string]reply{
			"list":      serve(front, "GET", "/api/addons/resources?addonId=tables", "", h.bearer("bob")),
			"grants":    serve(front, "GET", "/api/addons/resources/"+res+"/grants", "", h.tab("bob", res)),
			"selection": serve(front, "PUT", "/api/addons/conversations/c-1/selection/tables", fmt.Sprintf(`{"resourceId":%q}`, res), h.bearer("bob")),
		} {
			Expect(got.code).To(Equal(http.StatusServiceUnavailable), name)
			Expect(got.errType()).To(Equal("addon_error"), name)
			Expect(got.errOperation()).To(BeEmpty(), name)
		}
		// The owner did none of it.
		Expect(resourcesOf(h.list("bob"))).To(HaveLen(1))
		Expect(h.grants("bob", res).body["grants"]).To(BeEmpty())
		name, e := h.name("bob", res)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Lost"))
	})

	It("answers the sharing denial of local-owner mode the same from a frontend", func() {
		h := ready(true)
		front := h.frontend()
		res := h.database("local-owner", "lf-1", "Local")
		for _, path := range []string{"/grants", "/users?q=ab"} {
			own := h.send("GET", "/api/addons/resources/"+res+path, "", h.tab("x", res))
			fwd := serve(front, "GET", "/api/addons/resources/"+res+path, "", h.tab("x", res))
			Expect(own.code).To(Equal(http.StatusConflict), own.raw)
			Expect(fwd.code).To(Equal(own.code), fwd.raw)
			Expect(fwd.errType()).To(Equal("sharing_disabled"))
		}
	})
})
