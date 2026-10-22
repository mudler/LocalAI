// SPDX-License-Identifier: MIT
package tablesacceptance_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// export asks for the CSV of a table and waits until the owner holds the file.
func (d *deployment) export(node, user, res string) (id string) {
	GinkgoHelper()
	r := d.read(d.c.Token(node, user), res, map[string]any{"tool": "rows-export", "table": "Entries"})
	Expect(r.Status).To(Equal(200), r.String())
	outs, _ := body(r)["outputs"].([]any)
	Expect(outs).To(HaveLen(1), r.String())
	out := outs[0].(map[string]any)
	id, _ = out["handle"].(string)
	Expect(out["name"]).To(Equal("Entries.csv"))
	Eventually(func(g Gomega) string {
		st, err := d.c.Token(cluster.FrontB, user).Do("GET", "/api/addons/artifacts/"+id, nil, nil)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(st.Status).To(Equal(200), st.String())
		s, _ := body(st)["state"].(string)
		return s
	}, 30*time.Second, 100*time.Millisecond).Should(Equal("complete"))
	return id
}

func (d *deployment) download(node, user, id string) *cluster.Response {
	GinkgoHelper()
	r, err := d.c.Token(node, user).Do("GET", "/api/addons/artifacts/"+id+"/download", nil, nil)
	Expect(err).NotTo(HaveOccurred())
	return r
}

