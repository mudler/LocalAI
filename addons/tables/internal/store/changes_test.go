// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// clock is a settable time source for the retention specs.
type clock struct{ t atomic.Int64 }

func (c *clock) now() time.Time          { return time.Unix(0, c.t.Load()).UTC() }
func (c *clock) advance(d time.Duration) { c.t.Add(int64(d)) }

func noteOf(rc contract.Receipt) contract.UndoNote {
	GinkgoHelper()
	n, ok := contract.ParseUndoNote(rc.Result.Payload)
	Expect(ok).To(BeTrue(), "the receipt of a change carries an undo note: %s", rc.Result.Payload)
	return n
}

var _ = Describe("Tables change sets and undo", func() {
	ctx := context.Background()

	undo := func(r *rowsRig, change, op string) (contract.Receipt, error) {
		info, e := r.s.Change(ctx, idA, change)
		Expect(e).NotTo(HaveOccurred())
		rc, e := r.s.UndoChange(ctx, idA, change, op, info.Current)
		if e == nil {
			r.rev = rc.Revision
		}
		return rc, e
	}
	names := func(r *rowsRig) []string { return r.names(store.Query{Sort: []store.SortKey{{Column: "_id"}}}) }

	Describe("rows", func() {
		It("records an inverse for an insert and removes exactly those rows on undo", func() {
			r := newRowsRig(store.Options{})
			r.insert(map[string]any{"Name": "keep"})
			rc, e := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "a"}, map[string]any{"Name": "b"})})
			Expect(e).NotTo(HaveOccurred())
			n := noteOf(rc)
			Expect(n.Available).To(BeTrue())
			Expect(n.ID).To(Equal(rc.OperationID))
			Expect(n.Summary).To(ContainSubstring("2"))
			Expect(names(r)).To(Equal([]string{"keep", "a", "b"}))
			u, e := undo(r, rc.OperationID, "undo-1")
			Expect(e).NotTo(HaveOccurred())
			Expect(u.State).To(Equal(contract.ReceiptCommitted))
			Expect(u.Revision.Data).To(BeNumerically(">", rc.Revision.Data))
			Expect(names(r)).To(Equal([]string{"keep"}))
		})

		It("restores the previous values of an update, for the columns it set only", func() {
			r := newRowsRig(store.Options{})
			r.insert(map[string]any{"Name": "a", "Age": 10, "Tag": "x"}, map[string]any{"Name": "b", "Age": 20, "Tag": "y"})
			rc, e := r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, Set: setOf(map[string]any{"Age": 99}), Filter: &store.Filter{Column: "Age", Op: "gte", Value: jv(0)}})
			Expect(e).NotTo(HaveOccurred())
			Expect(noteOf(rc).Available).To(BeTrue())
			_, e = undo(r, rc.OperationID, "undo-1")
			Expect(e).NotTo(HaveOccurred())
			p := r.query(store.Query{Columns: []string{"Name", "Age", "Tag"}, Sort: []store.SortKey{{Column: "_id"}}})
			Expect(p.Rows[0].Values).To(Equal([]any{"a", int64(10), "x"}))
			Expect(p.Rows[1].Values).To(Equal([]any{"b", int64(20), "y"}))
		})

		It("brings deleted rows back with their row IDs and every value, nulls included", func() {
			r := newRowsRig(store.Options{})
			ids := r.insert(map[string]any{"Name": "a", "Age": 1, "Score": 1.5, "Active": true, "Born": "2020-01-02T03:04:05Z"}, map[string]any{"Name": "b"}, map[string]any{"Name": "c", "Age": 9007199254740993})
			before := r.query(store.Query{Columns: []string{"Name", "Age", "Score", "Active", "Born", "Tag"}, Sort: []store.SortKey{{Column: "_id"}}})
			rc, e := r.rowsDo(store.RowChange{Kind: store.KindRowDelete, IDs: ids[:2]})
			Expect(e).NotTo(HaveOccurred())
			Expect(names(r)).To(Equal([]string{"c"}))
			_, e = undo(r, rc.OperationID, "undo-1")
			Expect(e).NotTo(HaveOccurred())
			after := r.query(store.Query{Columns: []string{"Name", "Age", "Score", "Active", "Born", "Tag"}, Sort: []store.SortKey{{Column: "_id"}}})
			Expect(after.Rows).To(Equal(before.Rows))
			Expect(after.Rows[0].ID).To(Equal(ids[0]))
		})

		It("is idempotent by operation ID: the same ID returns the same receipt and undoes once", func() {
			r := newRowsRig(store.Options{})
			rc, _ := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "a"})})
			first, e := undo(r, rc.OperationID, "undo-1")
			Expect(e).NotTo(HaveOccurred())
			again, e := r.s.UndoChange(ctx, idA, rc.OperationID, "undo-1", first.Revision)
			Expect(e).NotTo(HaveOccurred())
			Expect(again).To(Equal(first))
			Expect(names(r)).To(BeEmpty())
			_, e = r.s.UndoChange(ctx, idA, rc.OperationID, "undo-2", first.Revision)
			Expect(e).To(MatchError(store.ErrChangeUndone))
		})

		It("refuses the same operation ID for another change", func() {
			r := newRowsRig(store.Options{})
			a, _ := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "a"})})
			u, e := undo(r, a.OperationID, "undo-1")
			Expect(e).NotTo(HaveOccurred())
			b, _ := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "b"})})
			_, e = r.s.UndoChange(ctx, idA, b.OperationID, "undo-1", u.Revision)
			Expect(e).To(MatchError(store.ErrConflict))
		})

		It("refuses a stale undo with what changed and overwrites nothing", func() {
			r := newRowsRig(store.Options{})
			rc, _ := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "a"})})
			r.insert(map[string]any{"Name": "later"})
			info, e := r.s.Change(ctx, idA, rc.OperationID)
			Expect(e).NotTo(HaveOccurred())
			Expect(info.State).To(Equal(contract.ChangeStale))
			Expect(info.Detail).To(ContainSubstring("People"))
			_, e = r.s.UndoChange(ctx, idA, rc.OperationID, "undo-1", info.Current)
			var stale *store.ChangeStaleError
			Expect(errors.As(e, &stale)).To(BeTrue())
			Expect(stale.Detail).To(ContainSubstring("inserted"))
			Expect(names(r)).To(Equal([]string{"a", "later"}))
		})

		It("refuses an undo whose expected revision moved between the read and the apply", func() {
			r := newRowsRig(store.Options{})
			rc, _ := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "a"})})
			info, _ := r.s.Change(ctx, idA, rc.OperationID)
			r.insert(map[string]any{"Name": "sneaky"})
			_, e := r.s.UndoChange(ctx, idA, rc.OperationID, "undo-1", info.Current)
			var stale *store.ChangeStaleError
			Expect(errors.As(e, &stale)).To(BeTrue())
			Expect(names(r)).To(Equal([]string{"a", "sneaky"}))
		})

		It("unwinds in order: undoing the latest change makes the earlier one undoable again", func() {
			r := newRowsRig(store.Options{})
			a, _ := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "a"})})
			b, _ := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "b"})})
			ia, _ := r.s.Change(ctx, idA, a.OperationID)
			Expect(ia.State).To(Equal(contract.ChangeStale))
			_, e := undo(r, b.OperationID, "undo-b")
			Expect(e).NotTo(HaveOccurred())
			ia, _ = r.s.Change(ctx, idA, a.OperationID)
			Expect(ia.State).To(Equal(contract.ChangeAvailable))
			_, e = undo(r, a.OperationID, "undo-a")
			Expect(e).NotTo(HaveOccurred())
			Expect(names(r)).To(BeEmpty())
		})

		It("keeps no change set that is too large and says so at apply time", func() {
			r := newRowsRig(store.Options{MaxChangeBytes: 2048})
			big := strings.Repeat("x", 600)
			ids := r.insert(
				map[string]any{"Name": big}, map[string]any{"Name": big}, map[string]any{"Name": big}, map[string]any{"Name": big})
			rc, e := r.rowsDo(store.RowChange{Kind: store.KindRowDelete, IDs: ids})
			Expect(e).NotTo(HaveOccurred())
			n := noteOf(rc)
			Expect(n.Available).To(BeFalse())
			Expect(n.Reason).To(ContainSubstring("too large"))
			_, e = r.s.Change(ctx, idA, rc.OperationID)
			Expect(e).To(MatchError(store.ErrChangeNotFound))
		})

		It("expires a change set after the retention and drops what it held", func() {
			c := &clock{}
			c.t.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
			r := newRowsRig(store.Options{ChangeRetention: time.Hour, Now: c.now})
			rc, _ := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "a"})})
			c.advance(2 * time.Hour)
			info, e := r.s.Change(ctx, idA, rc.OperationID)
			Expect(e).NotTo(HaveOccurred())
			Expect(info.State).To(Equal(contract.ChangeExpired))
			_, e = r.s.UndoChange(ctx, idA, rc.OperationID, "undo-1", info.Current)
			Expect(e).To(MatchError(store.ErrChangeExpired))
			Expect(names(r)).To(Equal([]string{"a"}))
		})

		It("keeps the total of change sets of one database bounded by dropping the oldest", func() {
			r := newRowsRig(store.Options{MaxChangeBytes: 4096, MaxChangesTotal: 1200})
			big := strings.Repeat("y", 300)
			ids := r.insert(map[string]any{"Name": big}, map[string]any{"Name": big}, map[string]any{"Name": big}, map[string]any{"Name": big}, map[string]any{"Name": big}, map[string]any{"Name": big})
			var first, last contract.Receipt
			for i, id := range ids {
				rc, e := r.rowsDo(store.RowChange{Kind: store.KindRowDelete, IDs: []int64{id}})
				Expect(e).NotTo(HaveOccurred())
				if i == 0 {
					first = rc
				}
				last = rc
			}
			_, e := r.s.Change(ctx, idA, first.OperationID)
			Expect(e).To(MatchError(store.ErrChangeNotFound))
			info, e := r.s.Change(ctx, idA, last.OperationID)
			Expect(e).NotTo(HaveOccurred())
			Expect(info.State).To(Equal(contract.ChangeAvailable))
		})

		It("answers not found for a change that never existed or belongs to a read", func() {
			r := newRowsRig(store.Options{})
			_, e := r.s.Change(ctx, idA, "no-such-change")
			Expect(e).To(MatchError(store.ErrChangeNotFound))
			_, e = r.s.UndoChange(ctx, idA, "no-such-change", "undo-1", r.rev)
			Expect(e).To(MatchError(store.ErrChangeNotFound))
		})
	})

	Describe("schema", func() {
		It("removes a created table again and refuses once it was touched", func() {
			r := newRowsRig(store.Options{})
			rc := r.must(tc("Orders"))
			Expect(noteOf(rc).Available).To(BeTrue())
			_, e := undo(r, rc.OperationID, "undo-1")
			Expect(e).NotTo(HaveOccurred())
			Expect(r.inspect().Tables).To(Equal([]string{"People"}))
			rc2 := r.must(tc("Orders"))
			r.must(ca("Orders", "Item", "text"))
			info, _ := r.s.Change(ctx, idA, rc2.OperationID)
			Expect(info.State).To(Equal(contract.ChangeStale))
		})

		It("renames a table or a column back", func() {
			r := newRowsRig(store.Options{})
			a := r.must(store.SchemaChange{Kind: store.KindTableRename, Table: "People", NewName: "Staff"})
			Expect(r.inspect().Tables).To(Equal([]string{"Staff"}))
			_, e := undo(r, a.OperationID, "undo-1")
			Expect(e).NotTo(HaveOccurred())
			Expect(r.inspect().Tables).To(Equal([]string{"People"}))
			b := r.must(store.SchemaChange{Kind: store.KindColumnRename, Table: "People", Column: "Name", NewName: "Label"})
			_, e = undo(r, b.OperationID, "undo-2")
			Expect(e).NotTo(HaveOccurred())
			Expect(r.columnID("People", "Name")).NotTo(BeEmpty())
		})

		It("removes an added column again, with its unique index", func() {
			r := newRowsRig(store.Options{})
			r.insert(map[string]any{"Name": "a"})
			a := r.must(store.SchemaChange{Kind: store.KindColumnAdd, Table: "People", Column: "Email", Type: "text", Unique: true})
			_, e := undo(r, a.OperationID, "undo-1")
			Expect(e).NotTo(HaveOccurred())
			for _, t := range r.inspect().Detail {
				for _, c := range t.Columns {
					Expect(c.Name).NotTo(Equal("Email"))
				}
			}
			Expect(names(r)).To(Equal([]string{"a"}))
		})

		It("restores a dropped column with its data, its type, required and unique flags", func() {
			r := newRowsRig(store.Options{})
			r.must(store.SchemaChange{Kind: store.KindColumnAdd, Table: "People", Column: "Email", Type: "text", Unique: true, Required: true, Default: jv("none")})
			ids := r.insert(map[string]any{"Name": "a", "Email": "a@x"}, map[string]any{"Name": "b", "Email": "b@x"})
			id := r.columnID("People", "Email")
			d := r.must(store.SchemaChange{Kind: store.KindColumnDrop, Table: "People", Column: "Email"})
			n := noteOf(d)
			Expect(n.Available).To(BeTrue())
			Expect(n.Summary).To(ContainSubstring("Email"))
			_, e := undo(r, d.OperationID, "undo-1")
			Expect(e).NotTo(HaveOccurred())
			Expect(r.columnID("People", "Email")).To(Equal(id), "the column keeps its internal ID")
			p := r.query(store.Query{Columns: []string{"Name", "Email"}, Sort: []store.SortKey{{Column: "_id"}}})
			Expect(p.Rows[0].Values).To(Equal([]any{"a", "a@x"}))
			Expect(p.Rows[1].Values).To(Equal([]any{"b", "b@x"}))
			Expect(p.Rows[0].ID).To(Equal(ids[0]))
			_, e = r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "c", "Email": "a@x"})})
			Expect(e).To(MatchError(store.ErrRowConstraint), "the unique rule is back")
			_, e = r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "c", "Email": nil})})
			Expect(e).To(MatchError(store.ErrRowConstraint), "the required rule is back")
		})

		It("restores a dropped table with its columns, rows and row IDs", func() {
			r := newRowsRig(store.Options{})
			ids := r.insert(map[string]any{"Name": "a", "Age": 3}, map[string]any{"Name": "b"})
			before := r.query(store.Query{Columns: []string{"Name", "Age"}, Sort: []store.SortKey{{Column: "_id"}}})
			d := r.must(store.SchemaChange{Kind: store.KindTableDrop, Table: "People"})
			Expect(noteOf(d).Available).To(BeTrue())
			Expect(r.inspect().Tables).To(BeEmpty())
			_, e := undo(r, d.OperationID, "undo-1")
			Expect(e).NotTo(HaveOccurred())
			after := r.query(store.Query{Columns: []string{"Name", "Age"}, Sort: []store.SortKey{{Column: "_id"}}})
			Expect(after.Rows).To(Equal(before.Rows))
			Expect(after.Rows[0].ID).To(Equal(ids[0]))
			more := r.insert(map[string]any{"Name": "c"})
			Expect(more[0]).To(BeNumerically(">", ids[1]), "row IDs are never reused")
		})

		It("says a dropped column or table was too large to keep, at apply time", func() {
			r := newRowsRig(store.Options{MaxChangeBytes: 1024})
			big := strings.Repeat("z", 400)
			r.insert(map[string]any{"Name": big}, map[string]any{"Name": big}, map[string]any{"Name": big})
			d := r.must(store.SchemaChange{Kind: store.KindColumnDrop, Table: "People", Column: "Name"})
			n := noteOf(d)
			Expect(n.Available).To(BeFalse())
			Expect(n.Reason).To(ContainSubstring("too large"))
			r.insert(map[string]any{"Tag": big}, map[string]any{"Tag": big}, map[string]any{"Tag": big})
			t := r.must(store.SchemaChange{Kind: store.KindTableDrop, Table: "People"})
			Expect(noteOf(t).Available).To(BeFalse())
		})

		It("renames the database back only while it still has the new name", func() {
			r := newRowsRig(store.Options{})
			rc, e := r.s.Rename(ctx, idA, "Revenue", "op-rename-1")
			Expect(e).NotTo(HaveOccurred())
			n := noteOf(rc)
			Expect(n.Available).To(BeTrue())
			_, e = undo(r, "op-rename-1", "undo-1")
			Expect(e).NotTo(HaveOccurred())
			Expect(r.inspect().Name).To(Equal("Sales"))
			_, e = r.s.Rename(ctx, idA, "Other", "op-rename-2")
			Expect(e).NotTo(HaveOccurred())
			_, e = r.s.Rename(ctx, idA, "Third", "op-rename-3")
			Expect(e).NotTo(HaveOccurred())
			info, _ := r.s.Change(ctx, idA, "op-rename-2")
			Expect(info.State).To(Equal(contract.ChangeStale))
		})

		It("undoes a creation only while the database is untouched, and marks that the resource is deleted", func() {
			r := newRowsRig(store.Options{})
			fresh := open(r.dir+"-fresh", store.Options{})
			_, e := fresh.Create(ctx, idB, "Scratch", "op-create-b")
			Expect(e).NotTo(HaveOccurred())
			info, e := fresh.Change(ctx, idB, "op-create-b")
			Expect(e).NotTo(HaveOccurred())
			Expect(info.State).To(Equal(contract.ChangeAvailable))
			Expect(info.Deletes).To(BeTrue())
			Expect(info.Class).To(Equal(contract.ChangeClassSchema))
			info, e = r.s.Change(ctx, idA, "op-create-a")
			Expect(e).NotTo(HaveOccurred())
			Expect(info.State).To(Equal(contract.ChangeStale), "the database has changes since its creation")
		})
	})

	It("keeps the change sets in the database file, so deleting the database removes them", func() {
		r := newRowsRig(store.Options{})
		rc, _ := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "a"})})
		_, e := r.s.Delete(ctx, idA, "op-delete-a")
		Expect(e).NotTo(HaveOccurred())
		_, e = r.s.Change(ctx, idA, rc.OperationID)
		Expect(e).To(HaveOccurred())
	})
})
