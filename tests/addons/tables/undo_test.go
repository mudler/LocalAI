// SPDX-License-Identifier: MIT
package tables_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mudler/LocalAI/core/services/addons/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The undo of a change a chat made at once, through the real host, the real
// owner and the real Tables executable.
var _ = Describe("Tables undo of chat changes", Label("application"), func() {
	ctx := context.Background()

	type chatRig struct {
		h    *host
		call func(tool, body string) tools.ToolResult
		res  string
	}
	setup := func() *chatRig {
		GinkgoHelper()
		h := newHost()
		lib := h.library()
		h.start()
		DeferCleanup(h.stop)
		h.seed(lib)
		chat := h.app.StudioAddonTools()
		Expect(chat).NotTo(BeNil())
		r := &chatRig{h: h}
		r.call = func(tool, body string) tools.ToolResult {
			GinkgoHelper()
			text, e := chat.Call(ctx, "alice", "conv1", "addon__tables__"+tool, json.RawMessage(body))
			Expect(e).NotTo(HaveOccurred(), tool)
			var out tools.ToolResult
			Expect(json.Unmarshal([]byte(text), &out)).To(Succeed(), text)
			return out
		}
		made := r.call("databases-create", `{"name":"Ledger"}`)
		Expect(made.Kind).To(Equal(tools.ResultApplied))
		r.res = made.Applied.ResourceID
		return r
	}
	step := func(r *chatRig, tool, body string) *tools.AppliedRef {
		GinkgoHelper()
		out := r.call(tool, fmt.Sprintf(`{"resourceId":%q,%s`, r.res, body))
		Expect(out.Kind).To(Equal(tools.ResultApplied), tool)
		return out.Applied
	}
	undo := func(r *chatRig, user, change, op string, headers func(*http.Request)) webReply {
		GinkgoHelper()
		if headers == nil {
			headers = r.h.humanTab(user, r.res)
		}
		return r.h.send("POST", "/api/addons/resources/"+r.res+"/changes/"+change+"/undo", fmt.Sprintf(`{"operationId":%q,"generation":1}`, op), headers)
	}
	rowCount := func(r *chatRig) int {
		GinkgoHelper()
		out := r.call("rows-query", fmt.Sprintf(`{"resourceId":%q,"table":"Entries"}`, r.res))
		var p page
		Expect(json.Unmarshal(out.Result, &p)).To(Succeed())
		return len(p.Rows)
	}
	build := func(r *chatRig) {
		GinkgoHelper()
		step(r, "tables-create", `"table":"Entries"}`)
		step(r, "columns-add", `"table":"Entries","column":"Memo","type":"text"}`)
	}

	It("tells the model and the chat that a data change can be undone, and undoes it from the person's own tab", func() {
		r := setup()
		build(r)
		ins := step(r, "rows-insert", `"table":"Entries","rows":[{"Memo":"rent"},{"Memo":"food"}]}`)
		Expect(ins.Undoable).To(BeTrue())
		Expect(ins.ChangeID).NotTo(BeEmpty())
		Expect(ins.Undo).To(ContainSubstring("Delete the 2 rows"))
		Expect(rowCount(r)).To(Equal(2))

		got := undo(r, "alice", ins.ChangeID, "undo-1", nil)
		Expect(got.code).To(Equal(200), "%v", got.body)
		Expect(got.body["changeId"]).To(Equal(ins.ChangeID))
		Expect(rowCount(r)).To(Equal(0))
		// The same operation ID is the same undo; another ID finds it done.
		again := undo(r, "alice", ins.ChangeID, "undo-1", nil)
		Expect(again.code).To(Equal(200), "%v", again.body)
		Expect(again.body["revision"]).To(Equal(got.body["revision"]))
		done := undo(r, "alice", ins.ChangeID, "undo-2", nil)
		Expect(done.code).To(Equal(409))
		Expect(done.body["error"].(map[string]any)["type"]).To(Equal("already_undone"))
		// After an unknown outcome the person looks the receipt up by kind=undo.
		status := r.h.send("GET", "/api/addons/resources/"+r.res+"/operations/undo-1?kind=undo", "", r.h.humanTab("alice", r.res))
		Expect(status.code).To(Equal(200), "%v", status.body)
		Expect(status.body["state"]).To(Equal("committed"))
	})

	It("never lets a key, a tool call, another session or a stranger undo, so a model cannot undo its own work", func() {
		r := setup()
		build(r)
		ins := step(r, "rows-insert", `"table":"Entries","rows":[{"Memo":"rent"}]}`)
		Expect(undo(r, "alice", ins.ChangeID, "undo-k", r.h.bearer("alice")).code).To(Equal(403))
		toolCall := func(q *http.Request) { r.h.humanTab("alice", r.res)(q); q.Header.Set("X-LocalAI-Tool-Call", "1") }
		Expect(undo(r, "alice", ins.ChangeID, "undo-t", toolCall).code).To(Equal(403))
		wrongHeader := func(q *http.Request) { r.h.humanTab("alice", "someone-else")(q) }
		Expect(undo(r, "alice", ins.ChangeID, "undo-h", wrongHeader).code).To(Equal(403))
		Expect(undo(r, "bob", ins.ChangeID, "undo-b", r.h.humanTab("bob", r.res)).code).To(Equal(403), "a user with no access to the resource")
		Expect(rowCount(r)).To(Equal(1))
		// The model has no tool for it either.
		defs, e := tools.NewChatToolAdapter(r.h.app.AddonGateway()).Discover(ctx, r.h.actor("alice"))
		Expect(e).NotTo(HaveOccurred())
		for _, d := range defs {
			Expect(d.Name).NotTo(ContainSubstring("undo"))
			Expect(d.Name).NotTo(ContainSubstring("changes-"))
		}
		_, e = tools.NewChatToolAdapter(r.h.app.AddonGateway()).ExecuteIn(ctx, r.h.actor("alice"), "conv1", "addon__tables__changes-undo", json.RawMessage(fmt.Sprintf(`{"resourceId":%q,"change":%q}`, r.res, ins.ChangeID)))
		Expect(e).To(HaveOccurred())
		Expect(rowCount(r)).To(Equal(1))
	})

	It("refuses a stale undo with what changed, and unwinds from the newest change to the oldest", func() {
		r := setup()
		build(r)
		a := step(r, "rows-insert", `"table":"Entries","rows":[{"Memo":"a"}]}`)
		b := step(r, "rows-insert", `"table":"Entries","rows":[{"Memo":"b"}]}`)
		stale := undo(r, "alice", a.ChangeID, "undo-a1", nil)
		Expect(stale.code).To(Equal(409))
		e := stale.body["error"].(map[string]any)
		Expect(e["type"]).To(Equal("stale"))
		Expect(e["message"]).To(ContainSubstring("inserted"))
		Expect(rowCount(r)).To(Equal(2), "nothing was overwritten")
		Expect(undo(r, "alice", b.ChangeID, "undo-b", nil).code).To(Equal(200))
		Expect(undo(r, "alice", a.ChangeID, "undo-a2", nil).code).To(Equal(200))
		Expect(rowCount(r)).To(Equal(0))
	})

	It("answers not_found for a change nobody recorded", func() {
		r := setup()
		got := undo(r, "alice", "never-made", "undo-x", nil)
		Expect(got.code).To(Equal(404), "%v", got.body)
		Expect(got.body["error"].(map[string]any)["type"]).To(Equal("not_found"))
	})

	It("restores a dropped column with its data and puts a dropped table back", func() {
		r := setup()
		build(r)
		step(r, "rows-insert", `"table":"Entries","rows":[{"Memo":"rent"},{"Memo":"food"}]}`)
		drop := step(r, "columns-drop", `"table":"Entries","column":"Memo"}`)
		Expect(drop.Undoable).To(BeTrue(), "%+v", drop)
		Expect(drop.Undo).To(ContainSubstring("Memo"))
		Expect(undo(r, "alice", drop.ChangeID, "undo-col", nil).code).To(Equal(200))
		out := r.call("rows-query", fmt.Sprintf(`{"resourceId":%q,"table":"Entries","columns":["Memo"]}`, r.res))
		Expect(string(out.Result)).To(ContainSubstring("rent"))
		Expect(string(out.Result)).To(ContainSubstring("food"))
		table := step(r, "tables-drop", `"table":"Entries"}`)
		Expect(table.Undoable).To(BeTrue())
		Expect(undo(r, "alice", table.ChangeID, "undo-table", nil).code).To(Equal(200))
		Expect(rowCount(r)).To(Equal(2))
	})

	It("undoes the creation of a database only while nothing touched it, and deletes it then", func() {
		r := setup()
		made := r.call("databases-create", `{"name":"Scratch"}`)
		Expect(made.Applied.Undoable).To(BeTrue(), "%+v", made.Applied)
		scratch := made.Applied.ResourceID
		got := r.h.send("POST", "/api/addons/resources/"+scratch+"/changes/"+made.Applied.ChangeID+"/undo", `{"operationId":"undo-create","generation":1}`, r.h.humanTab("alice", scratch))
		Expect(got.code).To(Equal(200), "%v", got.body)
		Expect(got.body["deleted"]).To(BeTrue())
		Expect(accessibleTo(r.h, "alice")).NotTo(ContainElement(scratch))
		// The one that got a table is stale.
		Expect(r.call("tables-create", fmt.Sprintf(`{"resourceId":%q,"table":"Entries"}`, r.res)).Kind).To(Equal(tools.ResultApplied))
		old := r.h.send("POST", "/api/addons/resources/"+r.res+"/changes/"+"unknown-creation"+"/undo", `{"operationId":"undo-create-2","generation":1}`, r.h.humanTab("alice", r.res))
		Expect(old.code).To(Equal(404))
	})

	It("lets an editor undo a data change but not a schema change", func() {
		r := setup()
		build(r)
		ins := step(r, "rows-insert", `"table":"Entries","rows":[{"Memo":"rent"}]}`)
		grant := r.h.send("PUT", "/api/addons/resources/"+r.res+"/grants/bob", `{"role":"editor"}`, r.h.humanTab("alice", r.res))
		Expect(grant.code).To(Equal(200), "%v", grant.body)
		sch := step(r, "columns-add", `"table":"Entries","column":"Tag","type":"text"}`)
		// The data change is stale now (a schema change came after it), the
		// schema change is current: the editor is refused it by role.
		Expect(undo(r, "bob", sch.ChangeID, "undo-e1", r.h.humanTab("bob", r.res)).code).To(Equal(403))
		Expect(undo(r, "alice", sch.ChangeID, "undo-o1", nil).code).To(Equal(200))
		Expect(undo(r, "bob", ins.ChangeID, "undo-e2", r.h.humanTab("bob", r.res)).code).To(Equal(200))
		Expect(rowCount(r)).To(Equal(0))
	})
})