var _ = Describe("Tables files across the owner and two frontends", Ordered, func() {
	var (
		d   *deployment
		res string
	)

	BeforeAll(func() {
		d = deploy(start("0.1.0"), "0.1.0")
		res = d.seedLedger(cluster.FrontA, "member", "Files", map[string]any{"Item": "pen", "Qty": 3}, map[string]any{"Item": "=SUM(A1:A9)", "Qty": 1})
		Expect(d.grant(cluster.Owner, "member", res, "viewer", "viewer").Status).To(Equal(200))
		Expect(d.grant(cluster.Owner, "member", res, "editor", "editor").Status).To(Equal(200))
	})

	It("exports a CSV through a frontend and serves the same bytes from every node to its requester only", func() {
		id := d.export(cluster.FrontA, "member", res)
		var first []byte
		for _, n := range nodes {
			r := d.download(n, "member", id)
			Expect(r.Status).To(Equal(200), n+": "+r.String())
			Expect(r.Header.Get("Content-Disposition")).To(ContainSubstring("attachment"))
			Expect(r.Header.Get("Content-Type")).To(Equal("text/csv"))
			Expect(r.Header.Get("X-Content-Type-Options")).To(Equal("nosniff"))
			Expect(r.String()).To(ContainSubstring("pen"))
			// A formula is neutralised by the add-on, not left to the sheet.
			Expect(r.String()).To(ContainSubstring("'=SUM(A1:A9)"))
			if first == nil {
				first = r.Body
			}
			Expect(r.Body).To(Equal(first), n)
			// The session of the same person works too.
			s, err := d.c.Session(n, "member").Do("GET", "/api/addons/artifacts/"+id+"/download", nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(s.Status).To(Equal(200), n)
		}
		// A person with a grant on the database does not get the file of
		// another person, and an unknown ID looks the same.
		unknown := d.download(cluster.FrontA, "editor", "a0123456789abcdef0123456789abcde")
		for _, n := range nodes {
			for _, who := range []string{"editor", "viewer", "friend", "admin"} {
				r := d.download(n, who, id)
				Expect(r.Status).To(Equal(403), who+" on "+n)
				Expect(r.String()).To(Equal(unknown.String()), who+" on "+n)
			}
		}
	})

	It("stops the downloads of a person whose access to the database was revoked, on every node, and keeps the owner's", func() {
		id := d.export(cluster.FrontB, "viewer", res)
		for _, n := range nodes {
			Expect(d.download(n, "viewer", id).Status).To(Equal(200), n)
		}
		mine := d.export(cluster.Owner, "member", res)
		Expect(d.revoke(cluster.FrontA, "member", res, "viewer").Status).To(Equal(200))
		for _, n := range nodes {
			Expect(d.download(n, "viewer", id).Status).To(Equal(403), n)
			Expect(d.download(n, "member", mine).Status).To(Equal(200), n)
		}
		// A viewer who may not read the database cannot start an export.
		r := d.read(d.c.Token(cluster.FrontB, "viewer"), res, map[string]any{"tool": "rows-export", "table": "Entries"})
		Expect(r.Status).To(Equal(403), r.String())
		Expect(d.grant(cluster.Owner, "member", res, "viewer", "viewer").Status).To(Equal(200))
	})

	It("denies downloads while the add-on is disabled and serves a complete file again when it is enabled", func() {
		id := d.export(cluster.FrontA, "member", res)
		gen := d.gen
		Expect(d.lifecycle(cluster.FrontB, "disable", gen, "").Status).To(Equal(200))
		for _, n := range nodes {
			r := d.download(n, "member", id)
			Expect(r.Status).To(BeElementOf(403, 503), n+": "+r.String())
			Expect(r.String()).NotTo(ContainSubstring("pen"))
		}
		Expect(d.lifecycle(cluster.FrontA, "enable", gen, "").Status).To(Equal(200))
		d.waitState("running")
		for _, n := range nodes {
			Expect(d.download(n, "member", id).Status).To(Equal(200), n)
		}
	})
})

var _ = Describe("Tables when the owner is down", Ordered, func() {
	It("fails every response type closed with the same typed answer from both frontends, runs nothing there and serves everything again after the restart", func() {
		d := deploy(start("0.1.0"), "0.1.0")
		res := d.seedLedger(cluster.FrontA, "member", "Outage", map[string]any{"Item": "pen", "Qty": 3})
		Expect(d.grant(cluster.Owner, "member", res, "editor", "editor").Status).To(Equal(200))
		file := d.export(cluster.FrontB, "member", res)
		app := d.app(d.c.Session(cluster.FrontA, "member"))
		entry := app["ui"].(map[string]any)["entry"].(string)
		held := d.propose(cluster.FrontA, "editor", res, "write", opID("held"), map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{map[string]any{"Item": "held"}}})
		Expect(held.Status).To(Equal(200), held.String())
		heldID, _ := body(held)["id"].(string)
		heldDigest, _ := body(held)["digest"].(string)
		statusOp := opID("stat")
		Expect(d.dispatch(d.c.Token(cluster.FrontA, "admin"), map[string]any{"operationId": statusOp, "action": "enable", "generation": d.gen}).Status).To(BeNumerically("<", 500))
		rows := d.rowCount(cluster.Owner, "member", res)
		receipts := len(d.receipts(res))
		file2 := d.databaseFile(res)
		before, err := os.Stat(file2)
		Expect(err).NotTo(HaveOccurred())

		owner := d.c.Nodes[cluster.Owner]
		ownerPID := owner.PID()
		owner.Kill()
		Eventually(func() int { return len(cluster.ChildrenOf(ownerPID)) }, 20*time.Second, 100*time.Millisecond).Should(BeZero())

		type call struct {
			name string
			do   func(n string) *cluster.Response
		}
		relay := func(kind string) func(n string) *cluster.Response {
			return func(n string) *cluster.Response {
				r, err := d.c.Token(n, "admin").Do("POST", "/api/addons/relay", map[string]any{"kind": kind, "request": map[string]any{
					"addonId": "tables", "generation": d.gen, "operationId": statusOp, "action": "operation-status", "arguments": map[string]any{}}}, nil)
				Expect(err).NotTo(HaveOccurred())
				return r
			}
		}
		proposalOp, applyOp := opID("down-p"), opID("down-a")
		calls := []call{
			{"json read", func(n string) *cluster.Response {
				return d.read(d.c.Token(n, "member"), res, map[string]any{"tool": "rows-query", "table": "Entries"})
			}},
			{"json relay", func(n string) *cluster.Response {
				r, err := d.c.Token(n, "member").Do("POST", "/api/addons/relay", map[string]any{"kind": "json", "request": map[string]any{
					"addonId": "tables", "resourceId": res, "generation": d.gen, "operationId": opID("rel"), "action": "read", "arguments": map[string]any{"tool": "rows-query", "table": "Entries"}}}, nil)
				Expect(err).NotTo(HaveOccurred())
				return r
			}},
			{"progress", relay("progress")},
			{"status", relay("status")},
			{"file status", func(n string) *cluster.Response {
				r, err := d.c.Token(n, "member").Do("GET", "/api/addons/artifacts/"+file, nil, nil)
				Expect(err).NotTo(HaveOccurred())
				return r
			}},
			{"file download", func(n string) *cluster.Response { return d.download(n, "member", file) }},
			{"asset", func(n string) *cluster.Response {
				r, err := d.c.Session(n, "member").Do("GET", entry, nil, nil)
				Expect(err).NotTo(HaveOccurred())
				return r
			}},
			{"apps", func(n string) *cluster.Response {
				r, err := d.c.Session(n, "member").Do("GET", "/api/addons/apps", nil, nil)
				Expect(err).NotTo(HaveOccurred())
				return r
			}},
			{"proposal", func(n string) *cluster.Response {
				return d.propose(n, "editor", res, "write", proposalOp, map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{map[string]any{"Item": "x"}}})
			}},
			{"proposal list", func(n string) *cluster.Response {
				r, err := d.c.Token(n, "editor").Do("GET", "/api/addons/proposals?state=pending", nil, nil)
				Expect(err).NotTo(HaveOccurred())
				return r
			}},
			{"confirmation", func(n string) *cluster.Response { return d.confirm(n, "editor", heldID, heldDigest) }},
			{"apply", func(n string) *cluster.Response {
				return d.apply(n, "member", res, applyOp, "rows-insert", map[string]any{"schema": 1, "data": 0}, map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "x"}}})
			}},
			{"chat tool", func(n string) *cluster.Response {
				return d.chatTool(n, "member", "rows-insert", map[string]any{"resourceId": res, "table": "Entries", "rows": []any{map[string]any{"Item": "x"}}})
			}},
		}
		for _, c := range calls {
			var bodies []string
			for _, n := range []string{cluster.FrontA, cluster.FrontB} {
				r := c.do(n)
				// The chat route reports the add-on tools as unavailable with
				// its own envelope, the same from both frontends: it never
				// serves a model turn that could invent an answer.
				Expect(r.Status).To(Equal(503), c.name+" on "+n+": "+r.String())
				Expect(r.String()).NotTo(ContainSubstring("pen"), c.name)
				bodies = append(bodies, r.String())
			}
			if len(bodies) == 2 {
				Expect(bodies[1]).To(Equal(bodies[0]), c.name+": the same typed answer from every frontend")
			}
		}
		for _, n := range []string{cluster.FrontA, cluster.FrontB} {
			Expect(cluster.ChildrenOf(d.c.Nodes[n].PID())).To(BeEmpty(), n+" runs nothing for the add-on")
			Expect(d.c.Config.Nodes[n].Root).NotTo(BeADirectory(), n+" keeps no add-on storage")
		}
		after, err := os.Stat(file2)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.ModTime()).To(Equal(before.ModTime()), "no database was touched while the owner was down")

		Expect(owner.Start()).To(Succeed())
		Expect(owner.Ready(120 * time.Second)).To(Succeed())
		d.waitState("running")
		for _, n := range nodes {
			Expect(d.rowCount(n, "member", res)).To(Equal(rows), n)
			Expect(d.download(n, "member", file).Status).To(Equal(200), n)
			asset, err := d.c.Session(n, "member").Do("GET", entry, nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(asset.Status).To(Equal(200), n)
		}
		Expect(len(d.receipts(res))).To(Equal(receipts), "nothing was applied by a failed call")
		// No call made while the owner was down stored a proposal: the editor
		// holds exactly the one it held before, whatever route the call took.
		pending := d.pending(cluster.Owner, "editor")
		Expect(pending).To(HaveLen(1))
		Expect(pending[0]["id"]).To(Equal(heldID))
		Expect(d.pending(cluster.Owner, "member")).To(BeEmpty())
		// The held proposal is still the editor's, and applies once.
		ok := d.confirm(cluster.FrontB, "editor", heldID, heldDigest)
		Expect(ok.Status).To(Equal(200), ok.String())
		Expect(body(ok)["state"]).To(Equal("applied"))
		Expect(d.rowCount(cluster.FrontA, "member", res)).To(Equal(rows + 1))
	})
})

