// SPDX-License-Identifier: MIT
package tablesexport_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const ordersCSV = "Item,Note,Qty\r\n" +
	"'=SUM(A1:A9),\"line one\nline two\",-4\r\n" +
	"\"Café, \"\"deluxe\"\"\",日本語,12\r\n" +
	"plain,,\r\n"

var seq int

func op(prefix string) string {
	seq++
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano()%1_000_000, seq)
}

// get is one real HTTP request; the headers are what the response carried.
func get(c cluster.Client, path string) *cluster.Response {
	GinkgoHelper()
	r, err := c.Do("GET", path, nil, nil)
	Expect(err).NotTo(HaveOccurred())
	return r
}

var _ = Describe("a CSV export downloaded through the owner and both frontends", func() {
	It("serves identical bytes and headers from every replica and denies every other way in", func() {
		c := start()
		raw, err := os.ReadFile("../../../addons/tables/manifest.json")
		Expect(err).NotTo(HaveOccurred())
		var m struct {
			Capabilities map[string]any `json:"capabilities"`
		}
		Expect(json.Unmarshal(raw, &m)).To(Succeed())
		delete(m.Capabilities, "ui")
		Expect(c.Library.Add(cluster.Package{ID: "tables", Version: "0.1.0", Capabilities: m.Capabilities, Executable: "bin/tables", Binary: tables}, c.Effects)).To(Succeed())
		Expect(c.Up()).To(Succeed())
		admin := c.Token(cluster.FrontA, "admin")
		life := func(cl cluster.Client, action string, gen uint64, extra map[string]any) {
			GinkgoHelper()
			body := map[string]any{"operationId": op("life"), "addonId": "tables", "action": action, "generation": gen, "arguments": extra}
			r, err := cl.Do("POST", "/api/addons/dispatch", body, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Status).To(Equal(200), r.String())
		}
		life(admin, "install", 0, map[string]any{"sourceId": "fixture", "version": "0.1.0"})
		life(c.Token(cluster.FrontB, "admin"), "enable", 1, map[string]any{})
		Eventually(func() string {
			r, err := admin.Do("GET", "/api/addons/apps", nil, nil)
			if err != nil || r.Status != 200 {
				return "unavailable"
			}
			var out struct {
				Apps []struct{ ID, State string } `json:"apps"`
			}
			_ = r.JSON(&out)
			for _, a := range out.Apps {
				if a.ID == "tables" {
					return a.State
				}
			}
			return "absent"
		}, 40*time.Second, 200*time.Millisecond).Should(Equal("running"))

		// The member creates the database and its rows through frontend A, by
		// the person's own session, as the page does.
		member := c.Session(cluster.FrontA, "member")
		tab := func(id string) map[string]string { return map[string]string{"X-LocalAI-Confirm": id} }
		created := op("create")
		r, err := member.Do("POST", "/api/addons/resources", map[string]any{"addonId": "tables", "operationId": created, "generation": 1, "arguments": map[string]any{"name": "Orders data"}}, tab(created))
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(200), r.String())
		var db struct {
			ID       string         `json:"id"`
			Revision map[string]any `json:"revision"`
		}
		Expect(r.JSON(&db)).To(Succeed())
		rev := db.Revision
		change := func(tool string, args map[string]any) {
			GinkgoHelper()
			r, err := member.Do("POST", "/api/addons/resources/"+db.ID+"/apply", map[string]any{"operationId": op("apply"), "generation": 1, "revision": rev, "tool": tool, "arguments": args}, tab(db.ID))
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Status).To(Equal(200), r.String())
			var out struct {
				Revision map[string]any `json:"revision"`
			}
			Expect(r.JSON(&out)).To(Succeed())
			rev = out.Revision
		}
		change("tables-create", map[string]any{"table": "Orders"})
		change("columns-add", map[string]any{"table": "Orders", "column": "Item", "type": "text"})
		change("columns-add", map[string]any{"table": "Orders", "column": "Note", "type": "text"})
		change("columns-add", map[string]any{"table": "Orders", "column": "Qty", "type": "integer"})
		change("rows-insert", map[string]any{"table": "Orders", "rows": []map[string]any{
			{"Item": "=SUM(A1:A9)", "Note": "line one\nline two", "Qty": -4},
			{"Item": "Café, \"deluxe\"", "Note": "日本語", "Qty": 12},
			{"Item": "plain"},
		}})
		share, err := member.Do("PUT", "/api/addons/resources/"+db.ID+"/grants/fnd-friend", map[string]any{"role": "viewer"}, tab(db.ID))
		Expect(err).NotTo(HaveOccurred())
		Expect(share.Status).To(Equal(200), share.String())

		// The export is asked for through frontend B and lands at the owner.
		viaB := c.Token(cluster.FrontB, "member")
		exp, err := viaB.Do("POST", "/api/addons/dispatch", map[string]any{
			"operationId": op("export"), "addonId": "tables", "resourceId": db.ID, "generation": 1, "action": "read",
			"arguments": map[string]any{"tool": "rows-export", "table": "Orders"},
		}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(exp.Status).To(Equal(200), exp.String())
		var out struct {
			Outputs []struct {
				Handle    string `json:"handle"`
				Name      string `json:"name"`
				MediaType string `json:"mediaType"`
				Size      int64  `json:"size"`
			} `json:"outputs"`
		}
		Expect(exp.JSON(&out)).To(Succeed())
		Expect(out.Outputs).To(HaveLen(1))
		id := out.Outputs[0].Handle
		Expect(out.Outputs[0].Size).To(Equal(int64(len(ordersCSV))))

		Eventually(func() string {
			s := get(c.Token(cluster.FrontA, "member"), "/api/addons/artifacts/"+id)
			var st struct {
				State string `json:"state"`
			}
			_ = s.JSON(&st)
			return st.State
		}, 20*time.Second, 100*time.Millisecond).Should(Equal("complete"))

		// The same bytes and the same headers from the owner and both frontends.
		var first *cluster.Response
		for _, node := range []string{cluster.Owner, cluster.FrontA, cluster.FrontB} {
			dl := get(c.Token(node, "member"), "/api/addons/artifacts/"+id+"/download")
			Expect(dl.Status).To(Equal(200), node+" "+dl.String())
			Expect(string(dl.Body)).To(Equal(ordersCSV), node)
			Expect(dl.Header.Get("Content-Type")).To(Equal("text/csv"), node)
			Expect(dl.Header.Get("Content-Disposition")).To(HavePrefix("attachment;"), node)
			Expect(dl.Header.Get("Content-Disposition")).To(ContainSubstring("Orders.csv"), node)
			Expect(dl.Header.Get("X-Content-Type-Options")).To(Equal("nosniff"), node)
			Expect(dl.Header.Get("Cache-Control")).To(ContainSubstring("no-store"), node)
			Expect(dl.Header.Get("Content-Length")).To(Equal(fmt.Sprint(len(ordersCSV))), node)
			if first == nil {
				first = dl
				continue
			}
			for _, k := range []string{"Content-Type", "Content-Disposition", "X-Content-Type-Options", "Cache-Control", "Content-Length"} {
				Expect(dl.Header.Get(k)).To(Equal(first.Header.Get(k)), node+" "+k)
			}
		}

		// A person the database is shared with, and one without the feature,
		// are denied the file on every replica exactly like an unknown ID.
		unknown := "a0123456789abcdef0123456789abcde"
		for _, node := range []string{cluster.Owner, cluster.FrontA, cluster.FrontB} {
			base := get(c.Token(node, "friend"), "/api/addons/artifacts/"+unknown+"/download")
			Expect(base.Status).To(Equal(http.StatusForbidden), node)
			for _, who := range []string{"friend", "admin", "outsider"} {
				dl := get(c.Token(node, who), "/api/addons/artifacts/"+id+"/download")
				Expect(dl.Status).To(Equal(http.StatusForbidden), node+" "+who)
				if who != "outsider" {
					Expect(string(dl.Body)).To(Equal(string(base.Body)), node+" "+who)
				}
				st := get(c.Token(node, who), "/api/addons/artifacts/"+id)
				Expect(st.Status).To(Equal(http.StatusForbidden), node+" "+who)
			}
		}

		// The friend exports what they may read; withdrawing the grant ends
		// the download on every replica.
		fr, err := c.Token(cluster.FrontA, "friend").Do("POST", "/api/addons/dispatch", map[string]any{
			"operationId": op("export-friend"), "addonId": "tables", "resourceId": db.ID, "generation": 1, "action": "read",
			"arguments": map[string]any{"tool": "rows-export", "table": "Orders", "columns": []string{"Item"}},
		}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fr.Status).To(Equal(200), fr.String())
		var fo struct {
			Outputs []struct {
				Handle string `json:"handle"`
			} `json:"outputs"`
		}
		Expect(fr.JSON(&fo)).To(Succeed())
		fid := fo.Outputs[0].Handle
		Eventually(func() int {
			return get(c.Token(cluster.FrontB, "friend"), "/api/addons/artifacts/"+fid+"/download").Status
		}, 20*time.Second, 100*time.Millisecond).Should(Equal(200))
		Expect(string(get(c.Token(cluster.FrontB, "friend"), "/api/addons/artifacts/"+fid+"/download").Body)).To(Equal("Item\r\n'=SUM(A1:A9)\r\n\"Café, \"\"deluxe\"\"\"\r\nplain\r\n"))
		gone, err := member.Do("DELETE", "/api/addons/resources/"+db.ID+"/grants/fnd-friend", nil, tab(db.ID))
		Expect(err).NotTo(HaveOccurred())
		Expect(gone.Status).To(Equal(200), gone.String())
		for _, node := range []string{cluster.Owner, cluster.FrontA, cluster.FrontB} {
			Expect(get(c.Token(node, "friend"), "/api/addons/artifacts/"+fid+"/download").Status).To(Equal(http.StatusForbidden), node)
		}
		// The member's own file is unaffected.
		Expect(string(get(c.Token(cluster.FrontB, "member"), "/api/addons/artifacts/"+id+"/download").Body)).To(Equal(ordersCSV))
	})
})
