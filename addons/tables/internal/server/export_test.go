// SPDX-License-Identifier: MIT
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/tables/internal/server"
	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const dbID = "db-one"

type exportRig struct {
	t     *server.Tables
	dir   string
	clock time.Time
}

func newExportRig() *exportRig {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	st, e := store.Open(filepath.Join(root, "tables"), store.Options{})
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = st.Close() })
	ctx := context.Background()
	info, e := st.Create(ctx, dbID, "Sales", "op-create")
	Expect(e).NotTo(HaveOccurred())
	rev := info.Revision
	n := 0
	step := func(c store.SchemaChange) {
		GinkgoHelper()
		c.ResourceID = dbID
		n++
		r, e := st.ApplySchema(ctx, c, rev, fmt.Sprintf("op-s-%d", n))
		Expect(e).NotTo(HaveOccurred())
		rev = r.Revision
	}
	step(store.SchemaChange{Kind: store.KindTableCreate, Table: "People"})
	step(store.SchemaChange{Kind: store.KindColumnAdd, Table: "People", Column: "Name", Type: "text"})
	step(store.SchemaChange{Kind: store.KindColumnAdd, Table: "People", Column: "Age", Type: "integer"})
	_, e = st.ApplyRows(ctx, store.RowChange{ResourceID: dbID, Kind: store.KindRowInsert, Table: "People", Rows: []map[string]json.RawMessage{
		{"Name": json.RawMessage(`"=HYPERLINK(1)"`), "Age": json.RawMessage(`-3`)},
		{"Name": json.RawMessage(`"Zoë, \"Z\""`), "Age": json.RawMessage(`41`)},
	}}, rev, "op-rows")
	Expect(e).NotTo(HaveOccurred())
	r := &exportRig{dir: filepath.Join(root, "exports"), clock: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	r.t = &server.Tables{Store: st, ExportDir: r.dir, Now: func() time.Time { return r.clock }}
	return r
}

func (r *exportRig) env(user, op, scope, action string, args map[string]any) contract.Envelope {
	b, _ := json.Marshal(args)
	return contract.Envelope{
		Request:   contract.Request{AddonID: "tables", ResourceID: dbID, OperationID: op, Generation: 1, Action: action, Arguments: b},
		Authority: contract.Authority{Actor: contract.Actor{UserID: user, CredentialID: "session:" + user, Mode: "named-user"}, Scope: scope, ExpiresAt: time.Now().Add(time.Minute)},
	}
}

func (r *exportRig) export(user, op string, extra map[string]any) (contract.Result, error) {
	args := map[string]any{"tool": "rows-export", "table": "People"}
	for k, v := range extra {
		args[k] = v
	}
	return r.t.Read(context.Background(), r.env(user, op, "read", "read", args))
}

