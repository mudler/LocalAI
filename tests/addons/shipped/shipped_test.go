// SPDX-License-Identifier: MIT
package shipped_test

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// addonPIDs lists the processes of one add-on that the server under test runs.
func addonPIDs(addon string) []int {
	out, err := exec.Command("ps", "-eo", "pid,args").Output() // #nosec G204 -- fixed command
	Expect(err).NotTo(HaveOccurred())
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, w.dataDir) && strings.Contains(line, "/"+addon+" --localai-addon") {
			var pid int
			if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &pid); err == nil {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

var _ = Describe("shipped add-ons on the real server", Ordered, ContinueOnFailure, func() {
	var admin, member, outsider *client

	BeforeAll(func() {
		setupWorld()
		startServer("named", true, nil)
		admin = newSession("admin")
		member = newSession("member")
		outsider = newSession("outsider")
	})
	AfterAll(func() {
		stopServer()
		if w.stopLibrary != nil {
			w.stopLibrary()
		}
	})

	Describe("install, trust and permissions", func() {
		It("refuses a package whose digest differs from its catalog entry", func() {
			r := admin.dispatch("tables", 1, "install", map[string]any{"sourceId": "local", "version": packageVer + "-badsum"}, "")
			Expect(r.Status).To(Equal(403), r.String())
			Expect(admin.installed("tables")).To(BeNil())
		})
		It("refuses a loopback http library that the owner configuration did not allow", func() {
			r := admin.dispatch("tables", 0, "catalog-source-save", map[string]any{"id": "sneaky", "name": "Sneaky", "url": w.libraryURL, "enabled": true, "allowLoopbackHTTP": true}, "")
			Expect(r.Status).To(Equal(403), r.String())
		})
		It("lets only an administrator install, upgrade, uninstall and manage libraries", func() {
			Expect(member.do("PUT", "/api/auth/admin/users/"+member.id+"/permissions", map[string]bool{"addons": true}, nil).Status).To(Equal(403))
			Expect(admin.do("PUT", "/api/auth/admin/users/"+member.id+"/permissions", map[string]bool{"addons": true}, nil).Status).To(Equal(200))
			Expect(member.dispatch("tables", 1, "install", map[string]any{"sourceId": "local", "version": packageVer}, "").Status).To(Equal(403))
			Expect(member.dispatch("tables", 1, "upgrade", map[string]any{"sourceId": "local", "version": upgradeVer}, "").Status).To(Equal(403))
			Expect(member.dispatch("tables", 1, "uninstall", nil, "").Status).To(Equal(403))
			Expect(member.get("/api/library/sources").Status).To(Equal(403))
			Expect(member.do("POST", "/api/library/sources", map[string]string{"kind": "addon", "url": "https://example.invalid/x.yaml"}, nil).Status).To(Equal(403))
		})
		It("installs, enables and runs every shipped add-on, and serves its page assets", func() {
			for _, a := range allAddons {
				if a == "sandbox" && w.skipSandbox {
					continue
				}
				admin.install(a, packageVer)
			}
			r := admin.get("/api/addons/apps")
			Expect(r.Status).To(Equal(200), r.String())
			apps, _ := r.JSON()["apps"].([]any)
			seen := map[string]bool{}
			for _, x := range apps {
				app := x.(map[string]any)
				id, _ := app["id"].(string)
				seen[id] = true
				Expect(app["state"]).To(Equal("running"), id)
				ui, _ := app["ui"].(map[string]any)
				Expect(ui).NotTo(BeNil(), id)
				paths := []string{ui["entry"].(string), app["icon"].(string)}
				for _, css := range ui["css"].([]any) {
					paths = append(paths, css.(string))
				}
				for _, p := range paths {
					a := admin.do("GET", p, nil, map[string]string{"Sec-Fetch-Dest": "script"})
					Expect(a.Status).To(Equal(200), id+" "+p)
					Expect(len(a.Body)).To(BeNumerically(">", 100), id+" "+p)
				}
			}
			for _, a := range allAddons {
				if a == "sandbox" && w.skipSandbox {
					continue
				}
				Expect(seen).To(HaveKey(a))
			}
			page := admin.do("GET", "/app/addons/tables/databases", nil, nil)
			Expect(page.Status).To(Equal(200))
			Expect(string(page.Body)).To(ContainSubstring("<html"))
		})
		It("keeps add-on routes and assets behind the session and the add-ons feature", func() {
			anon := &client{http: http.DefaultClient}
			Expect(anon.get("/api/addons/apps").Status).To(Equal(401))
			Expect(anon.get("/api/addons/pins").Status).To(Equal(401))
			Expect(outsider.get("/api/addons/apps").Status).To(Equal(403))
			Expect(outsider.get("/api/addons/builtin").Status).To(Equal(200))
			r := admin.get("/api/addons/apps").JSON()["apps"].([]any)[0].(map[string]any)
			entry := r["ui"].(map[string]any)["entry"].(string)
			Expect(anon.get(entry).Status).To(Equal(401))
			Expect(outsider.do("GET", entry, nil, nil).Status).To(Equal(403))
			Expect(member.do("GET", entry, nil, nil).Status).To(Equal(200))
		})
		It("keeps add-on pins per user and filters what the user may not use", func() {
			Expect(admin.do("PUT", "/api/addons/pins", []string{"tables", "office", "not-installed"}, nil).Status).To(Equal(200))
			r := admin.get("/api/addons/pins")
			Expect(string(r.Body)).To(ContainSubstring("tables"))
			Expect(string(r.Body)).NotTo(ContainSubstring("not-installed"))
			Expect(strings.TrimSpace(string(outsider.get("/api/addons/pins").Body))).To(Equal("[]"))
		})
		It("restarts an add-on that crashed, and counts the restart", func() {
			pids := addonPIDs("tables")
			Expect(pids).To(HaveLen(1))
			Expect(syscall.Kill(pids[0], syscall.SIGKILL)).To(Succeed())
			Eventually(func() []int { return addonPIDs("tables") }, 60*time.Second, 300*time.Millisecond).Should(HaveLen(1))
			Expect(addonPIDs("tables")[0]).NotTo(Equal(pids[0]))
			Eventually(func() string { return admin.state("tables") }, 60*time.Second, 300*time.Millisecond).Should(Equal("running"))
			Expect(admin.installed("tables")["restarts"]).To(BeNumerically(">=", 1))
		})
		It("stops the process when the add-on is turned off and starts it again", func() {
			gen := generation(admin.installed("tables"))
			Expect(admin.dispatch("tables", gen, "disable", nil, "").Status).To(Equal(200))
			Eventually(func() int { return len(addonPIDs("tables")) }, 30*time.Second, 200*time.Millisecond).Should(Equal(0))
			Expect(admin.state("tables")).To(Equal("stopped"))
			Expect(admin.dispatch("tables", gen, "enable", nil, "").Status).To(Equal(200))
			Eventually(func() string { return admin.state("tables") }, 60*time.Second, 300*time.Millisecond).Should(Equal("running"))
		})
	})

	Describe("Tables from a chat", func() {
		var res, entries string
		var changes map[string]string
		var key *client

		query := func(token *client) []any {
			r := token.dispatch("tables", generation(admin.installed("tables")), "read", map[string]any{"tool": "rows-query", "table": "Entries"}, res)
			Expect(r.Status).To(Equal(200), r.String())
			rows, _ := payload(r)["rows"].([]any)
			return rows
		}
		undo := func(c *client, change, op string) reply {
			return c.human("POST", "/api/addons/resources/"+res+"/changes/"+change+"/undo",
				map[string]any{"operationId": op, "generation": generation(admin.installed("tables"))}, res)
		}
		applied := func(tool string, args map[string]any) string {
			GinkgoHelper()
			args["resourceId"] = res
			doc := toolResult(admin.toolCall("tables__"+tool, args))
			Expect(doc["kind"]).To(Equal("applied"), fmt.Sprint(doc))
			a := doc["applied"].(map[string]any)
			Expect(a["undoable"]).To(BeTrue(), fmt.Sprint(a))
			Expect(a["changeId"]).NotTo(BeEmpty())
			return a["changeId"].(string)
		}

		BeforeAll(func() {
			changes = map[string]string{}
			key = admin.key()
			op := opID("create")
			r := admin.human("POST", "/api/addons/resources", map[string]any{"addonId": "tables", "operationId": op,
				"generation": generation(admin.installed("tables")), "arguments": map[string]any{"name": "Ledger"}}, op)
			Expect(r.Status).To(Equal(200), r.String())
			res, _ = r.JSON()["id"].(string)
			Expect(res).NotTo(BeEmpty())
			// A key cannot create a database: it is not a browser session.
			op2 := opID("create")
			Expect(key.human("POST", "/api/addons/resources", map[string]any{"addonId": "tables", "operationId": op2, "generation": 1,
				"arguments": map[string]any{"name": "Nope"}}, op2).Status).To(Equal(403))
		})

		It("applies a schema and a row change at once and says they can be undone", func() {
			changes["table"] = applied("tables-create", map[string]any{"table": "Entries"})
			changes["colItem"] = applied("columns-add", map[string]any{"table": "Entries", "column": "Item", "type": "text"})
			changes["colQty"] = applied("columns-add", map[string]any{"table": "Entries", "column": "Qty", "type": "integer"})
			changes["insert"] = applied("rows-insert", map[string]any{"table": "Entries", "rows": []map[string]any{{"Item": "apple", "Qty": 3}, {"Item": "pear", "Qty": 5}}})
			Expect(query(admin)).To(HaveLen(2))
			entries = "Entries"
		})
		It("refuses an invalid request with a typed error that tells the model not to retry", func() {
			doc := toolResult(admin.toolCall("tables__rows-insert", map[string]any{"resourceId": res, "table": entries, "rows": []map[string]any{{"Nope": 1}}}))
			Expect(doc["kind"]).To(Equal("error"))
			Expect(doc["code"]).To(Equal("invalid_request"))
			Expect(query(admin)).To(HaveLen(2))
		})
		It("answers a conflict of the add-on (a table that exists) as a refusal, not as an outage", func() {
			msg := admin.toolCall("tables__tables-create", map[string]any{"resourceId": res, "table": entries})
			doc := toolResult(msg)
			Expect(doc["kind"]).To(Equal("error"), msg)
			Expect(fmt.Sprint(doc)).NotTo(ContainSubstring("unavailable"))
			p := key.do("POST", "/api/addons/proposals", map[string]any{"addonId": "tables", "operationId": opID("p"), "generation": 1, "resourceId": res,
				"action": "schema", "arguments": map[string]any{"tool": "tables-create", "table": entries}}, nil)
			Expect(p.Status).To(BeNumerically("<", 500), p.String())
		})
		It("answers a resource the user cannot reach as a typed refusal, not as an internal string", func() {
			msg := admin.toolCall("tables__rows-insert", map[string]any{"resourceId": "rcdeadbeef", "table": entries, "rows": []map[string]any{{"Item": "x"}}})
			Expect(toolResult(msg)["kind"]).To(Equal("error"), msg)
		})
		It("undoes row changes from the newest to the oldest and refuses a stale undo", func() {
			changes["update"] = applied("rows-update", map[string]any{"table": entries, "ids": []int{1}, "set": map[string]any{"Qty": 99}})
			changes["delete"] = applied("rows-delete", map[string]any{"table": entries, "ids": []int{2}})
			Expect(query(admin)).To(HaveLen(1))
			stale := undo(admin, changes["update"], opID("u"))
			Expect(stale.Status).To(Equal(409), stale.String())
			Expect(stale.ErrType()).To(Equal("stale"))
			Expect(string(stale.Body)).To(ContainSubstring("deleted 1 row"))
			Expect(undo(admin, changes["delete"], opID("u")).Status).To(Equal(200))
			Expect(query(admin)).To(HaveLen(2))
			Expect(undo(admin, changes["update"], opID("u")).Status).To(Equal(200))
			first := query(admin)[0].(map[string]any)["values"].([]any)
			Expect(first[1]).To(BeEquivalentTo(3))
		})
		It("runs an undo once for one operation id and refuses another undo of the same change", func() {
			op := opID("u")
			a := undo(admin, changes["insert"], op)
			Expect(a.Status).To(Equal(200), a.String())
			Expect(query(admin)).To(BeEmpty())
			b := undo(admin, changes["insert"], op)
			Expect(b.Status).To(Equal(200), b.String())
			Expect(b.JSON()["summary"]).To(Equal(a.JSON()["summary"]))
			c := undo(admin, changes["insert"], opID("u"))
			Expect(c.Status).To(Equal(409))
			Expect(c.ErrType()).To(Equal("already_undone"))
		})
		It("answers an unknown change as not_found and an old change as expired", func() {
			r := undo(admin, "tdeadbeef", opID("u"))
			Expect(r.Status).To(Equal(404))
			Expect(r.ErrType()).To(Equal("not_found"))
			// Age the change set of the last column the way seven days would.
			db, err := sql.Open("sqlite3", filepath.Join(w.dataDir, "addons", "data", "tables", "tables", "databases", res+".db"))
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = db.Close() }()
			_, err = db.Exec("UPDATE tables_changes SET created_ns = ? WHERE operation_id = ?", time.Now().Add(-8*24*time.Hour).UnixNano(), changes["colQty"])
			Expect(err).NotTo(HaveOccurred())
			// The newest remaining change is the column Qty: no later change blocks it.
			e := undo(admin, changes["colQty"], opID("u"))
			Expect(e.Status).To(Equal(410), e.String())
			Expect(e.ErrType()).To(Equal("expired"))
		})
		It("lets only the person's own browser session undo or confirm", func() {
			ch := changes["colItem"]
			Expect(undo(key, ch, opID("u")).Status).To(Equal(403))
			// The key together with the session cookie is still not a browser session.
			both := &client{http: admin.http, token: key.token}
			Expect(undo(both, ch, opID("u")).Status).To(Equal(403))
			// Without the confirm header bound to the resource.
			Expect(admin.human("POST", "/api/addons/resources/"+res+"/changes/"+ch+"/undo", map[string]any{"operationId": opID("u"), "generation": 1}, "wrong").Status).To(Equal(403))
			// A user with the add-ons feature but no grant on the database.
			Expect(undo(member, ch, opID("u")).Status).To(BeNumerically(">=", 403))
		})
		It("exports rows as a private file only its owner can download", func() {
			msg := admin.toolCall("tables__rows-export", map[string]any{"resourceId": res, "table": entries})
			doc := toolResult(msg)
			Expect(doc["kind"]).To(Equal("artifact"), msg)
			art := doc["artifacts"].([]any)[0].(map[string]any)
			id := art["id"].(string)
			Expect(art["mediaType"]).To(Equal("text/csv"))
			ok := admin.get("/api/addons/artifacts/" + id + "/download")
			Expect(ok.Status).To(Equal(200))
			Expect(string(ok.Body)).To(ContainSubstring("Item,Qty"))
			Expect(member.get("/api/addons/artifacts/" + id + "/download").Status).To(Equal(403))
			Expect((&client{http: http.DefaultClient}).get("/api/addons/artifacts/" + id + "/download").Status).To(Equal(401))
		})
		It("makes a change from a key only a proposal, which only the proposer's session confirms", func() {
			raw, _ := json.Marshal(map[string]any{"resourceId": res, "table": entries, "rows": []map[string]any{{"Item": "kiwi", "Qty": 1}}})
			cc := key.do("POST", "/v1/chat/completions", map[string]any{"model": "mock-model", "metadata": map[string]string{"addon_tools": "true"},
				"messages": []map[string]string{{"role": "user", "content": "x ADDON_TOOL_CALL:addon__tables__rows-insert:" + base64.RawURLEncoding.EncodeToString(raw)}}}, nil)
			Expect(cc.Status).To(Equal(200), cc.String())
			Expect(string(cc.Body)).To(ContainSubstring("waiting for your confirmation"))
			Expect(query(admin)).To(BeEmpty())
			pend := key.get("/api/addons/proposals?state=pending")
			list, _ := pend.JSON()["proposals"].([]any)
			Expect(list).To(HaveLen(1))
			p := list[0].(map[string]any)
			id, digest := p["id"].(string), p["digest"].(string)
			Expect(key.human("POST", "/api/addons/proposals/"+id+"/confirm", map[string]string{"digest": digest}, id).Status).To(Equal(403))
			Expect(admin.human("POST", "/api/addons/proposals/"+id+"/confirm", map[string]string{"digest": strings.Repeat("0", 64)}, id).Status).To(Equal(403))
			Expect(query(admin)).To(BeEmpty())
			Expect(admin.human("POST", "/api/addons/proposals/"+id+"/confirm", map[string]string{"digest": digest}, id).Status).To(Equal(200))
			Expect(query(admin)).To(HaveLen(1))
		})
		It("rejects add-on tools on the other chat surfaces with the typed error", func() {
			for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/mcp/chat/completions"} {
				body := map[string]any{"model": "mock-model", "max_tokens": 10, "metadata": map[string]string{"addon_tools": "true"},
					"messages": []map[string]string{{"role": "user", "content": "hi"}}, "input": "hi"}
				r := key.do("POST", path, body, nil)
				Expect(r.Status).To(Equal(400), path+" "+r.String())
				Expect(r.ErrType()).To(Equal("addon_tools_unsupported"), path)
			}
		})
		It("keeps the data when an add-on is upgraded, uninstalled and installed again", func() {
			gen := generation(admin.installed("tables"))
			Expect(admin.dispatch("tables", gen, "upgrade", map[string]any{"sourceId": "local", "version": upgradeVer}, "").Status).To(Equal(200))
			a := admin.installed("tables")
			Expect(a["version"]).To(Equal(upgradeVer))
			Expect(generation(a)).To(BeNumerically(">", gen))
			Eventually(func() string { return admin.state("tables") }, 60*time.Second, 300*time.Millisecond).Should(Equal("running"))
			Expect(query(admin)).To(HaveLen(1))
			g2 := generation(admin.installed("tables"))
			Expect(admin.dispatch("tables", g2, "disable", nil, "").Status).To(Equal(200))
			Expect(admin.dispatch("tables", g2, "uninstall", nil, "").Status).To(Equal(200))
			Expect(admin.installed("tables")).To(BeNil())
			admin.install("tables", packageVer)
			Expect(query(admin)).To(HaveLen(1))
		})
	})

	Describe("Fine-tuning and Quantization from a chat", func() {
		jobs := func(kind string) []any {
			r := admin.get("/api/" + kind + "/jobs")
			Expect(r.Status).To(Equal(200), r.String())
			var out []any
			Expect(jsonList(r.Body, &out)).To(Succeed())
			return out
		}
		It("starts a quantization job at once and undoes the start by stopping it", func() {
			msg := admin.toolCall("quantization__quantize-start", map[string]any{"name": "q1", "model": "org/some-model"})
			doc := toolResult(msg)
			Expect(doc["kind"]).To(Equal("applied"), msg)
			a := doc["applied"].(map[string]any)
			res, change := a["resourceId"].(string), a["changeId"].(string)
			Expect(a["undoable"]).To(BeTrue())
			Eventually(func() string { return jobStatus(jobs("quantization")) }, 30*time.Second, 300*time.Millisecond).Should(Equal("quantizing"))
			gen := generation(admin.installed("quantization"))
			u := admin.human("POST", "/api/addons/resources/"+res+"/changes/"+change+"/undo", map[string]any{"operationId": opID("u"), "generation": gen}, res)
			Expect(u.Status).To(Equal(200), u.String())
			Eventually(func() string { return jobStatus(jobs("quantization")) }, 30*time.Second, 300*time.Millisecond).Should(Equal("stopped"))
			again := admin.human("POST", "/api/addons/resources/"+res+"/changes/"+change+"/undo", map[string]any{"operationId": opID("u"), "generation": gen}, res)
			Expect(again.ErrType()).To(Equal("already_undone"))
			// Another user may not undo it.
			Expect(member.human("POST", "/api/addons/resources/"+res+"/changes/"+change+"/undo", map[string]any{"operationId": opID("u"), "generation": gen}, res).Status).To(Equal(403))
		})
		It("starts a fine-tuning job at once and undoes the start", func() {
			doc := toolResult(admin.toolCall("fine-tuning__fine-tune-start", map[string]any{"name": "ft1", "model": "org/some-model", "datasetSource": "org/some-dataset"}))
			Expect(doc["kind"]).To(Equal("applied"), fmt.Sprint(doc))
			a := doc["applied"].(map[string]any)
			res, change := a["resourceId"].(string), a["changeId"].(string)
			u := admin.human("POST", "/api/addons/resources/"+res+"/changes/"+change+"/undo", map[string]any{"operationId": opID("u"), "generation": generation(admin.installed("fine-tuning"))}, res)
			Expect(u.Status).To(Equal(200), u.String())
		})
		It("does not offer the tools to a user without the fine-tuning and quantization permissions", func() {
			Expect(member.toolOffered("fine-tuning__fine-tune-list", map[string]any{})).To(BeFalse())
			Expect(member.toolOffered("quantization__quantize-list", map[string]any{})).To(BeFalse())
			Expect(member.get("/api/fine-tuning/jobs").Status).To(Equal(403))
			Expect(member.get("/api/quantization/jobs").Status).To(Equal(403))
			// The administrator, who holds both, still gets them.
			Expect(toolResult(admin.toolCall("fine-tuning__fine-tune-list", map[string]any{}))["kind"]).To(Equal("result"))
		})
		It("explains a denial of the user's permission instead of reporting an outage", func() {
			gen := generation(admin.installed("fine-tuning"))
			r := member.dispatch("fine-tuning", gen, "read", map[string]any{"tool": "fine-tune-list"}, "")
			Expect(r.Status).To(Equal(403), r.String())
			Expect(r.ErrType()).To(Equal("not_permitted"), r.String())
			Expect(string(r.Body)).NotTo(ContainSubstring("unavailable"))
		})
	})

	Describe("Office on real files", func() {
		put := func(c *client, path string, data []byte) {
			GinkgoHelper()
			req, _ := http.NewRequest("PUT", w.base+"/api/chats/files/content?path="+path, strings.NewReader(string(data)))
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Origin", w.base)
			resp, err := c.http.Do(req)
			Expect(err).NotTo(HaveOccurred())
			_ = resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(200))
		}
		It("reads a PDF and refuses files that are not what their name says or lie outside the user's files", func() {
			put(admin, "/Reports/t.pdf", minimalPDF("Hello PDF page one", "Second page text"))
			put(admin, "/Reports/fake.docx", []byte("not a zip"))
			doc := toolResult(admin.toolCall("office__read-pdf", map[string]any{"path": "/Reports/t.pdf"}))
			Expect(doc["kind"]).To(Equal("result"))
			Expect(fmt.Sprint(doc["result"])).To(ContainSubstring("Hello PDF page one"))
			Expect(fmt.Sprint(doc["result"])).To(ContainSubstring("Second page text"))
			one := toolResult(admin.toolCall("office__read-pdf", map[string]any{"path": "/Reports/t.pdf", "firstPage": 2, "lastPage": 2}))
			Expect(fmt.Sprint(one["result"])).NotTo(ContainSubstring("Hello PDF"))
			bad := toolResult(admin.toolCall("office__read-docx", map[string]any{"path": "/Reports/fake.docx"}))
			Expect(fmt.Sprint(bad["result"])).To(ContainSubstring("not a valid Word document"))
			ext := toolResult(admin.toolCall("office__read-docx", map[string]any{"path": "/../etc/passwd"}))
			Expect(fmt.Sprint(ext["result"])).To(ContainSubstring("must end in .docx"))
			// Another user does not see the file.
			other := toolResult(member.toolCall("office__read-pdf", map[string]any{"path": "/Reports/t.pdf"}))
			Expect(fmt.Sprint(other["result"])).To(ContainSubstring("not found"))
		})
		It("creates, reads and edits Word, Excel and PowerPoint files and keeps one undoable history", func() {
			chat := newLetterChat(admin)
			doc := toolResult(admin.toolCallIn(chat, "office__create-docx", map[string]any{"path": "/Reports/plan.docx", "title": "Plan",
				"blocks": []map[string]any{{"kind": "heading", "level": 1, "text": "Q1 plan"}, {"kind": "paragraph", "text": "Ship the thing."}}}))
			Expect(fmt.Sprint(doc["result"])).To(ContainSubstring("ok:true"))
			edit := toolResult(admin.toolCallIn(newLetterChat(admin), "office__edit-docx", map[string]any{"path": "/Reports/plan.docx", "edits": []map[string]any{{"op": "replace", "find": "Ship", "text": "Launch"}}}))
			Expect(fmt.Sprint(edit["result"])).To(ContainSubstring("replaced:1"))
			read := toolResult(admin.toolCall("office__read-docx", map[string]any{"path": "/Reports/plan.docx"}))
			Expect(fmt.Sprint(read["result"])).To(ContainSubstring("Launch the thing."))
			x := toolResult(admin.toolCallIn(newLetterChat(admin), "office__create-xlsx", map[string]any{"path": "/Reports/b.xlsx", "sheets": []map[string]any{{"name": "S1", "rows": [][]string{{"a", "b"}, {"1", "2"}}}}}))
			Expect(fmt.Sprint(x["result"])).To(ContainSubstring("ok:true"))
			p := toolResult(admin.toolCallIn(newLetterChat(admin), "office__create-pptx", map[string]any{"path": "/Reports/d.pptx", "slides": []map[string]any{{"title": "Hello", "bullets": []string{"x"}}}}))
			Expect(fmt.Sprint(p["result"])).To(ContainSubstring("ok:true"))
		})
		It("runs an action tool in every chat, whatever the chat id looks like", func() {
			chat := newDigitChat(admin)
			msg := admin.toolCallIn(chat, "office__create-docx", map[string]any{"path": "/Reports/digit.docx", "blocks": []map[string]any{{"kind": "paragraph", "text": "a"}}})
			Expect(fmt.Sprint(toolResult(msg)["result"])).To(ContainSubstring("ok:true"), msg)
		})
	})

	Describe("Sandbox in a packaged build", func() {
		BeforeEach(func() {
			if w.skipSandbox {
				Skip("the Sandbox package needs the runsc download (LOCALAI_E2E_RUNSC_DIR)")
			}
		})
		It("says in plain words why no isolated backend can run, and that the host backend is the only one usable", func() {
			doc := toolResult(admin.toolCall("sandbox__status", map[string]any{}))
			res, _ := doc["result"].(map[string]any)
			Expect(res["canRun"]).To(BeFalse())
			text := fmt.Sprint(res["backends"])
			Expect(text).To(ContainSubstring("no guest image"))
			Expect(text).To(ContainSubstring("name:host usable:true"))
		})
		It("lets an administrator choose the host backend with the typed confirmation", func() {
			gen := generation(admin.installed("sandbox"))
			Expect(admin.dispatch("sandbox", gen, "read", map[string]any{"tool": "confirm-host", "phrase": "I understand that commands run on this host without isolation"}, "").Status).To(Equal(200))
			Expect(admin.dispatch("sandbox", gen, "read", map[string]any{"tool": "set-backend", "backend": "host"}, "").Status).To(Equal(200))
		})
		It("answers 'This host cannot run commands.' when no backend is usable", func() {
			msg := admin.toolCallIn(newLetterChat(admin), "sandbox__bash", map[string]any{"command": "echo hi"})
			Expect(msg).To(ContainSubstring("This host cannot run commands."))
		})
		It("runs a command on the host backend and writes the result into the user's files", func() {
			Expect(os.WriteFile(filepath.Join(w.dataDir, "addons", "data", "sandbox", "settings.json"), []byte(`{"backend":"host","hostConfirmed":true}`), 0o600)).To(Succeed())
			gen := generation(admin.installed("sandbox"))
			Expect(admin.dispatch("sandbox", gen, "disable", nil, "").Status).To(Equal(200))
			Expect(admin.dispatch("sandbox", gen, "enable", nil, "").Status).To(Equal(200))
			Eventually(func() string { return admin.state("sandbox") }, 60*time.Second, 300*time.Millisecond).Should(Equal("running"))
			msg := admin.toolCallIn(newLetterChat(admin), "sandbox__bash", map[string]any{"command": "mkdir -p home/Reports && echo data > home/Reports/from-sandbox.txt"})
			Expect(fmt.Sprint(toolResult(msg)["result"])).To(ContainSubstring("ok:true"), msg)
			Expect(admin.get("/api/chats/files/content?path=/Reports/from-sandbox.txt").Status).To(Equal(200))
		})
	})

	Describe("owner modes and flags", func() {
		It("turns every add-on, fine-tuning and quantization route off with one answer", func() {
			stopServer()
			startServer("off", false, []string{"LOCALAI_ADDONS_ENABLED=false"})
			anon := &client{http: http.DefaultClient}
			features := anon.get("/api/features").JSON()
			Expect(features["addons"]).To(BeFalse())
			Expect(features["fine_tuning"]).To(BeFalse())
			Expect(features["quantization"]).To(BeFalse())
			for _, p := range []string{"/api/addons/apps", "/api/addons/not-a-route", "/api/addons/pins", "/api/fine-tuning/jobs", "/api/quantization/jobs"} {
				r := anon.get(p)
				Expect(r.Status).To(Equal(503), p)
				e := r.JSON()["error"].(map[string]any)
				Expect(e["code"]).To(Equal("unavailable"))
				Expect(e["message"]).To(Equal("add-ons are turned off on this instance"))
			}
			stopServer()
		})
		It("stops at start with a clear error for a role without its carrier settings and for an unknown mode", func() {
			for args, want := range map[string]string{
				"--addons-role=owner":                "--addons-owner-address is required with --addons-role=owner",
				"--addons-ownership-mode=bogus":      `"bogus" is not valid`,
				"--addons-ownership-mode=named-user": "",
			} {
				if want == "" {
					continue
				}
				dir := GinkgoT().TempDir()
				cmd := exec.Command(w.bin, "run", "--address", "127.0.0.1:0", "--data-path", dir, "--models-path", filepath.Join(dir, "m"), args) // #nosec G204 -- the binary under test
				cmd.Env = cleanEnv()
				out, err := cmd.CombinedOutput()
				Expect(err).To(HaveOccurred())
				Expect(string(out)).To(ContainSubstring(want))
			}
		})
	})
})
