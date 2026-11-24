// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func jv(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// rowsRig is a schemaRig with one table of every logical type.
type rowsRig struct {
	*schemaRig
	n2 int
}

func newRowsRig(opts store.Options) *rowsRig {
	GinkgoHelper()
	r := &rowsRig{schemaRig: newSchemaRig(opts)}
	r.must(tc("People"))
	for _, c := range [][2]string{{"Name", "text"}, {"Age", "integer"}, {"Score", "real"}, {"Active", "boolean"}, {"Born", "datetime"}, {"Tag", "text"}} {
		r.must(ca("People", c[0], c[1]))
	}
	return r
}

func (r *rowsRig) op2() string { r.n2++; return fmt.Sprintf("op-rows-%d", r.n2) }

func (r *rowsRig) rowsDo(c store.RowChange) (contract.Receipt, error) {
	c.ResourceID = idA
	if c.Table == "" {
		c.Table = "People"
	}
	rc, e := r.s.ApplyRows(context.Background(), c, r.rev, r.op2())
	if e == nil {
		r.rev = rc.Revision
	}
	return rc, e
}

func (r *rowsRig) insert(rows ...map[string]any) []int64 {
	GinkgoHelper()
	in := make([]map[string]json.RawMessage, len(rows))
	for i, row := range rows {
		in[i] = map[string]json.RawMessage{}
		for k, v := range row {
			in[i][k] = jv(v)
		}
	}
	rc, e := r.rowsDo(store.RowChange{Kind: store.KindRowInsert, Rows: in})
	Expect(e).NotTo(HaveOccurred())
	Expect(rc.State).To(Equal(contract.ReceiptCommitted))
	var p struct {
		IDs []int64 `json:"ids"`
	}
	Expect(json.Unmarshal(rc.Result.Payload, &p)).To(Succeed())
	return p.IDs
}

func (r *rowsRig) query(q store.Query) store.RowPage {
	GinkgoHelper()
	if q.Table == "" {
		q.Table = "People"
	}
	p, e := r.s.Query(context.Background(), idA, q)
	Expect(e).NotTo(HaveOccurred())
	return p
}

func (r *rowsRig) names(q store.Query) []string {
	GinkgoHelper()
	q.Columns = []string{"Name"}
	var out []string
	for {
		p := r.query(q)
		for _, row := range p.Rows {
			out = append(out, fmt.Sprint(row.Values[0]))
		}
		if !p.HasMore {
			return out
		}
		q.Cursor = p.Cursor
	}
}

func f(col, op string, v any) *store.Filter {
	x := &store.Filter{Column: col, Op: op}
	if v != nil {
		x.Value = jv(v)
	}
	return x
}
func fin(col string, vs ...any) *store.Filter {
	x := &store.Filter{Column: col, Op: store.OpIn}
	for _, v := range vs {
		x.Values = append(x.Values, jv(v))
	}
	return x
}
func and(fs ...*store.Filter) *store.Filter {
	x := &store.Filter{And: []store.Filter{}}
	for _, c := range fs {
		x.And = append(x.And, *c)
	}
	return x
}
func or(fs ...*store.Filter) *store.Filter {
	x := &store.Filter{Or: []store.Filter{}}
	for _, c := range fs {
		x.Or = append(x.Or, *c)
	}
	return x
}

// ---- reference evaluator ----

type refRow map[string]any // label -> nil | string | int64 | float64 | bool

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func cmpVals(a, b any) int {
	switch x := a.(type) {
	case string:
		return strings.Compare(x, b.(string))
	case int64:
		y := b.(int64)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	case float64:
		y := b.(float64)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	case bool:
		if x == b.(bool) {
			return 0
		}
		return 1
	}
	panic("type")
}