func (r *exportRig) open(user, op, handle string) ([]byte, error) {
	rc, e := r.t.OpenOutput(context.Background(), r.env(user, op, "output", "read", map[string]any{"tool": "rows-export", "table": "People"}), handle)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func (r *exportRig) files() []string {
	entries, _ := os.ReadDir(r.dir)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

var _ = Describe("rows-export outputs", func() {
	It("answers with one operation-bound CSV output whose bytes the handle streams exactly once", func() {
		r := newExportRig()
		res, e := r.export("alice", "op-1", nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(res.Outputs).To(HaveLen(1))
		o := res.Outputs[0]
		Expect(o.Name).To(Equal("People.csv"))
		Expect(o.MediaType).To(Equal("text/csv"))
		Expect(res.Validate(contract.DefaultLimits())).To(Succeed())
		want := "Name,Age\r\n'=HYPERLINK(1),-3\r\n\"Zoë, \"\"Z\"\"\",41\r\n"
		Expect(o.Size).To(Equal(int64(len(want))))
		Expect(r.files()).To(HaveLen(1))

		got, e := r.open("alice", "op-1", o.Handle)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal(want))
		// A completed stream removes the staged file and the handle.
		Expect(r.files()).To(BeEmpty())
		_, e = r.open("alice", "op-1", o.Handle)
		Expect(errors.Is(e, protocol.ErrNotFound)).To(BeTrue())
	})

	It("refuses a handle from another user, another operation, another database or after expiry, and removes expired files", func() {
		r := newExportRig()
		res, e := r.export("alice", "op-1", nil)
		Expect(e).NotTo(HaveOccurred())
		h := res.Outputs[0].Handle
		_, e = r.open("bob", "op-1", h)
		Expect(errors.Is(e, protocol.ErrNotFound)).To(BeTrue(), "another user")
		_, e = r.open("alice", "op-2", h)
		Expect(errors.Is(e, protocol.ErrNotFound)).To(BeTrue(), "another operation")
		env := r.env("alice", "op-1", "output", "read", map[string]any{"tool": "rows-export", "table": "People"})
		env.Request.ResourceID = "db-two"
		_, e = r.t.OpenOutput(context.Background(), env, h)
		Expect(errors.Is(e, protocol.ErrNotFound)).To(BeTrue(), "another database")
		_, e = r.open("alice", "op-1", "x00000000000000000000000000000000")
		Expect(errors.Is(e, protocol.ErrNotFound)).To(BeTrue(), "unknown handle")
		_, e = r.open("alice", "op-1", "../../etc/passwd")
		Expect(errors.Is(e, protocol.ErrNotFound)).To(BeTrue(), "a path is not a handle")
		Expect(r.files()).To(HaveLen(1), "denied opens do not consume the file")

		r.clock = r.clock.Add(time.Hour)
		_, e = r.open("alice", "op-1", h)
		Expect(errors.Is(e, protocol.ErrNotFound)).To(BeTrue(), "expired")
		Expect(r.files()).To(BeEmpty())
	})

	It("keeps a handle for a retry until the stream completed", func() {
		r := newExportRig()
		res, _ := r.export("alice", "op-1", nil)
		h := res.Outputs[0].Handle
		rc, e := r.t.OpenOutput(context.Background(), r.env("alice", "op-1", "output", "read", map[string]any{"tool": "rows-export", "table": "People"}), h)
		Expect(e).NotTo(HaveOccurred())
		buf := make([]byte, 4)
		_, _ = rc.Read(buf)
		Expect(rc.Close()).To(Succeed())
		got, e := r.open("alice", "op-1", h)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(got)).To(HavePrefix("Name,Age"))
	})

	It("reports a result past the limit as data without an output, and leaves no file", func() {
		r := newExportRig()
		res, e := r.export("alice", "op-1", map[string]any{"maxRows": 1})
		Expect(e).NotTo(HaveOccurred())
		Expect(res.Outputs).To(HaveLen(1), "a limit of one row exports one row")
		Expect(res.Outputs[0].Size).To(Equal(int64(len("Name,Age\r\n'=HYPERLINK(1),-3\r\n"))))

		// Too large: a table that passes the byte bound.
		r3 := newExportRig()
		st3, e := store.Open(filepath.Join(GinkgoT().TempDir(), "tables"), store.Options{ExportMaxBytes: 20})
		Expect(e).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = st3.Close() })
		ctx := context.Background()
		info, e := st3.Create(ctx, dbID, "Sales", "op-c")
		Expect(e).NotTo(HaveOccurred())
		rv := info.Revision
		rc, e := st3.ApplySchema(ctx, store.SchemaChange{ResourceID: dbID, Kind: store.KindTableCreate, Table: "People"}, rv, "op-t")
		Expect(e).NotTo(HaveOccurred())
		rc, e = st3.ApplySchema(ctx, store.SchemaChange{ResourceID: dbID, Kind: store.KindColumnAdd, Table: "People", Column: "Name", Type: "text"}, rc.Revision, "op-cl")
		Expect(e).NotTo(HaveOccurred())
		_, e = st3.ApplyRows(ctx, store.RowChange{ResourceID: dbID, Kind: store.KindRowInsert, Table: "People", Rows: []map[string]json.RawMessage{{"Name": json.RawMessage(`"a long enough value to pass twenty bytes"`)}}}, rc.Revision, "op-r")
		Expect(e).NotTo(HaveOccurred())
		r3.t.Store = st3
		res, e = r3.export("alice", "op-big", nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(res.Outputs).To(BeEmpty())
		var p map[string]any
		Expect(json.Unmarshal(res.Payload, &p)).To(Succeed())
		Expect(p["exported"]).To(Equal(false))
		Expect(p["error"]).To(Equal("too-large"))
		Expect(r3.files()).To(BeEmpty())
	})

	It("removes the staged file when the export fails and bounds what stays staged", func() {
		r := newExportRig()
		Expect(os.MkdirAll(r.dir, 0o700)).To(Succeed())
		Expect(os.Chmod(r.dir, 0o500)).To(Succeed())
		DeferCleanup(func() { _ = os.Chmod(r.dir, 0o700) })
		if os.Geteuid() != 0 {
			_, e := r.export("alice", "op-1", nil)
			Expect(e).To(HaveOccurred(), "an unwritable export directory is unavailable, not a success")
			Expect(errors.Is(e, protocol.ErrBadRequest)).To(BeFalse())
		}
		Expect(os.Chmod(r.dir, 0o700)).To(Succeed())

		// One user cannot stage without bound.
		var last error
		for i := 0; i < 20 && last == nil; i++ {
			var res contract.Result
			res, last = r.export("alice", "op-b"+string(rune('a'+i)), nil)
			if last == nil && len(res.Outputs) == 0 {
				break
			}
		}
		Expect(len(r.files())).To(BeNumerically("<=", 16))
		// Other users are not starved by one user's exports.
		res, e := r.export("bob", "op-bob", nil)
		Expect(e).NotTo(HaveOccurred())
		Expect(res.Outputs).To(HaveLen(1))
	})

	It("is a read that needs the read action and refuses unknown fields and mutation arguments", func() {
		r := newExportRig()
		args := map[string]any{"tool": "rows-export", "table": "People"}
		_, e := r.t.Read(context.Background(), r.env("alice", "op-1", "read", "write", args))
		Expect(errors.Is(e, protocol.ErrForbidden)).To(BeTrue())
		for _, extra := range []map[string]any{{"limit": 5}, {"cursor": "x"}, {"rows": []any{}}, {"set": map[string]any{}}, {"ids": []int{1}}, {"bogus": 1}, {"aggregates": []any{}}} {
			_, e = r.export("alice", "op-x", extra)
			Expect(errors.Is(e, protocol.ErrBadRequest)).To(BeTrue(), "%v", extra)
		}
		Expect(r.files()).To(BeEmpty())
	})

	It("serves the bytes on the protocol output route to the matching envelope only", func() {
		r := newExportRig()
		startup := protocol.Startup{
			Registration: protocol.Registration{AddonID: "tables", Generation: 1, Incarnation: "inc"},
			Credential:   "secret", Limits: contract.DefaultLimits(),
		}
		srv := httptest.NewServer(protocol.Handler(startup, r.t))
		DeferCleanup(srv.Close)
		post := func(path string, v any) (*http.Response, []byte) {
			b, _ := json.Marshal(v)
			req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer secret")
			req.Header.Set("X-LocalAI-Incarnation", "inc")
			resp, e := http.DefaultClient.Do(req)
			Expect(e).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			return resp, body
		}
		resp, body := post("/read", r.env("alice", "op-http", "read", "read", map[string]any{"tool": "rows-export", "table": "People"}))
		Expect(resp.StatusCode).To(Equal(200), string(body))
		var res contract.Result
		Expect(json.Unmarshal(body, &res)).To(Succeed())
		h := res.Outputs[0].Handle
		resp, _ = post("/output", protocol.OutputRequest{Envelope: r.env("bob", "op-http", "output", "read", map[string]any{"tool": "rows-export", "table": "People"}), Handle: h})
		Expect(resp.StatusCode).To(Equal(404))
		resp, body = post("/output", protocol.OutputRequest{Envelope: r.env("alice", "op-http", "output", "read", map[string]any{"tool": "rows-export", "table": "People"}), Handle: h})
		Expect(resp.StatusCode).To(Equal(200))
		Expect(int64(len(body))).To(Equal(res.Outputs[0].Size))
	})
})
