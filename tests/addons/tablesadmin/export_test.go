// SPDX-License-Identifier: MIT
package tablesadmin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/services/addons/artifacts"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ordersCSV is the exact file the Orders table of ordersTable exports.
const ordersCSV = "Item,Note,Qty,Price,Paid\r\n" +
	"'=SUM(A1:A9),\"line one\nline two\",-4,19.99,true\r\n" +
	"\"Café, \"\"deluxe\"\"\",日本語,12,,\r\n" +
	"plain,,,,\r\n"

// ordersTable creates a database for the user with the Orders table: hostile
// text, newlines, Unicode, a negative number and NULLs.
func (h *host) ordersTable(user, prefix string) string {
	GinkgoHelper()
	res := h.database(user, prefix+"-db", "Orders data")
	rev := func() map[string]any {
		v, e := h.read(user, res, `{"tool":"databases-inspect"}`)
		Expect(e).NotTo(HaveOccurred())
		return revisionOf(v)
	}
	step := func(n int, tool, args string) {
		GinkgoHelper()
		got := h.apply(user, res, fmt.Sprintf("%s-%d", prefix, n), tool, rev(), args)
		Expect(got.code).To(Equal(200), got.raw)
	}
	step(1, "tables-create", `{"table":"Orders"}`)
	for i, c := range [][2]string{{"Item", "text"}, {"Note", "text"}, {"Qty", "integer"}, {"Price", "real"}, {"Paid", "boolean"}} {
		step(2+i, "columns-add", fmt.Sprintf(`{"table":"Orders","column":%q,"type":%q}`, c[0], c[1]))
	}
	step(10, "rows-insert", `{"table":"Orders","rows":[
		{"Item":"=SUM(A1:A9)","Note":"line one\nline two","Qty":-4,"Price":19.99,"Paid":true},
		{"Item":"Café, \"deluxe\"","Note":"日本語","Qty":12},
		{"Item":"plain"}]}`)
	return res
}

// exported runs the export as the person through the owner's gateway and
// returns the host artifact it was turned into.
func (h *host) exported(user, res, args string) (contract.OutputDescription, error) {
	GinkgoHelper()
	out, e := h.app.AddonGateway().Dispatch(context.Background(), h.actor(user), contract.Request{
		AddonID: "tables", ResourceID: res, OperationID: "exp-" + uuid.NewString()[:12], Generation: 1, Action: "read",
		Arguments: json.RawMessage(args),
	})
	if e != nil {
		return contract.OutputDescription{}, e
	}
	Expect(out.Outputs).To(HaveLen(1))
	return out.Outputs[0], nil
}

// http is a real network client of the host's HTTP application.
type httpHost struct{ srv *httptest.Server }

func (h *host) listen() httpHost {
	s := httptest.NewServer(h.web)
	DeferCleanup(s.Close)
	return httpHost{s}
}

