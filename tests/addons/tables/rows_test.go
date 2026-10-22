// SPDX-License-Identifier: MIT
package tables_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// rowEnv is an envelope under the host's read or write action.
func (r *rig) rowEnv(inst addonruntime.Instance, actor contract.Actor, scope, action, op, resource string, a map[string]any) contract.Envelope {
	e := r.envelope(inst, actor, scope, op, resource, args(a))
	e.Request.Action = action
	return e
}

func (r *rig) rowsRead(inst addonruntime.Instance, actor contract.Actor, resource, op string, a map[string]any) (contract.Result, error) {
	return inst.Client.Read(context.Background(), r.rowEnv(inst, actor, "read", "read", op, resource, a))
}

// rowsWrite previews and applies one row mutation as the host's broker does.
func (r *rig) rowsWrite(inst addonruntime.Instance, actor contract.Actor, resource, op string, a map[string]any) (contract.Preview, contract.Receipt, error) {
	ctx := context.Background()
	p, e := inst.Client.Preview(ctx, r.rowEnv(inst, actor, "preview", "write", op, resource, a))
	if e != nil {
		return p, contract.Receipt{}, e
	}
	rc, e := inst.Client.Apply(ctx, r.rowEnv(inst, actor, "apply", "write", op, resource, a), p.Revision)
	return p, rc, e
}

type page struct {
	Columns []string `json:"columns"`
	Rows    []struct {
		ID     int64 `json:"id"`
		Values []any `json:"values"`
	} `json:"rows"`
	HasMore  bool              `json:"hasMore"`
	Cursor   string            `json:"cursor"`
	Revision contract.Revision `json:"revision"`
}

func asPage(res contract.Result) page {
	GinkgoHelper()
	var p page
	Expect(json.Unmarshal(res.Payload, &p)).To(Succeed())
	return p
}