func refMatch(row refRow, fl store.Filter, typ map[string]string) bool {
	if fl.And != nil {
		for _, c := range fl.And {
			if !refMatch(row, c, typ) {
				return false
			}
		}
		return true
	}
	if fl.Or != nil {
		for _, c := range fl.Or {
			if refMatch(row, c, typ) {
				return true
			}
		}
		return false
	}
	v := row[fl.Column]
	conv := func(raw json.RawMessage) any {
		var x any
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.UseNumber()
		Expect(d.Decode(&x)).To(Succeed())
		switch typ[fl.Column] {
		case "integer":
			n, _ := x.(json.Number).Int64()
			return n
		case "real":
			n, _ := x.(json.Number).Float64()
			return n
		case "datetime":
			t, _ := time.Parse(time.RFC3339Nano, x.(string))
			return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
		}
		return x
	}
	switch fl.Op {
	case store.OpIsNull:
		return v == nil
	case store.OpIsNotNull:
		return v != nil
	}
	if v == nil {
		return false
	}
	switch fl.Op {
	case store.OpIn:
		for _, raw := range fl.Values {
			if cmpVals(v, conv(raw)) == 0 {
				return true
			}
		}
		return false
	case store.OpContains:
		return strings.Contains(asciiLower(v.(string)), asciiLower(conv(fl.Value).(string)))
	case store.OpStartsWith:
		return strings.HasPrefix(asciiLower(v.(string)), asciiLower(conv(fl.Value).(string)))
	case store.OpEndsWith:
		return strings.HasSuffix(asciiLower(v.(string)), asciiLower(conv(fl.Value).(string)))
	}
	c := cmpVals(v, conv(fl.Value))
	switch fl.Op {
	case store.OpEq:
		return c == 0
	case store.OpNe:
		return c != 0
	case store.OpLt:
		return c < 0
	case store.OpLte:
		return c <= 0
	case store.OpGt:
		return c > 0
	case store.OpGte:
		return c >= 0
	}
	panic("op " + fl.Op)
}

