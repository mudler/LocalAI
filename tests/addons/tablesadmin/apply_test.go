// SPDX-License-Identifier: MIT
package tablesadmin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// apply sends one UI change as the person's own tab.
func (h *host) apply(user, res, op, tool string, rev map[string]any, args string) reply {
	GinkgoHelper()
	return h.applyAs(h.tab(user, res), res, op, tool, rev, args)
}

func (h *host) applyAs(header func(*http.Request), res, op, tool string, rev map[string]any, args string) reply {
	GinkgoHelper()
	b, _ := json.Marshal(map[string]any{"operationId": op, "generation": 1, "revision": rev, "tool": tool, "arguments": json.RawMessage(args)})
	return h.send("POST", "/api/addons/resources/"+res+"/apply", string(b), header)
}

// read runs a typed read as the person and returns the payload.
func (h *host) read(user, res, args string) (map[string]any, error) {
	GinkgoHelper()
	out, e := h.app.AddonGateway().Dispatch(context.Background(), h.actor(user), contract.Request{
		AddonID: "tables", ResourceID: res, OperationID: "read-" + uuid.NewString()[:8], Generation: 1, Action: "read",
		Arguments: json.RawMessage(args),
	})
	if e != nil {
		return nil, e
	}
	var v map[string]any
	Expect(json.Unmarshal(out.Payload, &v)).To(Succeed())
	return v, nil
}

func revisionOf(v map[string]any) map[string]any {
	GinkgoHelper()
	rev, _ := v["revision"].(map[string]any)
	Expect(rev).NotTo(BeNil())
	return rev
}