func (n httpHost) get(path string, header func(*http.Request)) (*http.Response, []byte) {
	GinkgoHelper()
	r, e := http.NewRequest("GET", n.srv.URL+path, nil)
	Expect(e).NotTo(HaveOccurred())
	if header != nil {
		header(r)
	}
	resp, e := http.DefaultClient.Do(r)
	Expect(e).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func (h *host) complete(user, id string) map[string]any {
	GinkgoHelper()
	var got reply
	Eventually(func() string {
		got = h.send("GET", "/api/addons/artifacts/"+id, "", h.bearer(user))
		s, _ := got.body["state"].(string)
		return s
	}, 20*time.Second, 50*time.Millisecond).Should(Equal("complete"), got.raw)
	return got.body
}

var _ = Describe("CSV exports are private artifacts", func() {
	It("exports a table with exact bytes and attachment headers over real HTTP and shows no one else the file", func() {
		h := ready(false)
		res := h.ordersTable("bob", "ex1")
		art, e := h.exported("bob", res, `{"tool":"rows-export","table":"Orders"}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(art.Name).To(Equal("Orders.csv"))
		Expect(art.MediaType).To(Equal("text/csv"))
		Expect(art.Size).To(Equal(int64(len(ordersCSV))))
		// What the caller holds is a host identifier, never the add-on's handle.
		Expect(art.Handle).NotTo(HavePrefix("x0"))
		meta := h.complete("bob", art.Handle)
		Expect(meta["name"]).To(Equal("Orders.csv"))
		Expect(meta["size"]).To(BeEquivalentTo(len(ordersCSV)))
		Expect(meta["expiresAt"]).NotTo(BeEmpty())
		Expect(meta).NotTo(HaveKey("path"))
		Expect(meta).NotTo(HaveKey("handle"))

		net := h.listen()
		resp, body := net.get("/api/addons/artifacts/"+art.Handle+"/download", func(r *http.Request) { h.bearer("bob")(r) })
		Expect(resp.StatusCode).To(Equal(200), string(body))
		Expect(string(body)).To(Equal(ordersCSV))
		Expect(resp.Header.Get("Content-Type")).To(Equal("text/csv"))
		Expect(resp.Header.Get("Content-Disposition")).To(HavePrefix("attachment;"))
		Expect(resp.Header.Get("Content-Disposition")).To(ContainSubstring("Orders.csv"))
		Expect(resp.Header.Get("X-Content-Type-Options")).To(Equal("nosniff"))
		Expect(resp.Header.Get("Cache-Control")).To(ContainSubstring("no-store"))
		Expect(resp.ContentLength).To(Equal(int64(len(ordersCSV))))
		Expect(strings.HasPrefix(string(body), "\xef\xbb\xbf")).To(BeFalse())

		// A person with the feature and no access, and an unknown identifier,
		// get the same denial.
		_, unknown := net.get("/api/addons/artifacts/a0123456789abcdef0123456789abcde/download", h.bearer("carol"))
		resp, stranger := net.get("/api/addons/artifacts/"+art.Handle+"/download", h.bearer("carol"))
		Expect(resp.StatusCode).To(Equal(403))
		Expect(string(stranger)).To(Equal(string(unknown)))
		resp, _ = net.get("/api/addons/artifacts/"+art.Handle, h.bearer("carol"))
		Expect(resp.StatusCode).To(Equal(403))
		// Without a credential there is nothing.
		resp, _ = net.get("/api/addons/artifacts/"+art.Handle+"/download", nil)
		Expect(resp.StatusCode).To(BeElementOf(401, 403))
		// A person who cannot read the database cannot start the export.
		_, e = h.exported("carol", res, `{"tool":"rows-export","table":"Orders"}`)
		Expect(e).To(HaveOccurred())
		Expect(gateway.StatusFor(e)).To(Equal(403))
	})

	It("keeps a shared database's file private, applies the person's own filter and sort, and denies after the grant is withdrawn", func() {
		h := ready(false)
		res := h.ordersTable("bob", "ex2")
		Expect(h.grant("bob", res, "carol", "viewer").code).To(Equal(200))
		mine, e := h.exported("bob", res, `{"tool":"rows-export","table":"Orders"}`)
		Expect(e).NotTo(HaveOccurred())
		h.complete("bob", mine.Handle)
		net := h.listen()
		// Sharing the source does not share the file.
		resp, _ := net.get("/api/addons/artifacts/"+mine.Handle+"/download", h.bearer("carol"))
		Expect(resp.StatusCode).To(Equal(403))

		// A viewer exports what they can read, narrowed by filter, sort and columns.
		theirs, e := h.exported("carol", res, `{"tool":"rows-export","table":"Orders","columns":["_id","Item","Qty"],"filter":{"column":"Qty","op":"is-not-null"},"sort":[{"column":"Qty"}]}`)
		Expect(e).NotTo(HaveOccurred())
		h.complete("carol", theirs.Handle)
		resp, body := net.get("/api/addons/artifacts/"+theirs.Handle+"/download", h.bearer("carol"))
		Expect(resp.StatusCode).To(Equal(200))
		Expect(string(body)).To(MatchRegexp(`^_id,Item,Qty\r\n1,'=SUM\(A1:A9\),-4\r\n2,"Café, ""deluxe""",12\r\n$`))
		resp, _ = net.get("/api/addons/artifacts/"+theirs.Handle+"/download", h.bearer("bob"))
		Expect(resp.StatusCode).To(Equal(403))

		// Withdrawing the grant ends the download of what the viewer exported.
		Expect(h.revoke("bob", res, "carol").code).To(Equal(200))
		resp, _ = net.get("/api/addons/artifacts/"+theirs.Handle+"/download", h.bearer("carol"))
		Expect(resp.StatusCode).To(Equal(403))
		resp, _ = net.get("/api/addons/artifacts/"+theirs.Handle, h.bearer("carol"))
		Expect(resp.StatusCode).To(Equal(200), "the transfer record stays readable to its principal")
		// The owner still downloads their own.
		resp, body = net.get("/api/addons/artifacts/"+mine.Handle+"/download", h.bearer("bob"))
		Expect(resp.StatusCode).To(Equal(200))
		Expect(string(body)).To(Equal(ordersCSV))
	})

	It("serves the same bytes and headers through a forwarding frontend, and fails the same way for a stranger", func() {
		h := ready(false)
		res := h.ordersTable("bob", "ex3")
		art, e := h.exported("bob", res, `{"tool":"rows-export","table":"Orders"}`)
		Expect(e).NotTo(HaveOccurred())
		h.complete("bob", art.Handle)
		front := h.frontend()
		path := "/api/addons/artifacts/" + art.Handle + "/download"
		get := func(web http.Handler, user string) *httptest.ResponseRecorder {
			r := httptest.NewRequest("GET", path, nil)
			h.bearer(user)(r)
			w := httptest.NewRecorder()
			web.ServeHTTP(w, r)
			return w
		}
		own, fwd := get(h.web, "bob"), get(front, "bob")
		Expect(own.Code).To(Equal(200))
		Expect(fwd.Code).To(Equal(200))
		Expect(fwd.Body.String()).To(Equal(own.Body.String()))
		Expect(fwd.Body.String()).To(Equal(ordersCSV))
		for _, k := range []string{"Content-Type", "Content-Disposition", "X-Content-Type-Options", "Cache-Control", "Content-Length"} {
			Expect(fwd.Header().Get(k)).To(Equal(own.Header().Get(k)), k)
		}
		ownDenied, fwdDenied := get(h.web, "carol"), get(front, "carol")
		Expect(ownDenied.Code).To(Equal(403))
		Expect(fwdDenied.Code).To(Equal(403))
		Expect(fwdDenied.Body.String()).To(Equal(ownDenied.Body.String()))
		// The export itself is requested through the frontend, too.
		body := fmt.Sprintf(`{"addonId":"tables","resourceId":%q,"operationId":"via-front-1","generation":1,"action":"read","arguments":{"tool":"rows-export","table":"Orders"}}`, res)
		got := serve(front, "POST", "/api/addons/dispatch", body, h.bearer("bob"))
		Expect(got.code).To(Equal(200), got.raw)
		outs, _ := got.body["outputs"].([]any)
		Expect(outs).To(HaveLen(1))
		id := outs[0].(map[string]any)["handle"].(string)
		h.complete("bob", id)
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/addons/artifacts/"+id+"/download", nil)
		h.bearer("bob")(r)
		front.ServeHTTP(w, r)
		Expect(w.Code).To(Equal(200))
		Expect(w.Body.String()).To(Equal(ordersCSV))
	})

	It("answers a result past the export limit as data with no file", func() {
		h := ready(false)
		res := h.ordersTable("bob", "ex4")
		out, e := h.app.AddonGateway().Dispatch(context.Background(), h.actor("bob"), contract.Request{
			AddonID: "tables", ResourceID: res, OperationID: "exp-big", Generation: 1, Action: "read",
			Arguments: json.RawMessage(`{"tool":"rows-export","table":"Orders","maxRows":5000000}`),
		})
		Expect(e).NotTo(HaveOccurred())
		Expect(out.Outputs).To(BeEmpty())
		var p map[string]any
		Expect(json.Unmarshal(out.Payload, &p)).To(Succeed())
		Expect(p["exported"]).To(Equal(false))
		Expect(p["error"]).To(Equal("too-large"))
		// An unknown table and a mutation argument are plain refusals.
		for _, a := range []string{`{"tool":"rows-export","table":"Nope"}`, `{"tool":"rows-export","table":"Orders","rows":[{}]}`, `{"tool":"rows-export","table":"Orders","bogus":1}`} {
			_, e = h.read("bob", res, a)
			Expect(e).To(HaveOccurred(), a)
		}
	})
})

// store opens an artifact store over the owner's own policy and runtime, with
// a clock and limits the spec controls.
func (h *host) store(clock *time.Time, limits artifacts.Limits) (*artifacts.Artifacts, string) {
	GinkgoHelper()
	dir := filepath.Join(h.dir, "artifacts-"+uuid.NewString()[:8])
	a, e := artifacts.Open(artifacts.Options{
		Dir: dir, Policy: h.app.AddonPolicy(), Runtime: h.app.AddonManager(),
		Addon: func(context.Context, string) (artifacts.AddonState, error) {
			return artifacts.AddonState{Installed: true, Enabled: true, Generation: 1}, nil
		},
		Limits: limits, Now: func() time.Time { return *clock },
	})
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = a.Close() })
	return a, dir
}