var _ = Describe("Tables backup and restore through the administrator's tab", Ordered, func() {
	var (
		d   *deployment
		res string
	)

	BeforeAll(func() {
		d = deploy(start("0.1.0"), "0.1.0")
		res = d.seedLedger(cluster.FrontA, "member", "Restore", map[string]any{"Item": "one", "Qty": 1})
		Expect(d.grant(cluster.Owner, "member", res, "editor", "editor").Status).To(Equal(200))
	})

	recovery := func(node, user, verb string, payload map[string]any) *cluster.Response {
		GinkgoHelper()
		op := opID(verb)
		payload["operationId"] = op
		return d.tab(node, user, "POST", "/api/addons/tables/recovery/"+verb, payload, op)
	}
	backupID := func(r *cluster.Response) string {
		GinkgoHelper()
		Expect(r.Status).To(Equal(200), r.String())
		id, _ := body(r)["id"].(string)
		Expect(id).NotTo(BeEmpty())
		return id
	}
	stop := func(node string) {
		GinkgoHelper()
		op := opID("stop")
		r := d.tab(node, "admin", "POST", "/api/addons/apps/tables/stop", map[string]any{"operationId": op, "generation": d.gen}, op)
		Expect(r.Status).To(Equal(200), r.String())
		d.waitState("stopped")
	}
	startApp := func(node string) {
		GinkgoHelper()
		op := opID("start")
		r := d.tab(node, "admin", "POST", "/api/addons/apps/tables/start", map[string]any{"operationId": op, "generation": d.gen}, op)
		Expect(r.Status).To(Equal(200), r.String())
		d.waitState("running")
	}

	It("is the administrator's own action on every node and never a key's, a member's or a tool's", func() {
		for _, n := range nodes {
			for _, user := range []string{"member", "editor"} {
				r := recovery(n, user, "backup", map[string]any{})
				Expect(r.Status).To(Equal(403), user+" on "+n+": "+r.String())
			}
			keyed, err := d.c.Token(n, "admin").Do("POST", "/api/addons/tables/recovery/backup", map[string]any{"operationId": opID("key")}, map[string]string{})
			Expect(err).NotTo(HaveOccurred())
			Expect(keyed.Status).To(Equal(403), n+": "+keyed.String())
		}
	})

	It("refuses a corrupt backup and a restore that is not allowed, and leaves the data as it was", func() {
		good := backupID(recovery(cluster.FrontB, "admin", "backup", map[string]any{}))
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(1))
		// Data changes after the backup.
		d.applyOK(cluster.FrontA, "member", res, "rows-insert", map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "two", "Qty": 2}}})
		bad := backupID(recovery(cluster.FrontA, "admin", "backup", map[string]any{}))

		// One byte of one file of the second backup is changed.
		dir := filepath.Join(d.c.Config.Nodes[cluster.Owner].Root, "backups", "tables", bad)
		var target string
		Expect(filepath.Walk(dir, func(p string, info os.FileInfo, e error) error {
			if e == nil && !info.IsDir() && filepath.Ext(p) == ".db" && target == "" {
				target = p
			}
			return e
		})).To(Succeed())
		Expect(target).NotTo(BeEmpty(), "the backup holds a database file")
		raw, err := os.ReadFile(target)
		Expect(err).NotTo(HaveOccurred())
		raw[len(raw)/2] ^= 0xff
		Expect(os.Chmod(target, 0o600)).To(Succeed())
		Expect(os.WriteFile(target, raw, 0o600)).To(Succeed())

		v := recovery(cluster.FrontB, "admin", "verify", map[string]any{"backupId": bad})
		Expect(errType(v)).To(Equal("backup_corrupt"), v.String())
		Expect(v.Status).NotTo(Equal(200))
		Expect(recovery(cluster.Owner, "admin", "verify", map[string]any{"backupId": good}).Status).To(Equal(200))

		// Refusals before anything is touched.
		running := recovery(cluster.FrontA, "admin", "restore", map[string]any{"backupId": good, "confirm": "tables"})
		Expect(errType(running)).To(Equal("addon_running"), running.String())
		stop(cluster.FrontB)
		wrongConfirm := recovery(cluster.FrontB, "admin", "restore", map[string]any{"backupId": good, "confirm": "other"})
		Expect(errType(wrongConfirm)).To(Equal("confirmation_mismatch"), wrongConfirm.String())
		missing := recovery(cluster.FrontB, "admin", "restore", map[string]any{"backupId": "b20000101t000000z-00000000", "confirm": "tables"})
		Expect(errType(missing)).To(Equal("backup_not_found"), missing.String())
		corrupt := recovery(cluster.FrontA, "admin", "restore", map[string]any{"backupId": bad, "confirm": "tables"})
		Expect(errType(corrupt)).To(Equal("backup_corrupt"), corrupt.String())
		startApp(cluster.FrontA)
		Expect(d.rowCount(cluster.FrontB, "member", res)).To(Equal(2), "a refused restore changed nothing")
	})

	It("restores a verified backup: the data goes back, the add-on starts again and access follows the current accounts", func() {
		good := ""
		list := d.tab(cluster.FrontA, "admin", "GET", "/api/addons/tables/recovery", nil, "")
		Expect(list.Status).To(Equal(200), list.String())
		for _, b := range body(list)["backups"].([]any) {
			m := b.(map[string]any)
			if m["verification"] == "verified" && m["verified"] == true {
				good = m["id"].(string)
				break
			}
		}
		Expect(good).NotTo(BeEmpty(), list.String())
		stop(cluster.Owner)
		r := recovery(cluster.FrontB, "admin", "restore", map[string]any{"backupId": good, "confirm": "tables"})
		Expect(r.Status).To(Equal(200), r.String())
		startApp(cluster.FrontA)
		for _, n := range nodes {
			Expect(d.rowCount(n, "member", res)).To(BeNumerically(">=", 1), n)
		}
		// The restored data is the data of the backup: the second row was
		// made after it and is gone, with the previous copy kept aside.
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(1))
		for _, n := range nodes {
			Expect(d.read(d.c.Token(n, "friend"), res, map[string]any{"tool": "rows-query", "table": "Entries"}).Status).To(Equal(403), n)
		}
		Expect(d.apply(cluster.FrontA, "member", res, opID("post-restore"), "rows-insert", d.revisionOf(res), map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "three"}}}).Status).To(Equal(200))
		_ = fmt.Sprint
		_ = http.StatusOK
	})
})

