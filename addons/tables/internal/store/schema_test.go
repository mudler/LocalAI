// SPDX-License-Identifier: MIT
package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// schemaRig is one database with a helper to apply and read it.
type schemaRig struct {
	dir string
	s   *store.Store
	rev contract.Revision
	n   int
}

func newSchemaRig(opts store.Options) *schemaRig {
	GinkgoHelper()
	dir := filepath.Join(GinkgoT().TempDir(), "tables")
	r := &schemaRig{dir: dir, s: open(dir, opts)}
	info, e := r.s.Create(context.Background(), idA, "Sales", "op-create-a")
	Expect(e).NotTo(HaveOccurred())
	r.rev = info.Revision
	return r
}

func (r *schemaRig) op() string { r.n++; return fmt.Sprintf("op-schema-%d", r.n) }

func (r *schemaRig) do(c store.SchemaChange) (contract.Receipt, error) {
	c.ResourceID = idA
	rc, e := r.s.ApplySchema(context.Background(), c, r.rev, r.op())
	if e == nil {
		r.rev = rc.Revision
	}
	return rc, e
}

func (r *schemaRig) must(c store.SchemaChange) contract.Receipt {
	GinkgoHelper()
	rc, e := r.do(c)
	Expect(e).NotTo(HaveOccurred())
	Expect(rc.State).To(Equal(contract.ReceiptCommitted))
	return rc
}

func (r *schemaRig) inspect() store.Schema {
	GinkgoHelper()
	s, e := r.s.Inspect(context.Background(), idA)
	Expect(e).NotTo(HaveOccurred())
	return s
}

func (r *schemaRig) raw() *sql.DB {
	GinkgoHelper()
	db, e := sql.Open("sqlite3", filepath.Join(r.dir, "databases", idA+".db"))
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = db.Close() })
	return db
}

func (r *schemaRig) tableID(name string) string {
	GinkgoHelper()
	for _, t := range r.inspect().Detail {
		if t.Name == name {
			return t.ID
		}
	}
	Fail("no table " + name)
	return ""
}

func (r *schemaRig) columnID(table, col string) string {
	GinkgoHelper()
	for _, t := range r.inspect().Detail {
		if t.Name == table {
			for _, c := range t.Columns {
				if c.Name == col {
					return c.ID
				}
			}
		}
	}
	Fail("no column " + col)
	return ""
}

func tc(table string) store.SchemaChange {
	return store.SchemaChange{Kind: store.KindTableCreate, Table: table}
}
func ca(table, col, typ string) store.SchemaChange {
	return store.SchemaChange{Kind: store.KindColumnAdd, Table: table, Column: col, Type: typ}
}

func summary(p contract.Preview) (count int, sample []map[string]any, loss map[string]float64) {
	GinkgoHelper()
	var s struct {
		Count    int                `json:"count"`
		Sample   []map[string]any   `json:"sample"`
		DataLoss map[string]float64 `json:"dataLoss"`
	}
	Expect(json.Unmarshal(p.Summary, &s)).To(Succeed())
	return s.Count, s.Sample, s.DataLoss
}