// stage asks the real Tables executable for an export and returns the output
// description and the request that binds its handle.
func (h *host) stage(user, res, op, args string) (contract.OutputDescription, contract.Request) {
	GinkgoHelper()
	inst, e := h.app.AddonManager().Runtime(context.Background(), "tables")
	Expect(e).NotTo(HaveOccurred())
	req := contract.Request{AddonID: "tables", ResourceID: res, OperationID: op, Generation: inst.Generation, Action: "read", Arguments: json.RawMessage(args)}
	out, e := inst.Client.Read(context.Background(), contract.Envelope{Request: req, Authority: contract.Authority{Actor: h.actor(user), Scope: "read", ExpiresAt: time.Now().Add(time.Minute)}})
	Expect(e).NotTo(HaveOccurred())
	Expect(out.Outputs).To(HaveLen(1))
	return out.Outputs[0], req
}

func (h *host) staged() []string {
	entries, _ := os.ReadDir(filepath.Join(h.owner.Root, "data", "tables", "exports"))
	var out []string
	for _, e := range entries {
		out = append(out, filepath.Join(h.owner.Root, "data", "tables", "exports", e.Name()))
	}
	return out
}

var _ = Describe("CSV export failures never leave a downloadable file", func() {
	It("binds an output handle to its operation, person and database", func() {
		h := ready(false)
		res := h.ordersTable("bob", "ox1")
		other := h.ordersTable("carol", "ox1c")
		out, req := h.stage("bob", res, "ox-op-1", `{"tool":"rows-export","table":"Orders"}`)
		inst, e := h.app.AddonManager().Runtime(context.Background(), "tables")
		Expect(e).NotTo(HaveOccurred())
		open := func(user string, r contract.Request) error {
			rc, e := inst.Client.OpenOutput(context.Background(), contract.Envelope{Request: r, Authority: contract.Authority{Actor: h.actor(user), Scope: "output", ExpiresAt: time.Now().Add(time.Minute)}}, out.Handle)
			if e == nil {
				_ = rc.Close()
			}
			return e
		}
		Expect(open("carol", req)).To(HaveOccurred(), "another person")
		wrong := req
		wrong.OperationID = "ox-op-2"
		Expect(open("bob", wrong)).To(HaveOccurred(), "another operation")
		wrong = req
		wrong.ResourceID = other
		Expect(open("bob", wrong)).To(HaveOccurred(), "another database")
		Expect(h.staged()).To(HaveLen(1), "denied opens leave the file for its owner")
		rc, e := inst.Client.OpenOutput(context.Background(), contract.Envelope{Request: req, Authority: contract.Authority{Actor: h.actor("bob"), Scope: "output", ExpiresAt: time.Now().Add(time.Minute)}}, out.Handle)
		Expect(e).NotTo(HaveOccurred())
		b, e := io.ReadAll(rc)
		Expect(e).NotTo(HaveOccurred())
		Expect(rc.Close()).To(Succeed())
		Expect(string(b)).To(Equal(ordersCSV))
		Expect(h.staged()).To(BeEmpty(), "a served export leaves nothing staged")
	})

	It("fails a truncated, grown or vanished stream and never serves it", func() {
		h := ready(false)
		res := h.ordersTable("bob", "ox2")
		clock := time.Now()
		files, _ := h.store(&clock, artifacts.DefaultLimits())
		bob := h.actor("bob")
		for name, damage := range map[string]func(path string){
			"truncated": func(p string) { Expect(os.Truncate(p, 10)).To(Succeed()) },
			"grown": func(p string) {
				f, e := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
				Expect(e).NotTo(HaveOccurred())
				_, _ = f.WriteString("extra,row\r\n")
				_ = f.Close()
			},
			"vanished": func(p string) { Expect(os.Remove(p)).To(Succeed()) },
		} {
			out, req := h.stage("bob", res, "ox-"+name, `{"tool":"rows-export","table":"Orders"}`)
			staged := h.staged()
			Expect(staged).To(HaveLen(1), name)
			damage(staged[0])
			art, e := files.Ingest(context.Background(), bob, req, out)
			Expect(e).To(HaveOccurred(), name)
			Expect(art.ID).To(BeEmpty(), name)
			// Nothing downloadable exists for this principal under any name.
			var sink bytes.Buffer
			_, e = files.Download(context.Background(), bob, "a"+strings.Repeat("0", 32), &sink)
			Expect(e).To(HaveOccurred())
			Expect(sink.Len()).To(BeZero())
			for _, p := range h.staged() {
				_ = os.Remove(p)
			}
		}
		// A healthy export through the same store still works afterwards.
		out, req := h.stage("bob", res, "ox-good", `{"tool":"rows-export","table":"Orders"}`)
		art, e := files.Ingest(context.Background(), bob, req, out)
		Expect(e).NotTo(HaveOccurred())
		var sink bytes.Buffer
		_, e = files.Download(context.Background(), bob, art.ID, &sink)
		Expect(e).NotTo(HaveOccurred())
		Expect(sink.String()).To(Equal(ordersCSV))
	})

	It("fails the transfer when the store cannot write, and when the source is revoked before completion", func() {
		h := ready(false)
		res := h.ordersTable("bob", "ox3")
		Expect(h.grant("bob", res, "carol", "viewer").code).To(Equal(200))
		clock := time.Now()
		files, root := h.store(&clock, artifacts.DefaultLimits())
		// A full or read-only disk: every directory of the store refuses a write.
		var dirs []string
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
			if e == nil && d.IsDir() {
				dirs = append(dirs, p)
			}
			return nil
		})
		for _, d := range dirs {
			Expect(os.Chmod(d, 0o500)).To(Succeed())
		}
		DeferCleanup(func() {
			for _, d := range dirs {
				_ = os.Chmod(d, 0o700)
			}
		})
		if os.Geteuid() != 0 {
			out, req := h.stage("bob", res, "ox3-disk", `{"tool":"rows-export","table":"Orders"}`)
			_, e := files.Ingest(context.Background(), h.actor("bob"), req, out)
			Expect(e).To(HaveOccurred())
		}
		for _, d := range dirs {
			Expect(os.Chmod(d, 0o700)).To(Succeed())
		}
		for _, p := range h.staged() {
			_ = os.Remove(p)
		}

		// Revoked before the transfer starts: nothing is staged for the person.
		out, req := h.stage("carol", res, "ox3-revoked", `{"tool":"rows-export","table":"Orders"}`)
		Expect(h.revoke("bob", res, "carol").code).To(Equal(200))
		_, e := files.Ingest(context.Background(), h.actor("carol"), req, out)
		Expect(e).To(HaveOccurred())
		Expect(gateway.StatusFor(e)).To(Equal(403))
	})

	It("expires the file only, enforces the quota per person and leaves the database as it was", func() {
		h := ready(false)
		res := h.ordersTable("bob", "ox4")
		clock := time.Now()
		limits := artifacts.DefaultLimits()
		limits.UserCount, limits.TotalCount = 2, 4
		files, _ := h.store(&clock, limits)
		bob := h.actor("bob")
		ingest := func(op string) (artifacts.Artifact, error) {
			out, req := h.stage("bob", res, op, `{"tool":"rows-export","table":"Orders"}`)
			return files.Ingest(context.Background(), bob, req, out)
		}
		first, e := ingest("ox4-1")
		Expect(e).NotTo(HaveOccurred())
		_, e = ingest("ox4-2")
		Expect(e).NotTo(HaveOccurred())
		_, e = ingest("ox4-3")
		Expect(e).To(HaveOccurred())
		Expect(gateway.StatusFor(e)).To(Equal(403))
		for _, p := range h.staged() {
			_ = os.Remove(p)
		}

		before, e := h.read("bob", res, `{"tool":"rows-query","table":"Orders"}`)
		Expect(e).NotTo(HaveOccurred())
		clock = clock.Add(limits.Expiry + time.Minute)
		var sink bytes.Buffer
		_, e = files.Download(context.Background(), bob, first.ID, &sink)
		Expect(e).To(HaveOccurred())
		Expect(gateway.StatusFor(e)).To(Equal(403))
		Expect(sink.Len()).To(BeZero())
		_, e = files.Sweep(context.Background())
		Expect(e).NotTo(HaveOccurred())
		_, e = files.Status(context.Background(), bob, first.ID)
		Expect(e).To(HaveOccurred())

		// The database is untouched and exports again; the quota is free.
		after, e := h.read("bob", res, `{"tool":"rows-query","table":"Orders"}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(after["rows"]).To(Equal(before["rows"]))
		Expect(after["revision"]).To(Equal(before["revision"]))
		again, e := ingest("ox4-4")
		Expect(e).NotTo(HaveOccurred())
		sink.Reset()
		_, e = files.Download(context.Background(), bob, again.ID, &sink)
		Expect(e).NotTo(HaveOccurred())
		Expect(sink.String()).To(Equal(ordersCSV))
	})
})
