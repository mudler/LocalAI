// SPDX-License-Identifier: MIT
package tables_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// schemaCall sends one schema preview and apply through the real runtime
// transport, as the owner's broker does after it admitted an owner.
func (r *rig) schemaCall(inst addonruntime.Instance, actor contract.Actor, resource, op string, a map[string]any) (contract.Preview, contract.Receipt, error) {
	body := args(a)
	p, e := inst.Client.Preview(context.Background(), r.envelope(inst, actor, "preview", op, resource, body))
	if e != nil {
		return p, contract.Receipt{}, e
	}
	rc, e := inst.Client.Apply(context.Background(), r.envelope(inst, actor, "apply", op, resource, body), p.Revision)
	return p, rc, e
}

var _ = Describe("Tables schema under the real owner", func() {
	ctx := context.Background()
	alice, bob := user("alice"), user("bob")

	It("changes the schema as the owner, shows data loss in the preview and refuses an editor at the host and the addon", func() {
		r := newRig()
		inst := r.start(1)
		key, e := r.create(inst, alice, "Sales")
		Expect(e).NotTo(HaveOccurred())
		id := key.ResourceID

		p, rc, e := r.schemaCall(inst, alice, id, "tbl-create-1", map[string]any{"tool": "tables-create", "table": "Orders"})
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(rc.Revision.Schema).To(Equal(p.Revision.Schema + 1))
		var shape struct {
			Count  int              `json:"count"`
			Sample []map[string]any `json:"sample"`
		}
		Expect(json.Unmarshal(p.Summary, &shape)).To(Succeed())
		Expect(shape.Count).To(Equal(1))
		Expect(shape.Sample[0]["action"]).To(Equal("create table"))

		_, rc, e = r.schemaCall(inst, alice, id, "col-add-1", map[string]any{"tool": "columns-add", "table": "orders", "column": "Total", "type": "real", "required": true, "default": 0})
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		res, e := r.read(inst, alice, id, "databases-inspect")
		Expect(e).NotTo(HaveOccurred())
		var s struct {
			Tables []string `json:"tables"`
			Detail []struct {
				Name    string `json:"name"`
				Columns []struct {
					Name, Type string
					Required   bool
				} `json:"columns"`
			} `json:"detail"`
		}
		Expect(json.Unmarshal(res.Payload, &s)).To(Succeed())
		Expect(s.Tables).To(Equal([]string{"Orders"}))
		Expect(s.Detail[0].Columns).To(HaveLen(1))
		Expect(s.Detail[0].Columns[0].Name).To(Equal("Total"))
		Expect(s.Detail[0].Columns[0].Type).To(Equal("real"))
		Expect(s.Detail[0].Columns[0].Required).To(BeTrue())

		// Rows exist: dropping the column reports the data it would destroy.
		raw, e := sql.Open("sqlite3", r.dbFile(id))
		Expect(e).NotTo(HaveOccurred())
		defer func() { _ = raw.Close() }()
		var tableID, colID string
		Expect(raw.QueryRow("SELECT table_id FROM tables_catalog_tables").Scan(&tableID)).To(Succeed())
		Expect(raw.QueryRow("SELECT column_id FROM tables_catalog_columns").Scan(&colID)).To(Succeed())
		_, e = raw.Exec("INSERT INTO " + tableID + " DEFAULT VALUES")
		Expect(e).NotTo(HaveOccurred())
		for i := 0; i < 3; i++ {
			_, e = raw.Exec("INSERT INTO "+tableID+"("+colID+") VALUES(?)", float64(i))
			Expect(e).NotTo(HaveOccurred())
		}
		dropArgs := map[string]any{"tool": "columns-drop", "table": "Orders", "column": "Total"}
		p, e = inst.Client.Preview(ctx, r.envelope(inst, alice, "preview", "col-drop-1", id, args(dropArgs)))
		Expect(e).NotTo(HaveOccurred())
		var loss struct {
			DataLoss map[string]float64 `json:"dataLoss"`
		}
		Expect(json.Unmarshal(p.Summary, &loss)).To(Succeed())
		// The row made by DEFAULT VALUES holds the default 0, so four rows hold a value.
		Expect(loss.DataLoss["rows"]).To(BeEquivalentTo(4))

		// An editor: the host refuses the action before any call is sent.
		Expect(r.policy.SetGrant(ctx, alice, key, "bob", policy.RoleEditor)).To(Succeed())
		Expect(r.policy.Authorize(ctx, bob, key, policy.ActionSchema)).To(MatchError(policy.ErrForbidden))
		Expect(r.policy.Authorize(ctx, bob, key, policy.ActionWrite)).To(Succeed())
		// Direct runtime transport: a schema tool under a row action, or
		// without an actor, is refused by the addon itself and changes nothing.
		before, _ := r.read(inst, alice, id, "databases-inspect")
		for _, act := range []string{"write", "read", "share", "delete", "unknown", ""} {
			env := r.envelope(inst, bob, "preview", "direct-"+act, id, args(dropArgs))
			env.Request.Action = act
			_, e = inst.Client.Preview(ctx, env)
			Expect(e).To(HaveOccurred(), act)
			env = r.envelope(inst, bob, "apply", "direct-"+act, id, args(dropArgs))
			env.Request.Action = act
			_, e = inst.Client.Apply(ctx, env, p.Revision)
			if e == nil {
				Fail("apply under action " + act + " must fail")
			}
		}
		noActor := r.envelope(inst, contract.Actor{}, "apply", "direct-noactor", id, args(dropArgs))
		_, e = inst.Client.Apply(ctx, noActor, p.Revision)
		Expect(e).To(HaveOccurred())
		after, _ := r.read(inst, alice, id, "databases-inspect")
		Expect(after.Payload).To(MatchJSON(before.Payload))
		rec, e := inst.Client.Receipt(ctx, "direct-write")
		Expect(e).NotTo(HaveOccurred())
		Expect(rec.State).To(Equal(contract.ReceiptUnknown))
	})

	It("rejects a stale schema revision, replays an operation and keeps receipts across a restart", func() {
		r := newRig()
		inst := r.start(1)
		key, e := r.create(inst, alice, "Sales")
		Expect(e).NotTo(HaveOccurred())
		id := key.ResourceID
		a := args(map[string]any{"tool": "tables-create", "table": "A"})
		stale, e := inst.Client.Preview(ctx, r.envelope(inst, alice, "preview", "sch-a", id, a))
		Expect(e).NotTo(HaveOccurred())
		_, rc, e := r.schemaCall(inst, alice, id, "sch-b", map[string]any{"tool": "tables-create", "table": "B"})
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		// The preview of "A" was made before "B" changed the schema.
		rj, e := inst.Client.Apply(ctx, r.envelope(inst, alice, "apply", "sch-a", id, a), stale.Revision)
		Expect(e).NotTo(HaveOccurred())
		Expect(rj.State).To(Equal(contract.ReceiptRejected))
		var body struct {
			Error    string            `json:"error"`
			Revision contract.Revision `json:"revision"`
		}
		Expect(json.Unmarshal(rj.Result.Payload, &body)).To(Succeed())
		Expect(body.Error).To(Equal("stale"))
		Expect(body.Revision).To(Equal(rc.Revision))
		res, _ := r.read(inst, alice, id, "databases-inspect")
		Expect(string(res.Payload)).NotTo(ContainSubstring(`"A"`))
		// The operation replays to one receipt and the same ID with another
		// request is a conflict at the transport.
		_, again, e := r.schemaCall(inst, alice, id, "sch-b", map[string]any{"tool": "tables-create", "table": "B"})
		Expect(e).NotTo(HaveOccurred())
		Expect(again).To(Equal(rc))
		_, _, e = r.schemaCall(inst, alice, id, "sch-b", map[string]any{"tool": "tables-create", "table": "C"})
		Expect(e).To(HaveOccurred())
		// A restart keeps the receipt.
		Expect(r.sup.Stop(ctx, inst)).To(Succeed())
		inst = r.start(2)
		got, e := inst.Client.Receipt(ctx, "sch-b")
		Expect(e).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(contract.ReceiptCommitted))
		Expect(got.Revision).To(Equal(rc.Revision))
		Expect(errors.Is(e, context.Canceled)).To(BeFalse())
	})

	It("declares the schema tools and refuses any schema tool the manifest does not name", func() {
		r := newRig()
		inst := r.start(1)
		key, e := r.create(inst, alice, "Sales")
		Expect(e).NotTo(HaveOccurred())
		regs, e := r.sup.Registry.Snapshot(ctx)
		Expect(e).NotTo(HaveOccurred())
		Expect(regs[0].Capabilities.Tools).To(ContainElements("tables-create", "tables-rename", "tables-drop", "columns-add", "columns-rename", "columns-drop"))
		_, _, e = r.schemaCall(inst, alice, key.ResourceID, "sch-x", map[string]any{"tool": "columns-retype", "table": "A", "column": "b", "type": "integer"})
		Expect(e).To(HaveOccurred())
		_, _, e = r.schemaCall(inst, alice, key.ResourceID, "sch-y", map[string]any{"tool": "tables-create", "table": "bad‮name"})
		Expect(e).To(HaveOccurred())
	})
})