var _ = Describe("UI changes of a database by the person's own action", func() {
	It("applies schema and row changes under the roles of the host, once per operation, with a receipt and no proposal", func() {
		h := ready(false)
		res := h.database("bob", "ap-1", "Ledger")
		Expect(h.grant("bob", res, "carol", "viewer").code).To(Equal(200))
		Expect(h.grant("bob", res, "dave", "editor").code).To(Equal(200))
		inspect := func(user string) map[string]any {
			v, e := h.read(user, res, `{"tool":"databases-inspect"}`)
			Expect(e).NotTo(HaveOccurred())
			return v
		}

		// The owner changes the schema: a table and a column.
		made := h.apply("bob", res, "ap-t1", "tables-create", revisionOf(inspect("bob")), `{"table":"Entries"}`)
		Expect(made.code).To(Equal(200), made.raw)
		Expect(made.body["resourceId"]).To(Equal(res))
		Expect(made.body["operationId"]).To(Equal("ap-t1"))
		Expect(made.body["revision"]).NotTo(Equal(map[string]any{"schema": 1, "data": 0}))
		col := h.apply("bob", res, "ap-c1", "columns-add", revisionOf(inspect("bob")), `{"table":"Entries","column":"Item","type":"text","required":true}`)
		Expect(col.code).To(Equal(200), col.raw)
		qty := h.apply("bob", res, "ap-c2", "columns-add", revisionOf(inspect("bob")), `{"table":"Entries","column":"Qty","type":"integer"}`)
		Expect(qty.code).To(Equal(200), qty.raw)

		// An editor and the owner write rows; the receipt reports the ids.
		rev := revisionOf(inspect("dave"))
		ins := h.apply("dave", res, "ap-r1", "rows-insert", rev, `{"table":"Entries","rows":[{"Item":"pen","Qty":3},{"Item":"ink","Qty":1}]}`)
		Expect(ins.code).To(Equal(200), ins.raw)
		result, _ := ins.body["result"].(map[string]any)
		Expect(result["count"]).To(BeEquivalentTo(2))
		Expect(result["ids"]).To(HaveLen(2))
		page, e := h.read("carol", res, `{"tool":"rows-query","table":"Entries"}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(page["rows"]).To(HaveLen(2))

		// The same operation with the same request is the same change.
		again := h.apply("dave", res, "ap-r1", "rows-insert", rev, `{"table":"Entries","rows":[{"Item":"pen","Qty":3},{"Item":"ink","Qty":1}]}`)
		Expect(again.code).To(Equal(200), again.raw)
		Expect(again.body["revision"]).To(Equal(ins.body["revision"]))
		page, _ = h.read("carol", res, `{"tool":"rows-query","table":"Entries"}`)
		Expect(page["rows"]).To(HaveLen(2))
		// The same operation with another request changes nothing.
		other := h.apply("dave", res, "ap-r1", "rows-insert", rev, `{"table":"Entries","rows":[{"Item":"cup","Qty":9}]}`)
		Expect(other.code).To(Equal(http.StatusConflict), other.raw)
		Expect(other.errType()).To(Equal("operation_conflict"))
		page, _ = h.read("carol", res, `{"tool":"rows-query","table":"Entries"}`)
		Expect(page["rows"]).To(HaveLen(2))
		// The receipt names the request it belongs to, so a client can tell
		// the change it asked for from another one under the same ID.
		hash, _ := ins.body["requestHash"].(string)
		Expect(hash).To(MatchRegexp(`^[0-9a-f]{64}$`))
		Expect(again.body["requestHash"]).To(Equal(hash))
		own := h.send("GET", "/api/addons/resources/"+res+"/operations/ap-r1?kind=apply&requestHash="+hash, "", h.tab("dave", res))
		Expect(own.code).To(Equal(200), own.raw)
		Expect(own.body["state"]).To(Equal("committed"))
		Expect(own.body["tool"]).To(Equal("rows-insert"))
		Expect(own.body["requestHash"]).To(Equal(hash))
		foreign := h.send("GET", "/api/addons/resources/"+res+"/operations/ap-r1?kind=apply&requestHash="+strings.Repeat("ab", 32), "", h.tab("dave", res))
		Expect(foreign.code).To(Equal(http.StatusConflict), foreign.raw)
		Expect(foreign.errType()).To(Equal("operation_conflict"))
		Expect(foreign.raw).NotTo(ContainSubstring("committed"))
		bad := h.send("GET", "/api/addons/resources/"+res+"/operations/ap-r1?kind=apply&requestHash=nope", "", h.tab("dave", res))
		Expect(bad.code).To(Equal(http.StatusBadRequest), bad.raw)

		// The receipt is there for its own person, and nobody else's.
		status := h.send("GET", "/api/addons/resources/"+res+"/operations/ap-r1?kind=apply", "", h.tab("dave", res))
		Expect(status.code).To(Equal(200), status.raw)
		Expect(status.body["state"]).To(Equal("committed"))
		Expect(status.body["tool"]).To(Equal("rows-insert"))
		Expect(status.body["requestHash"]).To(Equal(hash))
		for _, user := range []string{"bob", "carol"} {
			s := h.send("GET", "/api/addons/resources/"+res+"/operations/ap-r1?kind=apply", "", h.tab(user, res))
			if user == "carol" {
				Expect(s.code).To(Equal(http.StatusForbidden))
			} else {
				Expect(s.code).To(Equal(200), s.raw)
				Expect(s.body["state"]).NotTo(Equal("committed"))
			}
		}

		// A moved revision is stale and nothing is applied.
		stale := h.apply("bob", res, "ap-r2", "rows-insert", rev, `{"table":"Entries","rows":[{"Item":"late","Qty":1}]}`)
		Expect(stale.code).To(Equal(http.StatusConflict), stale.raw)
		Expect(stale.errType()).To(Equal("stale"))
		page, _ = h.read("bob", res, `{"tool":"rows-query","table":"Entries"}`)
		Expect(page["rows"]).To(HaveLen(2))
		// With the revision it read, it applies.
		fresh := revisionOf(page)
		ids := ins.body["result"].(map[string]any)["ids"].([]any)
		upd := h.apply("bob", res, "ap-r3", "rows-update", fresh, fmt.Sprintf(`{"table":"Entries","ids":[%v],"set":{"Qty":10}}`, ids[0]))
		Expect(upd.code).To(Equal(200), upd.raw)
		del := h.apply("dave", res, "ap-r4", "rows-delete", upd.body["revision"].(map[string]any), fmt.Sprintf(`{"table":"Entries","ids":[%v]}`, ids[1]))
		Expect(del.code).To(Equal(200), del.raw)
		page, _ = h.read("carol", res, `{"tool":"rows-query","table":"Entries"}`)
		Expect(page["rows"]).To(HaveLen(1))

		// A change is not a proposal: nothing waits for a confirmation.
		pending := h.send("GET", "/api/addons/proposals?state=pending", "", h.bearer("bob"))
		Expect(pending.body["proposals"]).To(BeEmpty())
		// The database that the change addressed is the one of the path.
		Expect(inspect("bob")["name"]).To(Equal("Ledger"))
	})

	It("refuses crafted writes by a viewer, an editor's schema change, a revoked editor and an installation administrator", func() {
		h := ready(false)
		res := h.database("bob", "cr-1", "Crafted")
		Expect(h.grant("bob", res, "carol", "viewer").code).To(Equal(200))
		Expect(h.grant("bob", res, "dave", "editor").code).To(Equal(200))
		v, e := h.read("bob", res, `{"tool":"databases-inspect"}`)
		Expect(e).NotTo(HaveOccurred())
		rev := revisionOf(v)
		Expect(h.apply("bob", res, "cr-t", "tables-create", rev, `{"table":"T"}`).code).To(Equal(200))
		v, _ = h.read("bob", res, `{"tool":"databases-inspect"}`)
		rev = revisionOf(v)
		Expect(h.apply("bob", res, "cr-c", "columns-add", rev, `{"table":"T","column":"A","type":"text"}`).code).To(Equal(200))
		v, _ = h.read("bob", res, `{"tool":"databases-inspect"}`)
		rev = revisionOf(v)
		unknown := h.apply("bob", "r0123456789abcdef0123456789abcde", "cr-u", "rows-insert", rev, `{"table":"T","rows":[{"A":"x"}]}`)
		Expect(unknown.code).To(Equal(http.StatusForbidden))

		rows := func() int {
			p, e := h.read("bob", res, `{"tool":"rows-query","table":"T"}`)
			Expect(e).NotTo(HaveOccurred())
			return len(p["rows"].([]any))
		}
		body := `{"table":"T","rows":[{"A":"x"}]}`
		// Viewer, administrator without data privileges and a feature-less
		// account get the answer of an unknown database.
		for _, user := range []string{"carol", "alice"} {
			got := h.apply(user, res, "cr-"+user, "rows-insert", rev, body)
			Expect(got.code).To(Equal(unknown.code), user+" "+got.raw)
			Expect(got.errType()).To(Equal("addon_error"), user)
		}
		Expect(h.apply("erin", res, "cr-erin", "rows-insert", rev, body).code).To(Equal(http.StatusForbidden))
		// An editor changes rows and never the schema.
		sch := h.apply("dave", res, "cr-dave-s", "tables-drop", rev, `{"table":"T"}`)
		Expect(sch.code).To(Equal(unknown.code), sch.raw)
		Expect(h.apply("dave", res, "cr-dave-c", "columns-add", rev, `{"table":"T","column":"B","type":"text"}`).code).To(Equal(unknown.code))
		Expect(rows()).To(Equal(0))

		// Not the person's own tab: a key, a bare session, a tool marker, a
		// confirmation of another resource and a cross-site request.
		marked := func(r *http.Request) { h.tab("bob", res)(r); r.Header.Set("X-LocalAI-Tool-Call", "1") }
		elsewhere := h.tab("bob", "r0123456789abcdef0123456789abcde")
		cross := func(r *http.Request) { h.tab("bob", res)(r); r.Header.Set("Sec-Fetch-Site", "cross-site") }
		for name, header := range map[string]func(*http.Request){"key": h.bearer("bob"), "session": h.session("bob"), "marked": marked, "elsewhere": elsewhere, "cross-site": cross} {
			got := h.applyAs(header, res, "cr-"+name, "rows-insert", rev, body)
			Expect(got.code).To(BeElementOf(http.StatusForbidden, http.StatusBadRequest), name+" "+got.raw)
		}
		Expect(rows()).To(Equal(0))

		// What is not a data or schema change cannot be applied: reads,
		// creations, renames, the admin delete and undeclared tools.
		for _, tool := range []string{"rows-query", "databases-create", "databases-rename", "databases-inspect", "databases-delete", "resource-exists", "nothing"} {
			got := h.apply("bob", res, "cr-tool-"+tool, tool, rev, `{"name":"X","table":"T"}`)
			Expect(got.code).To(BeElementOf(http.StatusBadRequest, http.StatusForbidden), tool+" "+got.raw)
		}
		// A body that names the resource, the tool, the actor or an unknown
		// field never reaches the add-on.
		for _, args := range []string{
			`{"table":"T","rows":[{"A":"x"}],"resourceId":"r0123456789abcdef0123456789abcde"}`,
			`{"table":"T","rows":[{"A":"x"}],"tool":"tables-drop"}`,
			`{"table":"T","rows":[{"A":"x"}],"userId":"alice"}`,
			`{"table":"T","rows":[{"A":"x"}],"extra":1}`,
			`[]`, `{}`,
		} {
			got := h.apply("bob", res, "cr-args", "rows-insert", rev, args)
			Expect(got.code).To(Equal(http.StatusBadRequest), args+" "+got.raw)
		}
		// A stale generation and a missing revision.
		b := fmt.Sprintf(`{"operationId":"cr-gen","generation":9,"revision":{"schema":%v,"data":%v},"tool":"rows-insert","arguments":%s}`, rev["schema"], rev["data"], body)
		got := h.send("POST", "/api/addons/resources/"+res+"/apply", b, h.tab("bob", res))
		Expect(got.code).To(Equal(http.StatusConflict))
		Expect(got.errType()).To(Equal("stale"))
		noRev := h.send("POST", "/api/addons/resources/"+res+"/apply", `{"operationId":"cr-norev","generation":1,"tool":"rows-insert","arguments":`+body+`}`, h.tab("bob", res))
		Expect(noRev.code).To(Equal(http.StatusBadRequest))
		Expect(rows()).To(Equal(0))

		// A revoked editor is refused at the next change, and one that was
		// writing is stopped before the revocation is acknowledged.
		Expect(h.apply("dave", res, "cr-ok", "rows-insert", rev, body).code).To(Equal(200))
		Expect(h.revoke("bob", res, "dave").code).To(Equal(200))
		v, _ = h.read("bob", res, `{"tool":"rows-query","table":"T"}`)
		after := h.apply("dave", res, "cr-late", "rows-insert", revisionOf(v), body)
		Expect(after.code).To(Equal(unknown.code), after.raw)
		Expect(rows()).To(Equal(1))
	})

	It("carries a UI change to the owner from a forwarding frontend and answers as the owner does", func() {
		h := ready(false)
		front := h.frontend()
		res := h.database("bob", "fa-1", "Forwarded")
		Expect(h.grant("bob", res, "dave", "editor").code).To(Equal(200))
		v, _ := h.read("bob", res, `{"tool":"databases-inspect"}`)
		rev := revisionOf(v)
		send := func(header func(*http.Request), op string, rev map[string]any, tool, args string) reply {
			b, _ := json.Marshal(map[string]any{"operationId": op, "generation": 1, "revision": rev, "tool": tool, "arguments": json.RawMessage(args)})
			return serve(front, "POST", "/api/addons/resources/"+res+"/apply", string(b), header)
		}
		made := send(h.tab("bob", res), "fa-t", rev, "tables-create", `{"table":"T"}`)
		Expect(made.code).To(Equal(200), made.raw)
		// Stale and forbidden answers are the owner's.
		stale := send(h.tab("bob", res), "fa-s", rev, "tables-create", `{"table":"U"}`)
		Expect(stale.code).To(Equal(http.StatusConflict))
		Expect(stale.errType()).To(Equal("stale"))
		direct := h.apply("bob", res, "fa-s2", "tables-create", rev, `{"table":"U"}`)
		Expect(direct.code).To(Equal(stale.code))
		Expect(direct.errType()).To(Equal(stale.errType()))
		denied := send(h.tab("dave", res), "fa-d", made.body["revision"].(map[string]any), "tables-drop", `{"table":"T"}`)
		Expect(denied.code).To(Equal(http.StatusForbidden))
		Expect(send(h.bearer("bob"), "fa-k", rev, "tables-create", `{"table":"V"}`).code).To(Equal(http.StatusForbidden))
		// A frontend whose owner cannot be reached reports the outcome of a
		// change as unknown, with the original operation ID.
		dead := h.deadFrontend()
		b, _ := json.Marshal(map[string]any{"operationId": "fa-dead", "generation": 1, "revision": made.body["revision"], "tool": "tables-create", "arguments": json.RawMessage(`{"table":"W"}`)})
		got := serve(dead, "POST", "/api/addons/resources/"+res+"/apply", string(b), h.tab("bob", res))
		Expect(got.code).To(Equal(http.StatusServiceUnavailable))
		Expect(got.errType()).To(Equal("outcome_unknown"))
		Expect(got.errOperation()).To(Equal("fa-dead"))
	})
})
