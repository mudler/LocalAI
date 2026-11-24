// SPDX-License-Identifier: MIT
package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/tables/internal/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// hookWriter is a destination that reports every write. It lets a spec act
// while an export is in flight.
type hookWriter struct {
	buf    bytes.Buffer
	writes int
	max    int
	hook   func(n int) error
}

func (w *hookWriter) Write(p []byte) (int, error) {
	w.writes++
	if len(p) > w.max {
		w.max = len(p)
	}
	if w.hook != nil {
		if err := w.hook(w.writes); err != nil {
			return 0, err
		}
	}
	return w.buf.Write(p)
}

func (r *rowsRig) export(q store.Query) (string, store.ExportStats, error) {
	var b bytes.Buffer
	if q.Table == "" {
		q.Table = "People"
	}
	st, e := r.s.ExportCSV(context.Background(), idA, q, &b)
	return b.String(), st, e
}

var _ = Describe("CSV export of a table snapshot", func() {
	It("writes a header and one record per row with RFC 4180 quoting, UTF-8 and the documented value forms", func() {
		r := newRowsRig(store.Options{})
		r.insert(
			map[string]any{"Name": "Ada, \"Countess\"", "Age": 36, "Score": 0.1, "Active": true, "Born": "1815-12-10T00:00:00Z", "Tag": "line1\nline2"},
			map[string]any{"Name": "  padded  ", "Age": 9007199254740993, "Score": 100000000.0, "Active": false, "Tag": "naïve ☃ 日本語"},
			map[string]any{"Name": "", "Tag": "cr\r\nlf"},
			map[string]any{"Name": "plain"},
		)
		out, st, e := r.export(store.Query{Sort: []store.SortKey{{Column: "_id"}}})
		Expect(e).NotTo(HaveOccurred())
		want := "Name,Age,Score,Active,Born,Tag\r\n" +
			"\"Ada, \"\"Countess\"\"\",36,0.1,true,1815-12-10T00:00:00Z,\"line1\nline2\"\r\n" +
			"\"  padded  \",9007199254740993,100000000,false,,naïve ☃ 日本語\r\n" +
			"\"\",,,,,\"cr\r\nlf\"\r\n" +
			"plain,,,,,\r\n"
		Expect(out).To(Equal(want))
		Expect(st.Rows).To(Equal(int64(4)))
		Expect(st.Bytes).To(Equal(int64(len(want))))
		Expect(strings.HasPrefix(out, "\xef\xbb\xbf")).To(BeFalse(), "no byte order mark")
	})

	It("prefixes text cells and labels that a spreadsheet would run, never numbers", func() {
		r := newRowsRig(store.Options{})
		r.must(ca("People", "=Calc", "text"))
		r.insert(
			map[string]any{"Name": "=1+1", "Age": -5, "Score": -0.5, "Tag": "+cmd|' /C calc'!A0", "=Calc": "@SUM(A1)"},
			map[string]any{"Name": "-2+3", "Tag": "\tTabbed", "=Calc": "\rcarriage"},
			map[string]any{"Name": "safe-dash", "Tag": "a=b"},
		)
		out, _, e := r.export(store.Query{Columns: []string{"Name", "Age", "Score", "Tag", "=Calc"}, Sort: []store.SortKey{{Column: "_id"}}})
		Expect(e).NotTo(HaveOccurred())
		want := "Name,Age,Score,Tag,'=Calc\r\n" +
			"'=1+1,-5,-0.5,'+cmd|' /C calc'!A0,'@SUM(A1)\r\n" +
			"'-2+3,,,'\tTabbed,\"'\rcarriage\"\r\n" +
			"safe-dash,,,a=b,\r\n"
		Expect(out).To(Equal(want))
	})

	It("honours columns, the row identifier column, filter, sort and the row limit of the query", func() {
		r := newRowsRig(store.Options{})
		ids := r.insert(
			map[string]any{"Name": "c", "Age": 3}, map[string]any{"Name": "a", "Age": 1},
			map[string]any{"Name": "b", "Age": 2}, map[string]any{"Name": "d", "Age": 4})
		out, st, e := r.export(store.Query{Columns: []string{"_id", "Name"}, Filter: f("Age", store.OpGte, 2), Sort: []store.SortKey{{Column: "Name"}}, Limit: 2})
		Expect(e).NotTo(HaveOccurred())
		Expect(out).To(Equal(fmt.Sprintf("_id,Name\r\n%d,b\r\n%d,c\r\n", ids[2], ids[0])))
		Expect(st.Rows).To(Equal(int64(2)))

		for _, bad := range []store.Query{
			{Table: "People", Cursor: "abc"},
			{Table: "People", Limit: -1},
			{Table: "People", Columns: []string{"Nope"}},
			{Table: "People", Columns: []string{"Name", "Name"}},
			{Table: "People", Sort: []store.SortKey{{Column: "Nope"}}},
			{Table: "Nope"},
			{Table: "People", Filter: f("Nope", store.OpEq, 1)},
		} {
			got, _, e := r.export(bad)
			Expect(e).To(HaveOccurred(), "%+v", bad)
			Expect(got).To(BeEmpty(), "nothing is written for a refused query: %+v", bad)
		}
		_, e = r.s.ExportCSV(context.Background(), "no-such-db", store.Query{Table: "People"}, io.Discard)
		Expect(errors.Is(e, store.ErrNotFound)).To(BeTrue())
	})

	It("reads one snapshot: writes that commit while the export streams are not in it, and are not blocked by it", func() {
		r := newRowsRig(store.Options{})
		for i := 0; i < 5; i++ {
			r.insert(map[string]any{"Name": fmt.Sprintf("before-%d", i)})
		}
		before := r.rev
		w := &hookWriter{}
		var writer error
		w.hook = func(n int) error {
			if n != 1 {
				return nil
			}
			// The header was written, so the snapshot is open. A writer must
			// get through now and the export must not see its rows.
			done := make(chan error, 1)
			go func() {
				_, e := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: []map[string]json.RawMessage{{"Name": jv("during-insert")}}})
				if e == nil {
					_, e = r.rowsDo(store.RowChange{Kind: store.KindRowUpdate, Filter: f("Name", store.OpEq, "before-0"), Set: map[string]json.RawMessage{"Name": jv("changed")}})
				}
				done <- e
			}()
			select {
			case writer = <-done:
			case <-time.After(5 * time.Second):
				writer = errors.New("the writer is blocked by the export")
			}
			return nil
		}
		st, e := r.s.ExportCSV(context.Background(), idA, store.Query{Table: "People", Columns: []string{"Name"}, Sort: []store.SortKey{{Column: "_id"}}}, w)
		Expect(e).NotTo(HaveOccurred())
		Expect(writer).NotTo(HaveOccurred())
		Expect(w.buf.String()).To(Equal("Name\r\nbefore-0\r\nbefore-1\r\nbefore-2\r\nbefore-3\r\nbefore-4\r\n"))
		Expect(st.Revision).To(Equal(before), "the snapshot names the revision it read")
		// The database moved on: the next export sees the writes.
		out, _, e := r.export(store.Query{Columns: []string{"Name"}, Sort: []store.SortKey{{Column: "_id"}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("during-insert"))
		Expect(out).To(ContainSubstring("changed"))
	})

	It("streams a large table row by row with bounded memory", func() {
		r := newRowsRig(store.Options{})
		db := r.raw()
		tbl := r.tableID("People")
		col := r.columnID("People", "Name")
		const n = 120000
		tx, e := db.Begin()
		Expect(e).NotTo(HaveOccurred())
		stmt, e := tx.Prepare("INSERT INTO " + tbl + "(" + col + ") VALUES(?)")
		Expect(e).NotTo(HaveOccurred())
		pad := strings.Repeat("x", 180)
		for i := 0; i < n; i++ {
			_, e = stmt.Exec(fmt.Sprintf("row-%07d-%s", i, pad))
			Expect(e).NotTo(HaveOccurred())
		}
		Expect(stmt.Close()).To(Succeed())
		Expect(tx.Commit()).To(Succeed())

		runtime.GC()
		var base runtime.MemStats
		runtime.ReadMemStats(&base)
		var peak uint64
		w := &hookWriter{}
		w.buf.Grow(0)
		var total int
		sink := writerFunc(func(p []byte) (int, error) {
			total += len(p)
			if w.writes++; w.writes%50 == 0 {
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak {
					peak = m.HeapAlloc
				}
			}
			if len(p) > w.max {
				w.max = len(p)
			}
			return len(p), nil
		})
		st, e := r.s.ExportCSV(context.Background(), idA, store.Query{Table: "People", Columns: []string{"Name"}}, sink)
		Expect(e).NotTo(HaveOccurred())
		Expect(st.Rows).To(Equal(int64(n)))
		Expect(total).To(BeNumerically(">", 20<<20))
		Expect(int64(total)).To(Equal(st.Bytes))
		// Incremental: many bounded writes, never one for the whole result.
		Expect(w.writes).To(BeNumerically(">", 500))
		Expect(w.max).To(BeNumerically("<=", 256<<10))
		if peak > base.HeapAlloc {
			Expect(peak-base.HeapAlloc).To(BeNumerically("<", 24<<20), "heap growth during a %d byte export", total)
		}
	})

	It("fails cleanly past the row or byte bound and keeps the bound finite by default", func() {
		r := newRowsRig(store.Options{ExportMaxRows: 10, ExportMaxBytes: 4096})
		for i := 0; i < 11; i++ {
			r.insert(map[string]any{"Name": fmt.Sprintf("n%d", i)})
		}
		_, _, e := r.export(store.Query{Columns: []string{"Name"}})
		Expect(errors.Is(e, store.ErrExportTooLarge)).To(BeTrue(), "%v", e)
		// A limit inside the bound is fine.
		out, _, e := r.export(store.Query{Columns: []string{"Name"}, Limit: 10})
		Expect(e).NotTo(HaveOccurred())
		Expect(strings.Count(out, "\r\n")).To(Equal(11))
		// A limit above the bound is refused, not clipped.
		_, _, e = r.export(store.Query{Columns: []string{"Name"}, Limit: 11})
		Expect(errors.Is(e, store.ErrExportTooLarge)).To(BeTrue())

		small := newRowsRig(store.Options{ExportMaxBytes: 64})
		small.insert(map[string]any{"Name": strings.Repeat("y", 100)})
		_, _, e = small.export(store.Query{Columns: []string{"Name"}})
		Expect(errors.Is(e, store.ErrExportTooLarge)).To(BeTrue(), "%v", e)

		Expect(store.DefaultExportRows).To(BeNumerically("==", 1000000))
		Expect(store.DefaultExportBytes).To(BeNumerically("==", 64<<20))
		Expect(store.DefaultExportTimeout).To(BeNumerically(">", 0))
	})

	It("stops on cancellation, on its deadline and on a failing destination, and reports each", func() {
		r := newRowsRig(store.Options{ExportTimeout: 300 * time.Millisecond})
		for i := 0; i < 50; i++ {
			r.insert(map[string]any{"Name": fmt.Sprintf("n%d", i)})
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w := &hookWriter{hook: func(n int) error {
			if n == 1 {
				cancel()
			}
			return nil
		}}
		_, e := r.s.ExportCSV(ctx, idA, store.Query{Table: "People", Columns: []string{"Name"}}, w)
		Expect(errors.Is(e, context.Canceled)).To(BeTrue(), "%v", e)

		slow := &hookWriter{hook: func(n int) error {
			if n >= 1 {
				time.Sleep(400 * time.Millisecond)
			}
			return nil
		}}
		_, e = r.s.ExportCSV(context.Background(), idA, store.Query{Table: "People", Columns: []string{"Name"}}, slow)
		Expect(errors.Is(e, context.DeadlineExceeded)).To(BeTrue(), "%v", e)

		full := errors.New("no space left on device")
		failing := &hookWriter{hook: func(n int) error {
			if n >= 2 {
				return full
			}
			return nil
		}}
		_, e = r.s.ExportCSV(context.Background(), idA, store.Query{Table: "People", Columns: []string{"Name"}}, failing)
		Expect(errors.Is(e, full)).To(BeTrue(), "%v", e)

		// The store is usable after each failure: no lock is left behind.
		_, e = r.s.Query(context.Background(), idA, store.Query{Table: "People"})
		Expect(e).NotTo(HaveOccurred())
		r.insert(map[string]any{"Name": "after"})
	})

	It("does not change the database it reads", func() {
		r := newRowsRig(store.Options{})
		r.insert(map[string]any{"Name": "a"})
		before := r.inspect().Revision
		_, _, e := r.export(store.Query{})
		Expect(e).NotTo(HaveOccurred())
		Expect(r.inspect().Revision).To(Equal(before))
	})
})

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