var _ = Describe("Tables upgrade with an explicit check between two packaged versions", Ordered, func() {
	var (
		d   *deployment
		res string
	)

	BeforeAll(func() {
		d = deploy(start("0.1.0", "0.2.0"), "0.1.0")
		res = d.seedLedger(cluster.FrontA, "member", "Upgrade", map[string]any{"Item": "kept", "Qty": 1})
		Expect(d.grant(cluster.Owner, "member", res, "editor", "editor").Status).To(Equal(200))
	})

	It("reports an update only on a check, shows a member no digest, refuses a key and applies one pinned candidate with the data and grants kept", func() {
		check := func(node, user string) *cluster.Response {
			op := opID("chk")
			return d.tab(node, user, "POST", "/api/addons/tables/update/check", map[string]any{"operationId": op}, op)
		}
		r := check(cluster.FrontB, "admin")
		Expect(r.Status).To(Equal(200), r.String())
		st := body(r)
		Expect(st["state"]).To(Equal("update-available"))
		Expect(st["installedVersion"]).To(Equal("0.1.0"))
		Expect(st["candidateVersion"]).To(Equal("0.2.0"))
		installed, _ := st["installedDigest"].(string)
		candidate, _ := st["candidateDigest"].(string)
		Expect(installed).To(Equal(packages["0.1.0"].ArchiveDigest))
		Expect(candidate).To(Equal(packages["0.2.0"].ArchiveDigest))
		// The same answer from every node, and nothing about the library or
		// the digests for a member.
		for _, n := range nodes {
			m := d.tab(n, "member", "GET", "/api/addons/tables/update", nil, "")
			Expect(m.Status).To(Equal(200), n)
			Expect(body(m)["state"]).To(Equal("update-available"))
			Expect(m.String()).NotTo(ContainSubstring(candidate))
			Expect(m.String()).NotTo(ContainSubstring("sourceId"))
			keyed, err := d.c.Token(n, "admin").Do("POST", "/api/addons/tables/update/check", map[string]any{"operationId": opID("k")}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(keyed.Status).To(Equal(403), n)
			asMember := check(n, "member")
			Expect(asMember.Status).To(Equal(403), n)
		}

		genBefore := d.gen
		upgrade := func(node, op, expected, cand string) *cluster.Response {
			return d.tab(node, "admin", "POST", "/api/addons/tables/upgrade", map[string]any{
				"operationId": op, "expectedDigest": expected, "candidateDigest": cand, "generation": genBefore}, op)
		}
		stale := upgrade(cluster.FrontA, opID("up"), "sha256:"+fmt.Sprintf("%064d", 0), candidate)
		Expect(stale.Status).To(Equal(409), stale.String())
		Expect(errType(stale)).To(Equal("stale_installed"))
		changed := upgrade(cluster.FrontA, opID("up"), installed, "sha256:"+fmt.Sprintf("%064d", 1))
		Expect(changed.Status).To(Equal(409), changed.String())
		Expect(errType(changed)).To(Equal("candidate_changed"))
		Expect(d.app(d.c.Token(cluster.Owner, "admin"))["version"]).To(Equal("0.1.0"), "a refused pin changed nothing")

		before := d.rowCount(cluster.Owner, "member", res)
		op := opID("up")
		done := upgrade(cluster.FrontB, op, installed, candidate)
		Expect(done.Status).To(Equal(200), done.String())
		d.waitState("running")
		for _, n := range nodes {
			a := d.app(d.c.Token(n, "admin"))
			Expect(a["version"]).To(Equal("0.2.0"), n)
		}
		// The same operation from another node is the recorded result.
		again := upgrade(cluster.FrontA, op, installed, candidate)
		Expect(again.Status).To(Equal(200), again.String())
		// Data, grants and the receipts survived, and the new generation works.
		for _, n := range nodes {
			Expect(d.rowCount(n, "member", res)).To(Equal(before), n)
			Expect(d.rowCount(n, "editor", res)).To(Equal(before), n)
			Expect(d.read(d.c.Token(n, "friend"), res, map[string]any{"tool": "rows-query", "table": "Entries"}).Status).To(Equal(403), n)
		}
		Expect(d.apply(cluster.FrontB, "editor", res, opID("post-up"), "rows-insert", d.revisionOf(res), map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "new"}}}).Status).To(Equal(200))
		// A backup was made before the data was touched.
		rec := d.tab(cluster.FrontA, "admin", "GET", "/api/addons/tables/recovery", nil, "")
		Expect(rec.Status).To(Equal(200), rec.String())
		reasons := ""
		for _, b := range body(rec)["backups"].([]any) {
			reasons += b.(map[string]any)["reason"].(string) + " "
		}
		Expect(reasons).To(ContainSubstring("upgrade"))
		// And the check now says there is nothing newer.
		final := check(cluster.FrontA, "admin")
		Expect(body(final)["state"]).To(Equal("up-to-date"), final.String())
	})
})