var _ = Describe("Tables rows under the real owner", func() {
	ctx := context.Background()
	alice, bob := user("alice"), user("bob")

	setup := func() (*rig, addonruntime.Instance, string, policy.ResourceKey) {
		GinkgoHelper()
		r := newRig()
		inst := r.start(1)
		key, e := r.create(inst, alice, "Sales")
		Expect(e).NotTo(HaveOccurred())
		_, _, e = r.schemaCall(inst, alice, key.ResourceID, "s-t", map[string]any{"tool": "tables-create", "table": "Orders"})
		Expect(e).NotTo(HaveOccurred())
		for i, c := range [][2]string{{"Item", "text"}, {"Qty", "integer"}} {
			_, rc, e := r.schemaCall(inst, alice, key.ResourceID, fmt.Sprint("s-c", i), map[string]any{"tool": "columns-add", "table": "Orders", "column": c[0], "type": c[1]})
			Expect(e).NotTo(HaveOccurred())
			Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		}
		return r, inst, key.ResourceID, key
	}

	It("reads and mutates rows with the exact preview, refuses wrong actions and keeps receipts across a restart", func() {
		r, inst, id, key := setup()
		p, rc, e := r.rowsWrite(inst, alice, id, "ins-1", map[string]any{"tool": "rows-insert", "table": "Orders",
			"rows": []map[string]any{{"Item": "pen", "Qty": 3}, {"Item": "ink", "Qty": 9}, {"Item": "pad"}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(rc.Revision.Data).To(Equal(p.Revision.Data + 1))
		Expect(rc.Revision.Schema).To(Equal(p.Revision.Schema))
		var shape struct {
			Count  int              `json:"count"`
			Sample []map[string]any `json:"sample"`
		}
		Expect(json.Unmarshal(p.Summary, &shape)).To(Succeed())
		Expect(shape.Count).To(Equal(3))

		res, e := r.rowsRead(inst, alice, id, "q-1", map[string]any{"tool": "rows-query", "table": "orders", "columns": []string{"Item"},
			"filter": map[string]any{"column": "Qty", "op": "gt", "value": 2}, "sort": []map[string]any{{"column": "Qty", "desc": true}}})
		Expect(e).NotTo(HaveOccurred())
		got := asPage(res)
		Expect(got.Columns).To(Equal([]string{"Item"}))
		Expect(got.Rows).To(HaveLen(2))
		Expect(got.Rows[0].Values).To(Equal([]any{"ink"}))
		agg, e := r.rowsRead(inst, alice, id, "q-2", map[string]any{"tool": "rows-aggregate", "table": "Orders",
			"aggregates": []map[string]any{{"fn": "sum", "column": "Qty"}, {"fn": "count"}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(string(agg.Payload)).To(ContainSubstring(`"values":[12,3]`))

		// A filter update shows its exact set; a delete by id removes one row.
		pv, rc, e := r.rowsWrite(inst, alice, id, "upd-1", map[string]any{"tool": "rows-update", "table": "Orders",
			"filter": map[string]any{"column": "Qty", "op": "is-null"}, "set": map[string]any{"Qty": 1}})
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(json.Unmarshal(pv.Summary, &shape)).To(Succeed())
		Expect(shape.Count).To(Equal(1))
		_, rc, e = r.rowsWrite(inst, alice, id, "del-1", map[string]any{"tool": "rows-delete", "table": "Orders", "ids": []int64{1}})
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))

		// Roles: a viewer reads, an editor writes, neither changes the schema.
		Expect(r.policy.SetGrant(ctx, alice, key, "bob", policy.RoleViewer)).To(Succeed())
		Expect(r.policy.Authorize(ctx, bob, key, policy.ActionRead)).To(Succeed())
		Expect(r.policy.Authorize(ctx, bob, key, policy.ActionWrite)).To(MatchError(policy.ErrForbidden))
		Expect(r.policy.SetGrant(ctx, alice, key, "bob", policy.RoleEditor)).To(Succeed())
		Expect(r.policy.Authorize(ctx, bob, key, policy.ActionWrite)).To(Succeed())
		Expect(r.policy.Authorize(ctx, bob, key, policy.ActionSchema)).To(MatchError(policy.ErrForbidden))

		// The addon refuses wrong actions on its own, whatever the host did.
		del := map[string]any{"tool": "rows-delete", "table": "Orders", "ids": []int64{2}}
		before, _ := r.rowsRead(inst, alice, id, "q-3", map[string]any{"tool": "rows-query", "table": "Orders"})
		for _, act := range []string{"schema", "read", "share", "delete", "unknown", ""} {
			env := r.rowEnv(inst, bob, "preview", act, "wrong-"+act, id, del)
			_, e = inst.Client.Preview(ctx, env)
			Expect(e).To(HaveOccurred(), act)
			env = r.rowEnv(inst, bob, "apply", act, "wrong-"+act, id, del)
			_, e = inst.Client.Apply(ctx, env, p.Revision)
			Expect(e).To(HaveOccurred(), act)
		}
		_, e = inst.Client.Apply(ctx, r.rowEnv(inst, contract.Actor{}, "apply", "write", "wrong-noactor", id, del), p.Revision)
		Expect(e).To(HaveOccurred())
		// A row read under the write or schema action, and a mutation tool as a read.
		for _, act := range []string{"write", "schema", "share"} {
			_, e = inst.Client.Read(ctx, r.rowEnv(inst, bob, "read", act, "rd-"+act, id, map[string]any{"tool": "rows-query", "table": "Orders"}))
			Expect(e).To(HaveOccurred(), act)
		}
		_, e = r.rowsRead(inst, bob, id, "rd-mut", del)
		Expect(e).To(HaveOccurred())
		// A schema tool under the write action is refused too.
		env := r.rowEnv(inst, bob, "preview", "write", "sch-as-write", id, map[string]any{"tool": "tables-drop", "table": "Orders"})
		_, e = inst.Client.Preview(ctx, env)
		Expect(e).To(HaveOccurred())
		// Unknown argument fields are refused, so a typo cannot widen a mutation.
		_, e = inst.Client.Preview(ctx, r.rowEnv(inst, bob, "preview", "write", "typo", id, map[string]any{"tool": "rows-delete", "table": "Orders", "filters": map[string]any{"column": "Qty", "op": "eq", "value": 1}}))
		Expect(e).To(HaveOccurred())
		after, _ := r.rowsRead(inst, alice, id, "q-4", map[string]any{"tool": "rows-query", "table": "Orders"})
		b, a := asPage(before), asPage(after)
		Expect(a.Rows).To(Equal(b.Rows))
		rec, e := inst.Client.Receipt(ctx, "wrong-write")
		Expect(e).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptUnknown))

		// Stale: two previews of one revision, the second apply is rejected as stale.
		first := map[string]any{"tool": "rows-update", "table": "Orders", "filter": map[string]any{"column": "Qty", "op": "gte", "value": 0}, "set": map[string]any{"Item": "A"}}
		second := map[string]any{"tool": "rows-update", "table": "Orders", "filter": map[string]any{"column": "Qty", "op": "gte", "value": 0}, "set": map[string]any{"Item": "B"}}
		p1, e := inst.Client.Preview(ctx, r.rowEnv(inst, bob, "preview", "write", "race-1", id, first))
		Expect(e).NotTo(HaveOccurred())
		p2, e := inst.Client.Preview(ctx, r.rowEnv(inst, bob, "preview", "write", "race-2", id, second))
		Expect(e).NotTo(HaveOccurred())
		rc1, e := inst.Client.Apply(ctx, r.rowEnv(inst, bob, "apply", "write", "race-1", id, first), p1.Revision)
		Expect(e).NotTo(HaveOccurred())
		Expect(rc1.State).To(Equal(contract.ReceiptCommitted))
		rc2, e := inst.Client.Apply(ctx, r.rowEnv(inst, bob, "apply", "write", "race-2", id, second), p2.Revision)
		Expect(e).NotTo(HaveOccurred())
		Expect(rc2.State).To(Equal(contract.ReceiptRejected))
		Expect(string(rc2.Result.Payload)).To(ContainSubstring(`"error":"stale"`))
		// A rejected operation has no receipt to replay and its ID is free.
		rec, e = inst.Client.Receipt(ctx, "race-2")
		Expect(e).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptUnknown))

		// A restart keeps the data and the receipt, and a replay is the same receipt.
		Expect(r.sup.Stop(ctx, inst)).To(Succeed())
		inst = r.start(2)
		got2, e := inst.Client.Receipt(ctx, "race-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(got2.State).To(Equal(contract.ReceiptCommitted))
		Expect(got2.Revision).To(Equal(rc1.Revision))
		res, e = r.rowsRead(inst, alice, id, "q-5", map[string]any{"tool": "rows-query", "table": "Orders", "columns": []string{"Item"}})
		Expect(e).NotTo(HaveOccurred())
		for _, row := range asPage(res).Rows {
			Expect(row.Values).To(Equal([]any{"A"}))
		}
	})

	It("declares the row tools and refuses a row tool the manifest does not name", func() {
		r, inst, id, _ := setup()
		regs, e := r.sup.Registry.Snapshot(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(regs[0].Capabilities.Tools).To(ContainElements("rows-query", "rows-aggregate", "rows-insert", "rows-update", "rows-delete"))
		_, _, e = r.rowsWrite(inst, alice, id, "tr-1", map[string]any{"tool": "rows-truncate", "table": "Orders"})
		Expect(e).To(HaveOccurred())
		_, e = r.rowsRead(inst, alice, id, "tr-2", map[string]any{"tool": "rows-sql", "table": "Orders"})
		Expect(e).To(HaveOccurred())
		_, _, e = r.rowsWrite(inst, alice, id, "tr-3", map[string]any{"tool": "rows-delete", "table": "Orders"})
		Expect(e).To(HaveOccurred())
	})
})

var _ = Describe("Tables row proposals in the application bootstrap", Label("application"), func() {
	It("applies row changes only on the proposer's confirmation and rejects a proposal whose database moved", func() {
		h := newHost()
		lib := h.library()
		h.start()
		DeferCleanup(h.stop)
		h.seed(lib)
		ctx := context.Background()

		made := h.chatCreate("alice", "rows-create", "Ledger")
		Expect(made.code).To(Equal(200), "%v", made.body)
		Expect(h.confirm("alice", "rows-create", made.body["digest"].(string)).code).To(Equal(200))
		res := made.body["resourceId"].(string)
		propose := func(user, op, action string, a map[string]any) webReply {
			body, _ := json.Marshal(map[string]any{"addonId": "tables", "resourceId": res, "operationId": op, "generation": 1, "action": action, "arguments": a})
			return h.send("POST", "/api/addons/proposals", string(body), h.bearer(user))
		}
		run := func(user, op, action string, a map[string]any) webReply {
			GinkgoHelper()
			p := propose(user, op, action, a)
			Expect(p.code).To(Equal(200), "%v", p.body)
			done := h.confirm(user, op, p.body["digest"].(string))
			Expect(done.code).To(Equal(200), "%v", done.body)
			Expect(done.body["state"]).To(Equal("applied"), "%v", done.body)
			return done
		}
		run("alice", "ap-t", "schema", map[string]any{"tool": "tables-create", "table": "Entries"})
		run("alice", "ap-c", "schema", map[string]any{"tool": "columns-add", "table": "Entries", "column": "Memo", "type": "text"})
		run("alice", "ap-n", "schema", map[string]any{"tool": "columns-add", "table": "Entries", "column": "Amount", "type": "integer"})

		// An editor is needed for rows; a stranger and a viewer cannot propose.
		Expect(propose("bob", "bob-denied", "write", map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []map[string]any{{"Memo": "x"}}}).code).To(Equal(403))
		key := policy.ResourceKey{AddonID: "tables", ResourceID: res}
		alice := h.actor("alice")
		Expect(h.app.AddonPolicy().SetGrant(ctx, alice, key, "bob", policy.RoleViewer)).To(Succeed())
		Expect(propose("bob", "bob-viewer", "write", map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []map[string]any{{"Memo": "x"}}}).code).To(Equal(403))
		read := func(user string, a map[string]any) (page, error) {
			out, e := h.app.AddonGateway().Dispatch(ctx, h.actor(user), contract.Request{AddonID: "tables", ResourceID: res,
				OperationID: "rd-" + uuid.NewString()[:8], Generation: 1, Action: "read", Arguments: args(a)})
			if e != nil {
				return page{}, e
			}
			return asPage(out), nil
		}
		_, e := read("bob", map[string]any{"tool": "rows-query", "table": "Entries"})
		Expect(e).NotTo(HaveOccurred())
		Expect(h.app.AddonPolicy().SetGrant(ctx, alice, key, "bob", policy.RoleEditor)).To(Succeed())

		// The proposal is a preview until the proposer's own session confirms.
		p := propose("bob", "ins-1", "write", map[string]any{"tool": "rows-insert", "table": "Entries",
			"rows": []map[string]any{{"Memo": "rent", "Amount": 100}, {"Memo": "food", "Amount": 40}}})
		Expect(p.code).To(Equal(200), "%v", p.body)
		Expect(p.body["state"]).To(Equal("pending"))
		pg, e := read("alice", map[string]any{"tool": "rows-query", "table": "Entries"})
		Expect(e).NotTo(HaveOccurred())
		Expect(pg.Rows).To(BeEmpty())
		Expect(h.send("POST", "/api/addons/proposals/ins-1/confirm", fmt.Sprintf(`{"digest":%q}`, p.body["digest"]), h.bearer("bob")).code).To(Equal(403))
		Expect(h.confirm("alice", "ins-1", p.body["digest"].(string)).code).To(Equal(403))
		Expect(h.confirm("bob", "ins-1", p.body["digest"].(string)).code).To(Equal(200))
		pg, e = read("alice", map[string]any{"tool": "rows-query", "table": "Entries", "sort": []map[string]any{{"column": "Amount", "desc": true}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(pg.Rows).To(HaveLen(2))
		Expect(pg.Rows[0].Values).To(Equal([]any{"rent", float64(100)}))

		// Two proposals on one revision: the second no longer applies, and the
		// filter is never re-evaluated against the rows the first one made.
		del := propose("alice", "del-all", "write", map[string]any{"tool": "rows-delete", "table": "Entries",
			"filter": map[string]any{"column": "Amount", "op": "gt", "value": 0}})
		Expect(del.code).To(Equal(200), "%v", del.body)
		ins := propose("bob", "ins-2", "write", map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []map[string]any{{"Memo": "late", "Amount": 5}}})
		Expect(ins.code).To(Equal(200), "%v", ins.body)
		Expect(h.confirm("bob", "ins-2", ins.body["digest"].(string)).code).To(Equal(200))
		stale := h.confirm("alice", "del-all", del.body["digest"].(string))
		Expect(stale.body["state"]).NotTo(Equal("applied"), "%v", stale.body)
		pg, e = read("alice", map[string]any{"tool": "rows-query", "table": "Entries"})
		Expect(e).NotTo(HaveOccurred())
		Expect(pg.Rows).To(HaveLen(3))
		_ = time.Second
	})
})