var _ = Describe("Tables typed schema operations", func() {
	ctx := context.Background()

	It("creates, renames and drops tables with stable internal identifiers and one revision step each", func() {
		r := newSchemaRig(store.Options{})
		start := r.rev
		rc := r.must(tc("Orders"))
		Expect(rc.Revision.Schema).To(Equal(start.Schema + 1))
		Expect(rc.Revision.Data).To(Equal(start.Data))
		id := r.tableID("Orders")
		Expect(id).To(MatchRegexp(`^t_[0-9a-f]{24}$`))
		r.must(store.SchemaChange{Kind: store.KindTableRename, Table: "orders", NewName: "Sales orders"})
		Expect(r.tableID("Sales orders")).To(Equal(id))
		Expect(r.inspect().Tables).To(Equal([]string{"Sales orders"}))
		Expect(r.inspect().Revision).To(Equal(r.rev))
		// The physical table is the internal ID; the label is never SQL.
		var n int
		Expect(r.raw().QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name=?", id).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(1))
		r.must(store.SchemaChange{Kind: store.KindTableDrop, Table: "SALES ORDERS"})
		Expect(r.inspect().Tables).To(BeEmpty())
		Expect(r.raw().QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name=?", id).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(0))
	})

	It("adds typed columns, renames them without touching identifiers and never uses a label in SQL", func() {
		r := newSchemaRig(store.Options{})
		hostile := `x"; DROP TABLE tables_meta; --`
		r.must(tc(hostile))
		for _, ty := range []string{"text", "integer", "real", "boolean", "datetime"} {
			r.must(ca(hostile, "col "+ty, ty))
		}
		before := r.columnID(hostile, "col integer")
		r.must(store.SchemaChange{Kind: store.KindColumnRename, Table: hostile, Column: "COL INTEGER", NewName: "Quantity"})
		Expect(r.columnID(hostile, "Quantity")).To(Equal(before))
		Expect(before).To(MatchRegexp(`^c_[0-9a-f]{24}$`))
		var sqlText string
		rows, e := r.raw().Query("SELECT COALESCE(sql,'') FROM sqlite_master WHERE name LIKE 't\\_%' ESCAPE '\\'")
		Expect(e).NotTo(HaveOccurred())
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			Expect(rows.Scan(&sqlText)).To(Succeed())
			Expect(sqlText).NotTo(ContainSubstring("DROP"))
			Expect(sqlText).NotTo(ContainSubstring("Quantity"))
		}
		var meta int
		Expect(r.raw().QueryRow("SELECT COUNT(*) FROM tables_meta").Scan(&meta)).To(Succeed())
		Expect(meta).To(BeNumerically(">", 0))
	})

	It("validates labels by length, characters and case-insensitive uniqueness", func() {
		r := newSchemaRig(store.Options{})
		r.must(tc("Customers"))
		for _, bad := range []string{"", " Leading", "Trailing ", strings.Repeat("a", 65), "tab\tname", "nul\x00name", "bidi‮name", "zero​width", "line sep", "bad\xffutf8"} {
			_, e := r.do(tc(bad))
			Expect(e).To(MatchError(store.ErrInvalidSchema), "%q", bad)
		}
		_, e := r.do(tc("CUSTOMERS"))
		Expect(e).To(MatchError(store.ErrSchemaExists))
		r.must(ca("Customers", "Email", "text"))
		_, e = r.do(ca("Customers", "email", "text"))
		Expect(e).To(MatchError(store.ErrSchemaExists))
		_, e = r.do(ca("Missing", "x", "text"))
		Expect(e).To(MatchError(store.ErrTableNotFound))
		_, e = r.do(store.SchemaChange{Kind: store.KindColumnDrop, Table: "Customers", Column: "Nope"})
		Expect(e).To(MatchError(store.ErrColumnNotFound))
		// A rename may change only the case of the same label.
		r.must(store.SchemaChange{Kind: store.KindTableRename, Table: "Customers", NewName: "CUSTOMERS"})
	})

	It("refuses type changes, unknown operations, expressions and unknown types", func() {
		r := newSchemaRig(store.Options{})
		r.must(tc("T"))
		for _, c := range []store.SchemaChange{
			{Kind: "column-type", Table: "T", Column: "x", Type: "integer"},
			{Kind: "trigger-create", Table: "T"},
			{Kind: "raw-sql", Table: "T"},
			ca("T", "x", "text; DROP TABLE tables_meta"),
			ca("T", "x", "varchar(10)"),
			ca("T", "x", "blob"),
			{Kind: store.KindColumnRename, Table: "T", Column: "x"},
			{Kind: store.KindTableDrop, Table: "T", Type: "text"},
			{Kind: store.KindTableCreate, Table: "T2", Column: "x"},
		} {
			_, e := r.do(c)
			Expect(e).To(MatchError(store.ErrInvalidSchema), "%+v", c)
		}
		Expect(r.rev.Schema).To(Equal(uint64(2)))
	})

	It("encodes typed defaults as literals and survives hostile defaults", func() {
		r := newSchemaRig(store.Options{})
		r.must(tc("T"))
		hostile := "'; DROP TABLE tables_meta; --"
		def := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
		add := func(name, ty string, d json.RawMessage) (contract.Receipt, error) {
			c := ca("T", name, ty)
			c.Default = d
			return r.do(c)
		}
		_, e := add("a", "text", def(hostile))
		Expect(e).NotTo(HaveOccurred())
		_, e = add("b", "text", def("it's \"quoted\" \\ 100% (SELECT 1)"))
		Expect(e).NotTo(HaveOccurred())
		_, e = add("c", "integer", def(int64(9223372036854775807)))
		Expect(e).NotTo(HaveOccurred())
		_, e = add("d", "real", json.RawMessage(`-1.5e300`))
		Expect(e).To(MatchError(store.ErrInvalidSchema), "SQLite does not parse this exactly")
		_, e = add("d", "real", json.RawMessage(`-0.1`))
		Expect(e).NotTo(HaveOccurred())
		_, e = add("e", "boolean", def(true))
		Expect(e).NotTo(HaveOccurred())
		_, e = add("f", "datetime", def("2026-10-14T12:00:00+02:00"))
		Expect(e).NotTo(HaveOccurred())
		_, e = add("g", "text", def("Unicode ✓ ünï"))
		Expect(e).NotTo(HaveOccurred())
		for name, v := range map[string]struct {
			ty string
			d  json.RawMessage
		}{
			"nul":        {"text", def("a\x00b")},
			"ctrl":       {"text", def("a\nb")},
			"huge":       {"text", def(strings.Repeat("x", store.MaxDefaultBytes+1))},
			"bigint":     {"integer", json.RawMessage(`9223372036854775808`)},
			"fracint":    {"integer", json.RawMessage(`1.5`)},
			"expint":     {"integer", json.RawMessage(`1e3`)},
			"strint":     {"integer", def("1; DROP TABLE x")},
			"inf":        {"real", json.RawMessage(`1e999`)},
			"nan":        {"real", json.RawMessage(`NaN`)},
			"strreal":    {"real", def("Infinity")},
			"intbool":    {"boolean", json.RawMessage(`1`)},
			"strbool":    {"boolean", def("true")},
			"baddate":    {"datetime", def("yesterday")},
			"sqldate":    {"datetime", def("2026-01-01'; DROP TABLE x; --")},
			"objdefault": {"text", json.RawMessage(`{"a":1}`)},
		} {
			rev := r.rev
			_, e := add("bad-"+name, v.ty, v.d)
			Expect(e).To(MatchError(store.ErrInvalidSchema), name)
			Expect(r.rev).To(Equal(rev))
		}
		// A row built from the defaults carries exactly the typed values.
		id := r.tableID("T")
		_, e = r.raw().Exec("INSERT INTO " + id + " DEFAULT VALUES")
		Expect(e).NotTo(HaveOccurred())
		row := func(col string) any {
			var v any
			Expect(r.raw().QueryRow("SELECT " + r.columnID("T", col) + " FROM " + id).Scan(&v)).To(Succeed())
			if b, ok := v.([]byte); ok {
				return string(b)
			}
			return v
		}
		Expect(row("a")).To(Equal(hostile))
		Expect(row("b")).To(Equal("it's \"quoted\" \\ 100% (SELECT 1)"))
		Expect(row("c")).To(Equal(int64(9223372036854775807)))
		Expect(row("d")).To(Equal(-0.1))
		Expect(row("e")).To(Equal(int64(1)))
		Expect(row("f")).To(Equal("2026-10-14T10:00:00.000000000Z"))
		Expect(row("g")).To(Equal("Unicode ✓ ünï"))
		var n int
		Expect(r.raw().QueryRow("SELECT COUNT(*) FROM tables_meta").Scan(&n)).To(Succeed())
		Expect(n).To(BeNumerically(">", 0))
	})

	It("enforces required columns and refuses unique defaults that would collide", func() {
		r := newSchemaRig(store.Options{})
		r.must(tc("T"))
		// An empty table takes a required column without a default.
		c := ca("T", "name", "text")
		c.Required = true
		r.must(c)
		id := r.tableID("T")
		col := r.columnID("T", "name")
		_, e := r.raw().Exec("INSERT INTO " + id + " DEFAULT VALUES")
		Expect(e).To(HaveOccurred(), "required column must refuse NULL")
		_, e = r.raw().Exec("INSERT INTO " + id + "(" + col + ") VALUES('a')")
		Expect(e).NotTo(HaveOccurred())
		_, e = r.raw().Exec("INSERT INTO " + id + "(" + col + ") VALUES('b')")
		Expect(e).NotTo(HaveOccurred())
		// With rows, a required column needs a default.
		c2 := ca("T", "qty", "integer")
		c2.Required = true
		rev := r.rev
		_, e = r.do(c2)
		Expect(e).To(MatchError(store.ErrInvalidSchema))
		Expect(r.rev).To(Equal(rev))
		c2.Default = json.RawMessage(`0`)
		r.must(c2)
		// A default repeated over several rows cannot be unique.
		c3 := ca("T", "code", "text")
		c3.Unique = true
		c3.Default = json.RawMessage(`"X"`)
		_, e = r.do(c3)
		Expect(e).To(MatchError(store.ErrInvalidSchema))
		Expect(r.rev.Schema).To(Equal(rev.Schema + 1))
	})

	It("adds a unique column through a unique index and refuses duplicates atomically", func() {
		r := newSchemaRig(store.Options{})
		r.must(tc("T"))
		u := ca("T", "email", "text")
		u.Unique = true
		r.must(u)
		id, col := r.tableID("T"), r.columnID("T", "email")
		_, e := r.raw().Exec("INSERT INTO " + id + "(" + col + ") VALUES('a@x')")
		Expect(e).NotTo(HaveOccurred())
		_, e = r.raw().Exec("INSERT INTO " + id + "(" + col + ") VALUES('a@x')")
		Expect(e).To(HaveOccurred(), "unique index must refuse a duplicate")
		_, e = r.raw().Exec("INSERT INTO " + id + " DEFAULT VALUES")
		Expect(e).NotTo(HaveOccurred())
		_, e = r.raw().Exec("INSERT INTO " + id + " DEFAULT VALUES")
		Expect(e).NotTo(HaveOccurred(), "NULLs are not duplicates")
	})

	It("drops a column by rebuilding the table and loses exactly what the preview reported", func() {
		r := newSchemaRig(store.Options{})
		r.must(tc("T"))
		r.must(ca("T", "keep", "text"))
		r.must(ca("T", "gone", "integer"))
		u := ca("T", "tag", "text")
		u.Unique = true
		r.must(u)
		id := r.tableID("T")
		keep, gone, tag := r.columnID("T", "keep"), r.columnID("T", "gone"), r.columnID("T", "tag")
		for i := 0; i < 5; i++ {
			v := any(nil)
			if i < 3 {
				v = i
			}
			_, e := r.raw().Exec("INSERT INTO "+id+"("+keep+","+gone+","+tag+") VALUES(?,?,?)", fmt.Sprint("k", i), v, fmt.Sprint("t", i))
			Expect(e).NotTo(HaveOccurred())
		}
		drop := store.SchemaChange{ResourceID: idA, Kind: store.KindColumnDrop, Table: "T", Column: "gone"}
		p, e := r.s.PreviewSchema(ctx, drop)
		Expect(e).NotTo(HaveOccurred())
		count, sample, loss := summary(p)
		Expect(count).To(Equal(1))
		Expect(sample[0]["action"]).To(Equal("drop column"))
		Expect(sample[0]["rowsLost"]).To(BeEquivalentTo(3))
		Expect(loss["rows"]).To(BeEquivalentTo(3))
		Expect(p.Revision).To(Equal(r.rev))
		r.must(drop)
		s := r.inspect()
		Expect(s.Detail[0].Columns).To(HaveLen(2))
		var cols []string
		rows, e := r.raw().Query("SELECT name FROM pragma_table_info('" + id + "') ORDER BY cid")
		Expect(e).NotTo(HaveOccurred())
		for rows.Next() {
			var n string
			Expect(rows.Scan(&n)).To(Succeed())
			cols = append(cols, n)
		}
		_ = rows.Close()
		Expect(cols).To(Equal([]string{"row_id", keep, tag}))
		var n int
		Expect(r.raw().QueryRow("SELECT COUNT(*) FROM " + id).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(5))
		var k string
		Expect(r.raw().QueryRow("SELECT " + keep + " FROM " + id + " WHERE row_id=4").Scan(&k)).To(Succeed())
		Expect(k).To(Equal("k3"))
		// The unique index survives the rebuild.
		_, e = r.raw().Exec("INSERT INTO " + id + "(" + tag + ") VALUES('t1')")
		Expect(e).To(HaveOccurred())
	})

	It("never hands out a row ID again after a column drop rebuilds the table", func() {
		r := newSchemaRig(store.Options{})
		r.must(tc("T"))
		r.must(ca("T", "keep", "text"))
		r.must(ca("T", "gone", "text"))
		u := ca("T", "tag", "text")
		u.Unique = true
		r.must(u)
		id := r.tableID("T")
		keep, tag := r.columnID("T", "keep"), r.columnID("T", "tag")
		for i := 1; i <= 3; i++ {
			_, e := r.raw().Exec("INSERT INTO "+id+"("+keep+","+tag+") VALUES(?,?)", fmt.Sprint("k", i), fmt.Sprint("t", i))
			Expect(e).NotTo(HaveOccurred())
		}
		_, e := r.raw().Exec("DELETE FROM " + id + " WHERE row_id=3")
		Expect(e).NotTo(HaveOccurred())
		r.must(store.SchemaChange{Kind: store.KindColumnDrop, Table: "T", Column: "gone"})
		// The surviving rows keep their IDs, the unique index still holds.
		var ids string
		Expect(r.raw().QueryRow("SELECT group_concat(row_id) FROM (SELECT row_id FROM " + id + " ORDER BY row_id)").Scan(&ids)).To(Succeed())
		Expect(ids).To(Equal("1,2"))
		_, e = r.raw().Exec("INSERT INTO " + id + "(" + tag + ") VALUES('t1')")
		Expect(e).To(HaveOccurred())
		res, e := r.raw().Exec("INSERT INTO " + id + "(" + keep + "," + tag + ") VALUES('k4','t4')")
		Expect(e).NotTo(HaveOccurred())
		last, e := res.LastInsertId()
		Expect(e).NotTo(HaveOccurred())
		Expect(last).To(BeEquivalentTo(4))
	})

	It("keeps the row ID counter when a column drop rebuilds a table emptied by deletes", func() {
		r := newSchemaRig(store.Options{})
		r.must(tc("T"))
		r.must(ca("T", "keep", "text"))
		r.must(ca("T", "gone", "text"))
		id, keep := r.tableID("T"), r.columnID("T", "keep")
		for i := 0; i < 3; i++ {
			_, e := r.raw().Exec("INSERT INTO " + id + "(" + keep + ") VALUES('v')")
			Expect(e).NotTo(HaveOccurred())
		}
		_, e := r.raw().Exec("DELETE FROM " + id)
		Expect(e).NotTo(HaveOccurred())
		r.must(store.SchemaChange{Kind: store.KindColumnDrop, Table: "T", Column: "gone"})
		res, e := r.raw().Exec("INSERT INTO " + id + "(" + keep + ") VALUES('w')")
		Expect(e).NotTo(HaveOccurred())
		last, e := res.LastInsertId()
		Expect(e).NotTo(HaveOccurred())
		Expect(last).To(BeEquivalentTo(4))
	})

	It("leaves the original table, data and revision intact when a rebuild fails midway", func() {
		r := newSchemaRig(store.Options{})
		r.must(tc("T"))
		r.must(ca("T", "a", "text"))
		r.must(ca("T", "b", "text"))
		id, a := r.tableID("T"), r.columnID("T", "a")
		_, e := r.raw().Exec("INSERT INTO " + id + "(" + a + ") VALUES('v')")
		Expect(e).NotTo(HaveOccurred())
		before := r.inspect()
		r.s.SetFailpoint(func(p string) error {
			if p == store.FailRebuildCopy {
				return errors.New("injected failure after the copy")
			}
			return nil
		})
		_, e = r.do(store.SchemaChange{Kind: store.KindColumnDrop, Table: "T", Column: "b"})
		Expect(e).To(MatchError(ContainSubstring("injected")))
		r.s.SetFailpoint(nil)
		Expect(r.inspect()).To(Equal(before))
		var v string
		Expect(r.raw().QueryRow("SELECT " + a + " FROM " + id).Scan(&v)).To(Succeed())
		Expect(v).To(Equal("v"))
		var leftovers int
		Expect(r.raw().QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE 't\\_%' ESCAPE '\\'").Scan(&leftovers)).To(Succeed())
		Expect(leftovers).To(Equal(1))
		// The same change then succeeds.
		r.must(store.SchemaChange{Kind: store.KindColumnDrop, Table: "T", Column: "b"})
	})

	It("previews exact effects and dependent data loss and changes nothing", func() {
		r := newSchemaRig(store.Options{})
		r.must(tc("T"))
		r.must(ca("T", "a", "text"))
		r.must(ca("T", "b", "text"))
		id := r.tableID("T")
		for i := 0; i < 4; i++ {
			_, e := r.raw().Exec("INSERT INTO " + id + " DEFAULT VALUES")
			Expect(e).NotTo(HaveOccurred())
		}
		before := r.inspect()
		p, e := r.s.PreviewSchema(ctx, store.SchemaChange{ResourceID: idA, Kind: store.KindTableDrop, Table: "t"})
		Expect(e).NotTo(HaveOccurred())
		count, sample, loss := summary(p)
		Expect(count).To(Equal(1))
		Expect(sample[0]).To(HaveKeyWithValue("action", "drop table"))
		Expect(sample[0]).To(HaveKeyWithValue("rowsLost", BeEquivalentTo(4)))
		Expect(sample[0]).To(HaveKeyWithValue("columnsLost", BeEquivalentTo(2)))
		Expect(loss).To(HaveKeyWithValue("rows", BeEquivalentTo(4)))
		Expect(loss).To(HaveKeyWithValue("tables", BeEquivalentTo(1)))
		Expect(p.Revision).To(Equal(r.rev))
		p, e = r.s.PreviewSchema(ctx, store.SchemaChange{ResourceID: idA, Kind: store.KindTableRename, Table: "T", NewName: "U"})
		Expect(e).NotTo(HaveOccurred())
		_, _, loss = summary(p)
		Expect(loss).To(HaveKeyWithValue("rows", BeEquivalentTo(0)))
		// A preview that apply would refuse is refused.
		_, e = r.s.PreviewSchema(ctx, store.SchemaChange{ResourceID: idA, Kind: store.KindTableCreate, Table: "T"})
		Expect(e).To(MatchError(store.ErrSchemaExists))
		Expect(r.inspect()).To(Equal(before))
		var n int
		Expect(r.raw().QueryRow("SELECT COUNT(*) FROM " + id).Scan(&n)).To(Succeed())
		Expect(n).To(Equal(4))
	})

	It("refuses a stale expected revision with the current revision and applies nothing", func() {
		r := newSchemaRig(store.Options{})
		old := r.rev
		r.must(tc("A"))
		_, e := r.s.ApplySchema(ctx, store.SchemaChange{ResourceID: idA, Kind: store.KindTableCreate, Table: "B"}, old, "op-stale")
		Expect(e).To(MatchError(store.ErrStale))
		var stale *store.StaleError
		Expect(errors.As(e, &stale)).To(BeTrue())
		Expect(stale.Current).To(Equal(r.rev))
		Expect(r.inspect().Tables).To(Equal([]string{"A"}))
		// A data revision that moved is stale too, because drops depend on rows.
		_, e = r.s.ApplySchema(ctx, store.SchemaChange{ResourceID: idA, Kind: store.KindTableCreate, Table: "B"},
			contract.Revision{Schema: r.rev.Schema, Data: r.rev.Data + 1}, "op-stale-data")
		Expect(e).To(MatchError(store.ErrStale))
		// The refused ID is free for a fresh attempt.
		_, e = r.s.ApplySchema(ctx, store.SchemaChange{ResourceID: idA, Kind: store.KindTableCreate, Table: "B"}, r.rev, "op-stale")
		Expect(e).NotTo(HaveOccurred())
	})

	It("keeps finite limits on tables and columns", func() {
		r := newSchemaRig(store.Options{})
		for i := 0; i < store.MaxTables; i++ {
			r.must(tc(fmt.Sprintf("t%d", i)))
		}
		_, e := r.do(tc("one too many"))
		Expect(e).To(MatchError(store.ErrSchemaLimit))
		for i := 0; i < store.MaxColumns; i++ {
			r.must(ca("t0", fmt.Sprintf("c%d", i), "text"))
		}
		_, e = r.do(ca("t0", "one too many", "text"))
		Expect(e).To(MatchError(store.ErrSchemaLimit))
		Expect(r.inspect().Tables).To(HaveLen(store.MaxTables))
	})

	It("migrates a database made before the catalog existed on its first schema change", func() {
		r := newSchemaRig(store.Options{})
		for _, t := range []string{"tables_catalog_columns", "tables_catalog_tables", "tables_schema_receipts"} {
			_, e := r.raw().Exec("DROP TABLE " + t)
			Expect(e).NotTo(HaveOccurred())
		}
		Expect(r.inspect().Tables).To(BeEmpty())
		r.must(tc("Late"))
		Expect(r.inspect().Tables).To(Equal([]string{"Late"}))
	})
})
