// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func rowsOf(m ...map[string]any) []map[string]json.RawMessage {
	out := make([]map[string]json.RawMessage, len(m))
	for i, row := range m {
		out[i] = map[string]json.RawMessage{}
		for k, v := range row {
			out[i][k] = jv(v)
		}
	}
	return out
}
func setOf(m map[string]any) map[string]json.RawMessage { return rowsOf(m)[0] }

func rowCount(r *rowsRig) int {
	GinkgoHelper()
	var n int
	Expect(r.raw().QueryRow("SELECT COUNT(*) FROM " + r.tableID("People")).Scan(&n)).To(Succeed())
	return n
}

var _ = Describe("Tables confirmed row mutations", func() {
	ctx := context.Background()

	It("leaves no pending line for a change the add-on refused, so the same ID applies the corrected request", func() {
		r := newRowsRig(store.Options{})
		r.must(store.SchemaChange{Kind: store.KindColumnAdd, Table: "People", Column: "Role", Type: "text", Required: true})
		const op = "refused-then-fixed"
		bad := store.RowChange{ResourceID: idA, Table: "People", Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "no role"})}
		_, e := r.s.ApplyRows(ctx, bad, r.rev, op)
		Expect(e).To(MatchError(store.ErrRowConstraint))
		got, e := r.s.Receipt(ctx, op)
		Expect(e).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(contract.ReceiptUnknown), "a refused change leaves no ledger line, not even a pending one")
		good := store.RowChange{ResourceID: idA, Table: "People", Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "ok", "Role": "dev"})}
		rc, e := r.s.ApplyRows(ctx, good, r.rev, op)
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(rowCount(r)).To(Equal(1))
	})

	It("inserts bound rows atomically with defaults, required and unique checks", func() {
		r := newRowsRig(store.Options{})
		r.must(store.SchemaChange{Kind: store.KindColumnAdd, Table: "People", Column: "Email", Type: "text", Unique: true})
		r.must(store.SchemaChange{Kind: store.KindColumnAdd, Table: "People", Column: "Level", Type: "integer", Required: true, Default: jv(1)})
		r.must(store.SchemaChange{Kind: store.KindColumnAdd, Table: "People", Column: "Role", Type: "text", Required: true})
		rev := r.rev
		_, e := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "no role"})})
		Expect(e).To(MatchError(store.ErrRowConstraint))
		Expect(e.Error()).To(ContainSubstring(`"Role"`))
		Expect(e.Error()).NotTo(ContainSubstring("c_"))
		Expect(rowCount(r)).To(Equal(0))
		Expect(r.rev).To(Equal(rev))
		rc, e := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(
			map[string]any{"Role": "dev", "Email": "a@x"},
			map[string]any{"Role": "ops", "Level": 7, "Email": "b@x"},
			map[string]any{"Role": "qa"},
			map[string]any{"Role": "qa"},
		)})
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.Revision).To(Equal(contract.Revision{Schema: rev.Schema, Data: rev.Data + 1}))
		p := r.query(store.Query{Columns: []string{"Role", "Level", "Email"}})
		Expect(p.Rows).To(HaveLen(4))
		Expect(p.Rows[0].Values).To(Equal([]any{"dev", int64(1), "a@x"}))
		Expect(p.Rows[1].Values).To(Equal([]any{"ops", int64(7), "b@x"}))
		Expect(p.Rows[2].Values).To(Equal([]any{"qa", int64(1), nil}))
		// A unique clash in the middle of a batch rejects the whole batch.
		before := r.rev
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(
			map[string]any{"Role": "x", "Email": "c@x"}, map[string]any{"Role": "y", "Email": "a@x"})})
		Expect(e).To(MatchError(store.ErrRowConstraint))
		Expect(e.Error()).To(ContainSubstring(`"Email"`))
		Expect(rowCount(r)).To(Equal(4))
		Expect(r.rev).To(Equal(before))
		// Explicit null for a required column is refused even when it has a default.
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Role": "z", "Level": nil})})
		Expect(e).To(MatchError(store.ErrRowConstraint))
		// Type errors, unknown columns, the id and duplicates by case.
		for _, bad := range []map[string]any{
			{"Role": "r", "Age": "old"}, {"Role": "r", "Age": 1.5}, {"Role": "r", "Score": "x"}, {"Role": "r", "Active": 1},
			{"Role": "r", "Born": "yesterday"}, {"Role": 5}, {"Role": "r", "Nope": 1}, {"Role": "r", "_id": 99},
			{"Role": "r", "Name": "a", "name": "b"}, {"Role": "bad\x00"}, {"Role": strings.Repeat("x", store.MaxTextBytes+1)},
		} {
			_, e = r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(bad)})
			Expect(e).To(HaveOccurred(), fmt.Sprint(bad))
		}
		Expect(rowCount(r)).To(Equal(4))
		// Batch bounds.
		var many []map[string]any
		for i := 0; i <= store.MaxInsertRows; i++ {
			many = append(many, map[string]any{"Role": "bulk"})
		}
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(many...)})
		Expect(e).To(MatchError(store.ErrInvalidRows))
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowInsert})
		Expect(e).To(MatchError(store.ErrInvalidRows))
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(many[:store.MaxInsertRows]...)})
		Expect(e).NotTo(HaveOccurred())
	})

	It("updates by explicit IDs and by filter and bumps only the data revision", func() {
		r := newRowsRig(store.Options{})
		ids := r.insert(
			map[string]any{"Name": "a", "Age": 1}, map[string]any{"Name": "b", "Age": 2},
			map[string]any{"Name": "c", "Age": 2}, map[string]any{"Name": "d"})
		start := r.rev
		rc, e := r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, IDs: ids[:1], Set: setOf(map[string]any{"Name": "A", "Score": 4.5})})
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.Revision).To(Equal(contract.Revision{Schema: start.Schema, Data: start.Data + 1}))
		rc, e = r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, Filter: f("Age", "eq", 2), Set: setOf(map[string]any{"Tag": "two", "Score": nil})})
		Expect(e).NotTo(HaveOccurred())
		var pay struct {
			Count int     `json:"count"`
			IDs   []int64 `json:"ids"`
		}
		Expect(json.Unmarshal(rc.Result.Payload, &pay)).To(Succeed())
		Expect(pay.IDs).To(Equal([]int64{2, 3}))
		Expect(pay.Count).To(Equal(2))
		p := r.query(store.Query{Columns: []string{"Name", "Score", "Tag"}})
		Expect(p.Rows[0].Values).To(Equal([]any{"A", 4.5, nil}))
		Expect(p.Rows[1].Values).To(Equal([]any{"b", nil, "two"}))
		Expect(p.Rows[3].Values).To(Equal([]any{"d", nil, nil}))
		// Setting a column to null is allowed; a type error or a unique clash changes nothing.
		r.must(store.SchemaChange{Kind: store.KindColumnAdd, Table: "People", Column: "Code", Type: "integer", Unique: true})
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, IDs: []int64{1}, Set: setOf(map[string]any{"Code": 5})})
		Expect(e).NotTo(HaveOccurred())
		rev := r.rev
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, IDs: []int64{2}, Set: setOf(map[string]any{"Code": 5})})
		Expect(e).To(MatchError(store.ErrRowConstraint))
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, IDs: []int64{2}, Set: setOf(map[string]any{"Age": "x"})})
		Expect(e).To(HaveOccurred())
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, IDs: []int64{2}, Set: setOf(map[string]any{"_id": 9})})
		Expect(e).To(HaveOccurred())
		Expect(r.rev).To(Equal(rev))
		// Unknown ids, no match, neither or both selections and an empty set.
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, IDs: []int64{1, 99}, Set: setOf(map[string]any{"Name": "x"})})
		Expect(e).To(MatchError(store.ErrRowNotFound))
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, Filter: f("Age", "eq", 77), Set: setOf(map[string]any{"Name": "x"})})
		Expect(e).To(MatchError(store.ErrNoRows))
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, Set: setOf(map[string]any{"Name": "x"})})
		Expect(e).To(MatchError(store.ErrInvalidRows))
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, IDs: []int64{1}, Filter: f("Age", "eq", 1), Set: setOf(map[string]any{"Name": "x"})})
		Expect(e).To(MatchError(store.ErrInvalidRows))
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, IDs: []int64{1}})
		Expect(e).To(MatchError(store.ErrInvalidRows))
		Expect(r.rev).To(Equal(rev))
	})

	It("deletes by IDs or filter and refuses broad, empty or oversized selections", func() {
		r := newRowsRig(store.Options{})
		var rows []map[string]any
		for i := 0; i < store.MaxAffectedRows+5; i++ {
			rows = append(rows, map[string]any{"Name": fmt.Sprint(i), "Age": i % 2})
			if len(rows) == 100 {
				r.insert(rows...)
				rows = nil
			}
		}
		r.insert(rows...)
		Expect(rowCount(r)).To(Equal(1005))
		rev := r.rev
		// A filter that matches more than the bound is refused with a request to narrow it.
		_, e := r.rowsDo(store.RowChange{Kind: store.KindRowDelete, Filter: f("Age", "gte", 0)})
		Expect(e).To(MatchError(store.ErrTooManyRows))
		var ids []int64
		for i := int64(1); i <= store.MaxAffectedRows+1; i++ {
			ids = append(ids, i)
		}
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowDelete, IDs: ids})
		Expect(e).To(MatchError(store.ErrTooManyRows))
		// Neither ids nor filter would be a truncate; an empty filter is invalid, not "everything".
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowDelete})
		Expect(e).To(MatchError(store.ErrInvalidRows))
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowDelete, Filter: &store.Filter{}})
		Expect(e).To(HaveOccurred())
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowDelete, IDs: []int64{3, 3}})
		Expect(e).To(MatchError(store.ErrInvalidRows))
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowDelete, IDs: []int64{0}})
		Expect(e).To(MatchError(store.ErrInvalidRows))
		Expect(rowCount(r)).To(Equal(1005))
		Expect(r.rev).To(Equal(rev))
		rc, e := r.rowsDo(store.RowChange{Kind: store.KindRowDelete, Filter: and(f("Age", "eq", 1), f("_id", "lte", 100))})
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.Revision.Data).To(Equal(rev.Data + 1))
		Expect(rowCount(r)).To(Equal(955))
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowDelete, IDs: []int64{1, 2}})
		Expect(e).To(MatchError(store.ErrRowNotFound))
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowDelete, IDs: []int64{1}})
		Expect(e).NotTo(HaveOccurred())
		// Row IDs are never reused after a delete.
		Expect(r.insert(map[string]any{"Name": "fresh"})).To(Equal([]int64{1006}))
	})

	It("previews exact count, sample and conflicts without changing anything", func() {
		r := newRowsRig(store.Options{})
		r.must(store.SchemaChange{Kind: store.KindColumnAdd, Table: "People", Column: "Code", Type: "integer", Unique: true})
		var rows []map[string]any
		for i := 0; i < 30; i++ {
			rows = append(rows, map[string]any{"Name": fmt.Sprint("n", i), "Age": i % 3, "Code": i})
		}
		r.insert(rows...)
		before := r.query(store.Query{Limit: store.MaxPage})
		rev := r.rev
		var receipts int
		Expect(r.raw().QueryRow("SELECT COUNT(*) FROM tables_schema_receipts").Scan(&receipts)).To(Succeed())
		prev := func(c store.RowChange) (contract.Preview, error) {
			c.ResourceID, c.Table = idA, "People"
			return r.s.PreviewRows(ctx, c)
		}
		type sum struct {
			Count        int              `json:"count"`
			Action       string           `json:"action"`
			Sample       []map[string]any `json:"sample"`
			AffectedHash string           `json:"affectedHash"`
			DataLoss     map[string]int   `json:"dataLoss"`
		}
		read := func(p contract.Preview) sum {
			var s sum
			Expect(json.Unmarshal(p.Summary, &s)).To(Succeed())
			return s
		}
		p, e := prev(store.RowChange{Kind: store.KindRowDelete, Filter: f("Age", "eq", 1)})
		Expect(e).NotTo(HaveOccurred())
		Expect(p.Revision).To(Equal(rev))
		s := read(p)
		Expect(s.Count).To(Equal(10))
		Expect(s.Action).To(Equal("delete rows"))
		Expect(s.Sample).To(HaveLen(10))
		Expect(s.DataLoss["rows"]).To(Equal(10))
		Expect(s.AffectedHash).To(HaveLen(64))
		Expect(s.Sample[0]["id"]).To(BeEquivalentTo(2))
		Expect(s.Sample[0]["values"]).To(HaveKeyWithValue("Name", "n1"))
		p, e = prev(store.RowChange{Kind: store.KindRowUpdate, Filter: f("Age", "in", nil), Set: setOf(map[string]any{"Name": "x"})})
		Expect(e).To(HaveOccurred())
		p, e = prev(store.RowChange{Kind: store.KindRowUpdate, Filter: f("Age", "gte", 0), Set: setOf(map[string]any{"Tag": "t"})})
		Expect(e).NotTo(HaveOccurred())
		s = read(p)
		Expect(s.Count).To(Equal(30))
		Expect(s.Sample).To(HaveLen(store.MaxPage / 50))
		Expect(s.Sample[0]).To(HaveKey("before"))
		Expect(s.Sample[0]["after"]).To(HaveKeyWithValue("Tag", "t"))
		// A preview of a clash reports it, and so would apply.
		_, e = prev(store.RowChange{Kind: store.KindRowUpdate, Filter: f("Age", "eq", 1), Set: setOf(map[string]any{"Code": 5})})
		Expect(e).To(MatchError(store.ErrRowConstraint))
		_, e = prev(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Code": 5}, map[string]any{"Name": "ok"})})
		Expect(e).To(MatchError(store.ErrRowConstraint))
		p, e = prev(store.RowChange{Kind: store.KindRowInsert, Rows: rowsOf(map[string]any{"Name": "new", "Age": 1})})
		Expect(e).NotTo(HaveOccurred())
		Expect(read(p).Count).To(Equal(1))
		Expect(r.query(store.Query{Limit: store.MaxPage})).To(Equal(before))
		Expect(r.inspect().Revision).To(Equal(rev))
		// Nothing was recorded: the preview left no receipt in the database or the ledger.
		var n int
		Expect(r.raw().QueryRow("SELECT COUNT(*) FROM tables_schema_receipts").Scan(&n)).To(Succeed())
		Expect(n).To(Equal(receipts))
	})

	It("refuses a changed revision and never re-evaluates a filter against new rows", func() {
		r := newRowsRig(store.Options{})
		r.insert(map[string]any{"Name": "old", "Age": 5})
		del := store.RowChange{ResourceID: idA, Kind: store.KindRowDelete, Table: "People", Filter: f("Age", "eq", 5)}
		p, e := r.s.PreviewRows(ctx, del)
		Expect(e).NotTo(HaveOccurred())
		// Another writer adds a row that the same filter now matches.
		r.insert(map[string]any{"Name": "newcomer", "Age": 5})
		_, e = r.s.ApplyRows(ctx, del, p.Revision, "op-stale-1")
		var stale *store.StaleError
		Expect(errors.As(e, &stale)).To(BeTrue())
		Expect(stale.Current).To(Equal(r.rev))
		Expect(e).To(MatchError(store.ErrStale))
		Expect(r.names(store.Query{})).To(Equal([]string{"old", "newcomer"}))
		// The rejected ID is not burnt: after a fresh preview the same ID applies.
		p, e = r.s.PreviewRows(ctx, del)
		Expect(e).NotTo(HaveOccurred())
		rc, e := r.s.ApplyRows(ctx, del, p.Revision, "op-stale-1")
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		r.rev = rc.Revision
		// A schema change moves the pair too, whatever the rows are.
		r.insert(map[string]any{"Name": "x", "Age": 9})
		upd := store.RowChange{ResourceID: idA, Kind: store.KindRowUpdate, Table: "People", IDs: []int64{3}, Set: setOf(map[string]any{"Name": "y"})}
		p, e = r.s.PreviewRows(ctx, upd)
		Expect(e).NotTo(HaveOccurred())
		r.must(ca("People", "Extra", "text"))
		_, e = r.s.ApplyRows(ctx, upd, p.Revision, "op-stale-2")
		Expect(e).To(MatchError(store.ErrStale))
		// A row change moves a schema proposal's pair as well (the Task 14 semantic is kept).
		sp := store.SchemaChange{ResourceID: idA, Kind: store.KindColumnAdd, Table: "People", Column: "Late", Type: "text"}
		rev := r.rev
		r.insert(map[string]any{"Name": "z"})
		_, e = r.s.ApplySchema(ctx, sp, rev, "op-stale-3")
		Expect(e).To(MatchError(store.ErrStale))
	})

	It("applies exactly one of two proposals made on the same revision", func() {
		r := newRowsRig(store.Options{})
		r.insert(map[string]any{"Name": "a", "Age": 1}, map[string]any{"Name": "b", "Age": 1})
		a := store.RowChange{ResourceID: idA, Kind: store.KindRowUpdate, Table: "People", Filter: f("Age", "eq", 1), Set: setOf(map[string]any{"Tag": "A"})}
		b := store.RowChange{ResourceID: idA, Kind: store.KindRowUpdate, Table: "People", Filter: f("Age", "eq", 1), Set: setOf(map[string]any{"Tag": "B"})}
		pa, e := r.s.PreviewRows(ctx, a)
		Expect(e).NotTo(HaveOccurred())
		pb, e := r.s.PreviewRows(ctx, b)
		Expect(e).NotTo(HaveOccurred())
		Expect(pa.Revision).To(Equal(pb.Revision))
		var wg sync.WaitGroup
		errs := make([]error, 2)
		recs := make([]contract.Receipt, 2)
		start := make(chan struct{})
		for i, c := range []store.RowChange{a, b} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				recs[i], errs[i] = r.s.ApplyRows(ctx, c, pa.Revision, fmt.Sprintf("op-race-%d", i))
			}()
		}
		close(start)
		wg.Wait()
		okN, staleN := 0, 0
		for _, e := range errs {
			switch {
			case e == nil:
				okN++
			case errors.Is(e, store.ErrStale):
				staleN++
			}
		}
		Expect(okN).To(Equal(1))
		Expect(staleN).To(Equal(1))
		p := r.query(store.Query{Columns: []string{"Tag"}})
		Expect(p.Rows[0].Values[0]).To(Equal(p.Rows[1].Values[0]))
		Expect(p.Revision.Data).To(Equal(pa.Revision.Data + 1))
	})

	It("replays a row operation by ID and refuses another request under the same ID", func() {
		r := newRowsRig(store.Options{})
		c := store.RowChange{ResourceID: idA, Kind: store.KindRowInsert, Table: "People", Rows: rowsOf(map[string]any{"Name": "once"})}
		rc, e := r.s.ApplyRows(ctx, c, r.rev, "op-replay")
		Expect(e).NotTo(HaveOccurred())
		again, e := r.s.ApplyRows(ctx, c, r.rev, "op-replay")
		Expect(e).NotTo(HaveOccurred())
		Expect(again).To(Equal(rc))
		// The same request with a different spelling is the same request.
		c2 := c
		c2.Table = "PEOPLE"
		again, e = r.s.ApplyRows(ctx, c2, rc.Revision, "op-replay")
		Expect(e).NotTo(HaveOccurred())
		Expect(again).To(Equal(rc))
		Expect(r.names(store.Query{})).To(Equal([]string{"once"}))
		other := c
		other.Rows = rowsOf(map[string]any{"Name": "different"})
		_, e = r.s.ApplyRows(ctx, other, rc.Revision, "op-replay")
		Expect(e).To(MatchError(store.ErrConflict))
		_, e = r.s.ApplySchema(ctx, store.SchemaChange{ResourceID: idA, Kind: store.KindTableCreate, Table: "Z"}, rc.Revision, "op-replay")
		Expect(e).To(MatchError(store.ErrConflict))
		got, e := r.s.LookupReceipt(ctx, "op-replay")
		Expect(e).NotTo(HaveOccurred())
		Expect(got).To(Equal(rc))
		Expect(got.Validate(contract.DefaultLimits())).To(Succeed())
		Expect(r.names(store.Query{})).To(Equal([]string{"once"}))
	})

	It("leaves no change when the process dies before commit and finds the receipt after commit", func() {
		r := newRowsRig(store.Options{})
		r.insert(map[string]any{"Name": "a", "Age": 1})
		c := store.RowChange{ResourceID: idA, Kind: store.KindRowUpdate, Table: "People", IDs: []int64{1}, Set: setOf(map[string]any{"Name": "b"})}
		fail := func(point string) {
			r.s.SetFailpoint(func(p string) error {
				if p == point {
					return errors.New("killed at " + point)
				}
				return nil
			})
		}
		fail(store.FailBeforeCommit)
		_, e := r.s.ApplyRows(ctx, c, r.rev, "op-row-crash-a")
		Expect(e).To(MatchError(ContainSubstring("killed")))
		Expect(r.s.Close()).To(Succeed())
		s2 := open(r.dir, store.Options{})
		Expect(s2.LookupReceipt(ctx, "op-row-crash-a")).To(Equal(contract.Receipt{OperationID: "op-row-crash-a", State: contract.ReceiptUnknown}))
		p, e := s2.Query(ctx, idA, store.Query{Table: "People", Columns: []string{"Name"}})
		Expect(e).NotTo(HaveOccurred())
		Expect(p.Rows[0].Values[0]).To(Equal("a"))
		Expect(p.Revision).To(Equal(r.rev))
		// The ID is held by the request that began; a different request is a conflict.
		diff := c
		diff.Set = setOf(map[string]any{"Name": "other"})
		_, e = s2.ApplyRows(ctx, diff, r.rev, "op-row-crash-a")
		Expect(e).To(MatchError(store.ErrConflict))
		// The same ID can still apply.
		s2.SetFailpoint(func(p string) error {
			if p == store.FailAfterCommit {
				return errors.New("killed after commit")
			}
			return nil
		})
		_, e = s2.ApplyRows(ctx, c, r.rev, "op-row-crash-a")
		Expect(e).To(MatchError(ContainSubstring("killed after commit")))
		Expect(s2.Close()).To(Succeed())
		s3 := open(r.dir, store.Options{})
		rc, e := s3.LookupReceipt(ctx, "op-row-crash-a")
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(rc.Revision.Data).To(Equal(r.rev.Data + 1))
		var pay struct {
			Count int     `json:"count"`
			IDs   []int64 `json:"ids"`
		}
		Expect(json.Unmarshal(rc.Result.Payload, &pay)).To(Succeed())
		Expect(pay.Count).To(Equal(1))
		// A retry returns the receipt and never applies twice.
		again, e := s3.ApplyRows(ctx, c, r.rev, "op-row-crash-a")
		Expect(e).NotTo(HaveOccurred())
		Expect(again.Revision).To(Equal(rc.Revision))
		p, e = s3.Query(ctx, idA, store.Query{Table: "People", Columns: []string{"Name"}})
		Expect(e).NotTo(HaveOccurred())
		Expect(p.Rows[0].Values[0]).To(Equal("b"))
		Expect(p.Revision.Data).To(Equal(r.rev.Data + 1))
	})

	It("reserves the row id label so no column can shadow it", func() {
		r := newRowsRig(store.Options{})
		_, e := r.do(ca("People", "_ID", "integer"))
		Expect(e).To(MatchError(store.ErrInvalidSchema))
		_, e = r.do(store.SchemaChange{Kind: store.KindColumnRename, Table: "People", Column: "Tag", NewName: "_id"})
		Expect(e).To(MatchError(store.ErrInvalidSchema))
		r.insert(map[string]any{"Name": "a"})
		_, e = r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, IDs: []int64{1}, Set: setOf(map[string]any{"_id": 5})})
		Expect(e).To(HaveOccurred())
		Expect(r.query(store.Query{}).Rows[0].ID).To(Equal(int64(1)))
	})

	It("keeps a row table's data across a column drop that rebuilds it", func() {
		r := newRowsRig(store.Options{})
		r.insert(map[string]any{"Name": "a", "Age": 1, "Tag": "t"}, map[string]any{"Name": "b", "Tag": "u"})
		r.must(store.SchemaChange{Kind: store.KindColumnDrop, Table: "People", Column: "Age"})
		p := r.query(store.Query{Columns: []string{"Name", "Tag"}})
		Expect(p.Rows).To(HaveLen(2))
		Expect(p.Rows[1].Values).To(Equal([]any{"b", "u"}))
		Expect(p.Rows[1].ID).To(Equal(int64(2)))
		_, e := r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, IDs: []int64{1}, Set: setOf(map[string]any{"Age": 1})})
		Expect(e).To(HaveOccurred())
	})
})