var _ = Describe("Tables structured row queries", func() {
	ctx := context.Background()

	It("reads selected columns with stable row IDs and typed JSON values", func() {
		r := newRowsRig(store.Options{})
		ids := r.insert(
			map[string]any{"Name": "Ada", "Age": 36, "Score": 9.5, "Active": true, "Born": "1815-12-10T00:00:00Z", "Tag": "math"},
			map[string]any{"Name": "Bob"},
		)
		Expect(ids).To(Equal([]int64{1, 2}))
		all := r.query(store.Query{})
		Expect(all.Columns).To(Equal([]string{"Name", "Age", "Score", "Active", "Born", "Tag"}))
		Expect(all.Rows).To(HaveLen(2))
		Expect(all.Rows[0].ID).To(Equal(int64(1)))
		Expect(all.Rows[0].Values).To(Equal([]any{"Ada", int64(36), 9.5, true, "1815-12-10T00:00:00Z", "math"}))
		Expect(all.Rows[1].Values).To(Equal([]any{"Bob", nil, nil, nil, nil, nil}))
		Expect(all.Revision).To(Equal(r.rev))
		sel := r.query(store.Query{Columns: []string{"age", "NAME"}, Filter: f("_id", "eq", 2)})
		Expect(sel.Columns).To(Equal([]string{"Age", "Name"}))
		Expect(sel.Rows).To(HaveLen(1))
		Expect(sel.Rows[0].ID).To(Equal(int64(2)))
		Expect(sel.Rows[0].Values).To(Equal([]any{nil, "Bob"}))
		// An ID is stable: it survives updates of other rows and deletes.
		_, e := r.rowsDo(store.RowChange{Kind: store.KindRowDelete, IDs: []int64{1}})
		Expect(e).NotTo(HaveOccurred())
		again := r.query(store.Query{})
		Expect(again.Rows).To(HaveLen(1))
		Expect(again.Rows[0].ID).To(Equal(int64(2)))
		Expect(r.insert(map[string]any{"Name": "Cy"})).To(Equal([]int64{3}))
	})

	It("filters with bounded AND/OR trees and every operator, matching a reference evaluator on random trees", func() {
		r := newRowsRig(store.Options{})
		rng := rand.New(rand.NewSource(7))
		words := []string{"alpha", "Alpine", "beta", "ALPHA beta", "a_c", "a%c", "abc", "", "gamma", "Δelta"}
		types := map[string]string{"Name": "text", "Age": "integer", "Score": "real", "Active": "boolean", "Born": "datetime", "Tag": "text", "_id": "integer"}
		var data []refRow
		var rows []map[string]any
		for i := 0; i < 60; i++ {
			row := map[string]any{}
			ref := refRow{}
			put := func(k string, v any, ok bool) {
				if ok {
					row[k] = v
					ref[k] = v
				} else {
					ref[k] = nil
				}
			}
			put("Name", words[rng.Intn(len(words))], rng.Intn(6) != 0)
			put("Tag", words[rng.Intn(len(words))], rng.Intn(3) != 0)
			put("Age", rng.Intn(6)*10, rng.Intn(5) != 0)
			put("Score", float64(rng.Intn(5))/2, rng.Intn(5) != 0)
			put("Active", rng.Intn(2) == 0, rng.Intn(5) != 0)
			put("Born", fmt.Sprintf("2020-01-0%dT10:00:0%dZ", 1+rng.Intn(3), rng.Intn(3)), rng.Intn(5) != 0)
			if v, ok := ref["Age"].(int); ok {
				ref["Age"] = int64(v)
			}
			rows = append(rows, row)
			ref["_id"] = int64(i + 1)
			data = append(data, ref)
		}
		r.insert(rows...)
		// normalise the reference datetime to the stored fixed-width form
		for _, d := range data {
			if s, ok := d["Born"].(string); ok {
				t, _ := time.Parse(time.RFC3339Nano, s)
				d["Born"] = t.UTC().Format("2006-01-02T15:04:05.000000000Z")
			}
		}
		leaf := func() *store.Filter {
			switch rng.Intn(8) {
			case 0:
				return f("Name", []string{"eq", "ne", "lt", "gte"}[rng.Intn(4)], words[rng.Intn(len(words))])
			case 1:
				return f("Age", []string{"eq", "ne", "lt", "lte", "gt", "gte"}[rng.Intn(6)], rng.Intn(6)*10)
			case 2:
				return f("Score", []string{"lt", "gt", "eq"}[rng.Intn(3)], float64(rng.Intn(5))/2)
			case 3:
				return f("Active", []string{"eq", "ne"}[rng.Intn(2)], rng.Intn(2) == 0)
			case 4:
				return f([]string{"Name", "Tag", "Age", "Born", "Score"}[rng.Intn(5)], []string{"is-null", "is-not-null"}[rng.Intn(2)], nil)
			case 5:
				return f([]string{"Name", "Tag"}[rng.Intn(2)], []string{"contains", "starts-with", "ends-with"}[rng.Intn(3)], []string{"a", "_", "%", "ALP", "c", "beta", "Δ", ""}[rng.Intn(8)])
			case 6:
				return fin("Age", rng.Intn(6)*10, rng.Intn(6)*10, 999)
			default:
				return f("Born", []string{"lt", "gte", "eq"}[rng.Intn(3)], fmt.Sprintf("2020-01-0%dT10:00:0%dZ", 1+rng.Intn(3), rng.Intn(3)))
			}
		}
		var tree func(d int) *store.Filter
		tree = func(d int) *store.Filter {
			if d == 0 || rng.Intn(3) == 0 {
				return leaf()
			}
			n := 1 + rng.Intn(3)
			var kids []*store.Filter
			for i := 0; i < n; i++ {
				kids = append(kids, tree(d-1))
			}
			if rng.Intn(2) == 0 {
				return and(kids...)
			}
			return or(kids...)
		}
		nonEmpty := 0
		for i := 0; i < 250; i++ {
			fl := tree(3)
			var want []int64
			for _, d := range data {
				if refMatch(d, *fl, types) {
					want = append(want, d["_id"].(int64))
				}
			}
			var got []int64
			q := store.Query{Filter: fl, Columns: []string{"Name"}, Limit: 7}
			for {
				p := r.query(q)
				for _, row := range p.Rows {
					got = append(got, row.ID)
				}
				if !p.HasMore {
					break
				}
				q.Cursor = p.Cursor
			}
			b, _ := json.Marshal(fl)
			Expect(got).To(Equal(want), string(b))
			if len(want) > 0 && len(want) < len(data) {
				nonEmpty++
			}
		}
		// The trees must be selective often enough for the comparison to mean something.
		Expect(nonEmpty).To(BeNumerically(">", 100))
	})

	It("escapes LIKE wildcards in text filters", func() {
		r := newRowsRig(store.Options{})
		r.insert(map[string]any{"Name": "a_c"}, map[string]any{"Name": "abc"}, map[string]any{"Name": "a%c"}, map[string]any{"Name": `a\c`}, map[string]any{"Name": "ABC"})
		Expect(r.names(store.Query{Filter: f("Name", "contains", "_")})).To(Equal([]string{"a_c"}))
		Expect(r.names(store.Query{Filter: f("Name", "contains", "%")})).To(Equal([]string{"a%c"}))
		Expect(r.names(store.Query{Filter: f("Name", "contains", `\`)})).To(Equal([]string{`a\c`}))
		Expect(r.names(store.Query{Filter: f("Name", "starts-with", "AB")})).To(Equal([]string{"abc", "ABC"}))
		Expect(r.names(store.Query{Filter: f("Name", "eq", "ABC")})).To(Equal([]string{"ABC"}))
	})

	It("keeps NULL semantics: comparisons never match NULL and is-null finds it", func() {
		r := newRowsRig(store.Options{})
		r.insert(map[string]any{"Name": "a", "Age": 1}, map[string]any{"Name": "b"}, map[string]any{"Name": "c", "Age": 3})
		Expect(r.names(store.Query{Filter: f("Age", "ne", 1)})).To(Equal([]string{"c"}))
		Expect(r.names(store.Query{Filter: f("Age", "lt", 100)})).To(Equal([]string{"a", "c"}))
		Expect(r.names(store.Query{Filter: f("Age", "is-null", nil)})).To(Equal([]string{"b"}))
		Expect(r.names(store.Query{Filter: or(f("Age", "ne", 1), f("Age", "is-null", nil))})).To(Equal([]string{"b", "c"}))
		for _, bad := range []*store.Filter{f("Age", "eq", nil), f("Age", "eq", json.RawMessage("null")), fin("Age", 1, nil)} {
			_, e := r.s.Query(ctx, idA, store.Query{Table: "People", Filter: bad})
			Expect(e).To(MatchError(store.ErrInvalidQuery))
		}
	})

	It("sorts with a stable row ID tie-break and pages through a cursor that stops at a revision change", func() {
		r := newRowsRig(store.Options{})
		var rows []map[string]any
		for i := 0; i < 23; i++ {
			rows = append(rows, map[string]any{"Name": fmt.Sprintf("n%02d", i), "Age": i % 3})
		}
		r.insert(rows...)
		var got []int64
		q := store.Query{Sort: []store.SortKey{{Column: "Age", Desc: true}}, Limit: 5}
		pages := 0
		for {
			p := r.query(q)
			pages++
			for _, row := range p.Rows {
				got = append(got, row.ID)
			}
			if !p.HasMore {
				Expect(p.Cursor).To(BeEmpty())
				break
			}
			Expect(p.Rows).To(HaveLen(5))
			q.Cursor = p.Cursor
		}
		Expect(pages).To(Equal(5))
		Expect(got).To(HaveLen(23))
		want := make([]int64, 23)
		for i := range want {
			want[i] = int64(i + 1)
		}
		sort.SliceStable(want, func(i, j int) bool { return (want[i]-1)%3 > (want[j]-1)%3 })
		Expect(got).To(Equal(want))
		// A cursor taken before a change is refused after it, and under a different query.
		first := r.query(store.Query{Limit: 5})
		other := store.Query{Table: "People", Limit: 5, Cursor: first.Cursor, Filter: f("Age", "eq", 1)}
		_, e := r.s.Query(ctx, idA, other)
		Expect(e).To(MatchError(store.ErrStaleCursor))
		r.insert(map[string]any{"Name": "late"})
		_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Limit: 5, Cursor: first.Cursor})
		Expect(e).To(MatchError(store.ErrStaleCursor))
		_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Cursor: "not-a-cursor"})
		Expect(e).To(MatchError(store.ErrInvalidQuery))
		// NULLs sort first ascending and last descending.
		r.insert(map[string]any{"Name": "nullage"})
		asc := r.query(store.Query{Sort: []store.SortKey{{Column: "Age"}}, Columns: []string{"Name"}, Limit: 1})
		Expect(asc.Rows[0].Values[0]).To(Equal("late"))
		desc := r.query(store.Query{Sort: []store.SortKey{{Column: "Age", Desc: true}}, Columns: []string{"Name"}, Limit: store.MaxPage})
		Expect(desc.Rows[len(desc.Rows)-2].Values[0]).To(Equal("late"))
		Expect(desc.Rows[len(desc.Rows)-1].Values[0]).To(Equal("nullage"))
	})

	It("counts, sums, averages and bounds groups of aggregates", func() {
		r := newRowsRig(store.Options{})
		r.insert(
			map[string]any{"Name": "a", "Age": 10, "Score": 1.5, "Tag": "x"},
			map[string]any{"Name": "b", "Age": 20, "Score": 2.5, "Tag": "x"},
			map[string]any{"Name": "c", "Age": 30, "Tag": "y"},
			map[string]any{"Name": "d", "Tag": "y"},
			map[string]any{"Name": "e"},
		)
		all, e := r.s.Aggregate(ctx, idA, store.AggregateQuery{Table: "People", Aggregates: []store.Aggregation{
			{Fn: "count"}, {Fn: "count", Column: "Age"}, {Fn: "sum", Column: "Age"}, {Fn: "avg", Column: "Age"},
			{Fn: "min", Column: "Name"}, {Fn: "max", Column: "Score"}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(all.Groups).To(HaveLen(1))
		Expect(all.Groups[0].Group).To(BeNil())
		Expect(all.Groups[0].Values).To(Equal([]any{int64(5), int64(3), int64(60), 20.0, "a", 2.5}))
		Expect(all.Aggregates).To(Equal([]string{"count", "count(Age)", "sum(Age)", "avg(Age)", "min(Name)", "max(Score)"}))
		by, e := r.s.Aggregate(ctx, idA, store.AggregateQuery{Table: "People", GroupBy: "Tag", Filter: f("Name", "ne", "e"),
			Aggregates: []store.Aggregation{{Fn: "count"}, {Fn: "sum", Column: "Age"}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(by.Groups).To(Equal([]store.AggregateGroup{
			{Group: []any{"x"}, Values: []any{int64(2), int64(30)}},
			{Group: []any{"y"}, Values: []any{int64(2), int64(30)}},
		}))
		nul, e := r.s.Aggregate(ctx, idA, store.AggregateQuery{Table: "People", GroupBy: "Tag", Aggregates: []store.Aggregation{{Fn: "count"}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(nul.Groups[0]).To(Equal(store.AggregateGroup{Group: []any{nil}, Values: []any{int64(1)}}))
		empty, e := r.s.Aggregate(ctx, idA, store.AggregateQuery{Table: "People", Filter: f("Name", "eq", "zzz"), Aggregates: []store.Aggregation{{Fn: "count"}, {Fn: "sum", Column: "Age"}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(empty.Groups[0].Values).To(Equal([]any{int64(0), nil}))
		lim, e := r.s.Aggregate(ctx, idA, store.AggregateQuery{Table: "People", GroupBy: "Name", Limit: 2, Aggregates: []store.Aggregation{{Fn: "count"}}})
		Expect(e).NotTo(HaveOccurred())
		Expect(lim.Groups).To(HaveLen(2))
		Expect(lim.Truncated).To(BeTrue())
		for _, bad := range []store.AggregateQuery{
			{Table: "People", Aggregates: []store.Aggregation{{Fn: "sum", Column: "Name"}}},
			{Table: "People", Aggregates: []store.Aggregation{{Fn: "median", Column: "Age"}}},
			{Table: "People", Aggregates: []store.Aggregation{{Fn: "sum"}}},
			{Table: "People", Aggregates: []store.Aggregation{{Fn: "max", Column: "Active"}}},
			{Table: "People"},
			{Table: "People", GroupBy: "nope", Aggregates: []store.Aggregation{{Fn: "count"}}},
			{Table: "People", Aggregates: []store.Aggregation{{Fn: "count"}}, Limit: store.MaxGroups + 1},
		} {
			_, e = r.s.Aggregate(ctx, idA, bad)
			Expect(e).To(HaveOccurred())
		}
		// An overflowing sum is a typed error, not a wrapped number.
		r.insert(map[string]any{"Name": "big1", "Age": "9223372036854775807"}, map[string]any{"Name": "big2", "Age": 5})
		_, e = r.s.Aggregate(ctx, idA, store.AggregateQuery{Table: "People", Aggregates: []store.Aggregation{{Fn: "sum", Column: "Age"}}})
		Expect(e).To(MatchError(store.ErrArithmetic))
	})

	It("reports a real sum or average that overflows as the typed arithmetic error", func() {
		r := newRowsRig(store.Options{})
		r.insert(map[string]any{"Name": "a", "Score": 1.7e308}, map[string]any{"Name": "b", "Score": 1.7e308})
		for _, fn := range []string{"sum", "avg"} {
			_, e := r.s.Aggregate(ctx, idA, store.AggregateQuery{Table: "People", Aggregates: []store.Aggregation{{Fn: fn, Column: "Score"}}})
			Expect(e).To(MatchError(store.ErrArithmetic), fn)
		}
	})

	It("never lets a label, operator or value reach SQL as text", func() {
		r := newRowsRig(store.Options{})
		r.insert(map[string]any{"Name": "a"}, map[string]any{"Name": "b"})
		hostile := []string{`Name"; DROP TABLE t; --`, "Name) OR 1=1 --", "t_abc", "c_abc", "row_id", "sqlite_master", "*", "Name,Age", "", "1", " Name"}
		for _, h := range hostile {
			_, e := r.s.Query(ctx, idA, store.Query{Table: "People", Columns: []string{h}})
			Expect(e).To(HaveOccurred(), h)
			_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Filter: f(h, "eq", 1)})
			Expect(e).To(HaveOccurred(), h)
			_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Sort: []store.SortKey{{Column: h}}})
			Expect(e).To(HaveOccurred(), h)
			_, e = r.s.Query(ctx, idA, store.Query{Table: h})
			Expect(e).To(HaveOccurred(), h)
			if h == "" { // an empty group-by means ungrouped
				continue
			}
			_, e = r.s.Aggregate(ctx, idA, store.AggregateQuery{Table: "People", GroupBy: h, Aggregates: []store.Aggregation{{Fn: "count"}}})
			Expect(e).To(HaveOccurred(), h)
		}
		for _, op := range []string{"=", "eq OR 1=1", "like", "EQ", "regexp", "match", "glob", "not", ""} {
			_, e := r.s.Query(ctx, idA, store.Query{Table: "People", Filter: &store.Filter{Column: "Name", Op: op, Value: jv("a")}})
			Expect(e).To(HaveOccurred(), op)
		}
		// A value is data: a quote, comment or SQL text is just a string that matches nothing.
		for _, v := range []string{"a' OR '1'='1", "a'; DROP TABLE People; --", "%", "\x00"} {
			_, e := r.s.Query(ctx, idA, store.Query{Table: "People", Filter: f("Name", "eq", v)})
			if e == nil {
				Expect(r.names(store.Query{Filter: f("Name", "eq", v)})).To(BeEmpty())
			}
		}
		Expect(r.names(store.Query{})).To(Equal([]string{"a", "b"}))
		// Typed values: a string for an integer column, an object, an array, a fraction.
		for _, v := range []any{"abc", map[string]any{"x": 1}, []any{1}, 1.5, true} {
			_, e := r.s.Query(ctx, idA, store.Query{Table: "People", Filter: f("Age", "eq", v)})
			Expect(e).To(MatchError(store.ErrInvalidQuery), fmt.Sprint(v))
		}
		// Shape: both and/or, neither, children on a leaf, empty lists.
		for _, bad := range []*store.Filter{
			{And: []store.Filter{*f("Age", "eq", 1)}, Or: []store.Filter{*f("Age", "eq", 1)}},
			{},
			{And: []store.Filter{}},
			{And: []store.Filter{*f("Age", "eq", 1)}, Column: "Age"},
			{Column: "Age"},
			{Op: "eq", Value: jv(1)},
			fin("Age"),
			{Column: "Age", Op: "is-null", Value: jv(1)},
			f("Active", "lt", true),
			f("Age", "contains", "1"),
		} {
			_, e := r.s.Query(ctx, idA, store.Query{Table: "People", Filter: bad})
			Expect(e).To(HaveOccurred())
		}
		// Duplicates in select and sort, and the reserved id label.
		_, e := r.s.Query(ctx, idA, store.Query{Table: "People", Columns: []string{"Name", "name"}})
		Expect(e).To(MatchError(store.ErrInvalidQuery))
		_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Sort: []store.SortKey{{Column: "Name"}, {Column: "NAME", Desc: true}}})
		Expect(e).To(MatchError(store.ErrInvalidQuery))
		_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Columns: []string{"_id"}})
		Expect(e).To(MatchError(store.ErrInvalidQuery))
		_, e = r.s.Query(ctx, idA, store.Query{Table: "Missing"})
		Expect(e).To(MatchError(store.ErrTableNotFound))
		_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Columns: []string{"Nope"}})
		Expect(e).To(MatchError(store.ErrColumnNotFound))
	})

	It("honours hard limits on page size, filter depth, conditions, in-lists, sort keys and result bytes", func() {
		r := newRowsRig(store.Options{})
		var rows []map[string]any
		for i := 0; i < 520; i++ {
			rows = append(rows, map[string]any{"Name": fmt.Sprint(i)})
			if len(rows) == 100 {
				r.insert(rows...)
				rows = nil
			}
		}
		r.insert(rows...)
		Expect(r.query(store.Query{}).Rows).To(HaveLen(store.DefaultPage))
		Expect(r.query(store.Query{Limit: store.MaxPage}).Rows).To(HaveLen(store.MaxPage))
		Expect(r.query(store.Query{Limit: store.MaxPage}).HasMore).To(BeTrue())
		for _, l := range []int{-1, store.MaxPage + 1, 100000} {
			_, e := r.s.Query(ctx, idA, store.Query{Table: "People", Limit: l})
			Expect(e).To(MatchError(store.ErrInvalidQuery), fmt.Sprint(l))
		}
		deep := f("Age", "eq", 1)
		for i := 0; i < store.MaxFilterDepth-1; i++ {
			deep = and(deep)
		}
		_, e := r.s.Query(ctx, idA, store.Query{Table: "People", Filter: deep})
		Expect(e).NotTo(HaveOccurred())
		_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Filter: and(deep)})
		Expect(e).To(MatchError(store.ErrInvalidQuery))
		var wide []*store.Filter
		for i := 0; i < store.MaxFilterNodes; i++ {
			wide = append(wide, f("Age", "eq", i))
		}
		_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Filter: or(wide...)})
		Expect(e).To(MatchError(store.ErrInvalidQuery))
		_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Filter: or(wide[:store.MaxFilterNodes-2]...)})
		Expect(e).NotTo(HaveOccurred())
		var in []any
		for i := 0; i <= store.MaxInValues; i++ {
			in = append(in, i)
		}
		_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Filter: fin("Age", in...)})
		Expect(e).To(MatchError(store.ErrInvalidQuery))
		_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Filter: fin("Age", in[:store.MaxInValues]...)})
		Expect(e).NotTo(HaveOccurred())
		_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Sort: []store.SortKey{{Column: "Name"}, {Column: "Age"}, {Column: "Score"}, {Column: "Born"}, {Column: "Tag"}}})
		Expect(e).To(MatchError(store.ErrInvalidQuery))
		long := strings.Repeat("x", store.MaxFilterText+1)
		_, e = r.s.Query(ctx, idA, store.Query{Table: "People", Filter: f("Name", "contains", long)})
		Expect(e).To(MatchError(store.ErrInvalidQuery))
		// A page is cut at the byte bound and continues through its cursor.
		big := strings.Repeat("z", 60000)
		for i := 0; i < 20; i++ {
			r.insert(map[string]any{"Name": big})
		}
		page := r.query(store.Query{Filter: f("Name", "eq", big), Limit: store.MaxPage})
		Expect(page.HasMore).To(BeTrue())
		Expect(len(page.Rows)).To(BeNumerically("<", 20))
		Expect(len(page.Rows)).To(BeNumerically(">", 0))
		total := len(page.Rows)
		for page.HasMore {
			page = r.query(store.Query{Filter: f("Name", "eq", big), Limit: store.MaxPage, Cursor: page.Cursor})
			total += len(page.Rows)
		}
		Expect(total).To(Equal(20))
	})

	It("stops a query when the context is cancelled or the deadline passes", func() {
		r := newRowsRig(store.Options{})
		id := r.tableID("People")
		nameCol := r.columnID("People", "Name")
		tx, e := r.raw().Begin()
		Expect(e).NotTo(HaveOccurred())
		for i := 0; i < 60000; i++ {
			_, e = tx.Exec("INSERT INTO "+id+"("+nameCol+") VALUES(?)", fmt.Sprintf("name-%d-%d", i*7919%60000, i))
			Expect(e).NotTo(HaveOccurred())
		}
		Expect(tx.Commit()).To(Succeed())
		done, cancel := context.WithCancel(ctx)
		cancel()
		_, e = r.s.Query(done, idA, store.Query{Table: "People"})
		Expect(e).To(MatchError(context.Canceled))
		_, e = r.s.Aggregate(done, idA, store.AggregateQuery{Table: "People", Aggregates: []store.Aggregation{{Fn: "count"}}})
		Expect(e).To(MatchError(context.Canceled))
		// A store with a one-millisecond operation deadline cannot finish a sort of 60k rows.
		Expect(r.s.Close()).To(Succeed())
		slow := open(r.dir, store.Options{OpTimeout: time.Millisecond})
		_, e = slow.Query(ctx, idA, store.Query{Table: "People", Filter: f("Name", "contains", "9"), Sort: []store.SortKey{{Column: "Name", Desc: true}}, Limit: store.MaxPage})
		Expect(e).To(HaveOccurred())
		Expect(errors.Is(e, context.DeadlineExceeded) || strings.Contains(e.Error(), "interrupt")).To(BeTrue(), e.Error())
		// The same query under the default deadline completes.
		ok := open(r.dir, store.Options{})
		p, e := ok.Query(ctx, idA, store.Query{Table: "People", Sort: []store.SortKey{{Column: "Name", Desc: true}}, Limit: 3})
		Expect(e).NotTo(HaveOccurred())
		Expect(p.Rows).To(HaveLen(3))
	})

	It("encodes all five types as JSON and writes 64-bit integers as strings beyond 2^53", func() {
		r := newRowsRig(store.Options{})
		r.insert(map[string]any{"Name": "edge", "Age": "9007199254740993", "Score": 1e300, "Active": false, "Born": "2026-10-14T12:00:00.5+02:00"})
		r.insert(map[string]any{"Name": "safe", "Age": 9007199254740992})
		r.insert(map[string]any{"Name": "neg", "Age": "-9223372036854775808"})
		p := r.query(store.Query{Columns: []string{"Name", "Age", "Score", "Active", "Born"}})
		Expect(p.Rows[0].Values).To(Equal([]any{"edge", "9007199254740993", 1e300, false, "2026-10-14T10:00:00.5Z"}))
		Expect(p.Rows[1].Values[1]).To(Equal(int64(9007199254740992)))
		Expect(p.Rows[2].Values[1]).To(Equal("-9223372036854775808"))
		b, e := json.Marshal(p)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"9007199254740993"`))
		// The big value filters exactly, as a string or a number.
		Expect(r.names(store.Query{Filter: f("Age", "eq", "9007199254740993")})).To(Equal([]string{"edge"}))
		Expect(r.names(store.Query{Filter: f("Age", "gt", 9007199254740992)})).To(Equal([]string{"edge"}))
	})

	It("orders datetimes chronologically across fractional seconds and offsets", func() {
		r := newRowsRig(store.Options{})
		r.insert(
			map[string]any{"Name": "whole", "Born": "2026-01-01T10:00:00Z"},
			map[string]any{"Name": "half", "Born": "2026-01-01T10:00:00.5Z"},
			map[string]any{"Name": "offset", "Born": "2026-01-01T12:00:00+02:00"},
			map[string]any{"Name": "later", "Born": "2026-01-01T10:00:01Z"},
		)
		Expect(r.names(store.Query{Sort: []store.SortKey{{Column: "Born"}}})).To(Equal([]string{"whole", "offset", "half", "later"}))
		Expect(r.names(store.Query{Filter: f("Born", "gt", "2026-01-01T10:00:00Z"), Sort: []store.SortKey{{Column: "Born"}}})).To(Equal([]string{"half", "later"}))
		Expect(r.names(store.Query{Filter: f("Born", "eq", "2026-01-01T10:00:00Z")})).To(Equal([]string{"whole", "offset"}))
	})
})

var _ = contract.Revision{}
