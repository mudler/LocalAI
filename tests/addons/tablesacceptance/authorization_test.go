// SPDX-License-Identifier: MIT
package tablesacceptance_test

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Role separation and credential changes, decided by the real servers.
var _ = Describe("Tables authorization across the owner and two frontends", Ordered, func() {
	var (
		d   *deployment
		res string
	)

	BeforeAll(func() {
		c := start("0.1.0")
		d = deploy(c, "0.1.0")
		res = d.seedLedger(cluster.FrontA, "member", "Ledger", map[string]any{"Item": "pen", "Qty": 3})
		Expect(d.grant(cluster.FrontB, "member", res, "editor", "editor").Status).To(Equal(200))
		Expect(d.grant(cluster.Owner, "member", res, "viewer", "viewer").Status).To(Equal(200))
	})

	It("separates owner, editor, viewer, administrator and stranger across the browser routes, the REST routes and the chat tool consumer", func() {
		row := map[string]any{"Item": "ink", "Qty": 1}
		expect := map[string]struct {
			read, rows, schema, share int
			proposals                 int
		}{
			"member":   {200, 200, 200, 200, 1},
			"editor":   {200, 200, 403, 403, 1},
			"viewer":   {200, 403, 403, 403, 0},
			"friend":   {403, 403, 403, 403, 0},
			"admin":    {403, 403, 403, 403, 0},
			"outsider": {403, 403, 403, 403, 0},
		}
		order := []string{"member", "editor", "viewer", "friend", "admin", "outsider"}
		// The one denial every refusal must equal: a database that does not
		// exist, read by an account that has the feature.
		probe := d.read(d.c.Token(cluster.FrontB, "member"), "r-not-a-database", map[string]any{"tool": "rows-query", "table": "Entries"})
		Expect(probe.Status).To(Equal(403), probe.String())
		denial := probe.String()
		for i, user := range order {
			want := expect[user]
			// Every role goes through a different node in turn: the answer
			// never depends on which process the person reached.
			node := nodes[i%len(nodes)]
			other := nodes[(i+1)%len(nodes)]

			// REST read with the API key, on the node and on another one.
			for _, n := range []string{node, other} {
				r := d.read(d.c.Token(n, user), res, map[string]any{"tool": "rows-query", "table": "Entries"})
				Expect(r.Status).To(Equal(want.read), fmt.Sprintf("%s reads on %s: %s", user, n, r.String()))
				if want.read == 403 && user != "outsider" {
					// A stranger and an administrator read the same denial, on
					// every node; it does not tell a missing database from a
					// private one.
					Expect(r.String()).To(Equal(denial), user+" on "+n)
				}
			}
			missing := d.read(d.c.Token(node, user), "r-not-a-database", map[string]any{"tool": "rows-query", "table": "Entries"})
			Expect(missing.Status).To(Equal(403), user)
			if user != "outsider" {
				Expect(missing.String()).To(Equal(denial), "a missing database looks like a private one: "+user)
			}

			// The person's own tab: a row change and a schema change.
			rows := d.apply(node, user, res, opID("acc-r"), "rows-insert", d.revisionOf(res), map[string]any{"table": "Entries", "rows": []any{row}})
			Expect(rows.Status).To(Equal(want.rows), fmt.Sprintf("%s inserts on %s: %s", user, node, rows.String()))
			column := "Col" + strings.ToUpper(user[:1]) + user[1:]
			schema := d.apply(other, user, res, opID("acc-s"), "columns-add", d.revisionOf(res), map[string]any{"table": "Entries", "column": column, "type": "text"})
			Expect(schema.Status).To(Equal(want.schema), fmt.Sprintf("%s changes the schema on %s: %s", user, other, schema.String()))
			share := d.grant(node, user, res, "friend", "viewer")
			Expect(share.Status).To(Equal(want.share), fmt.Sprintf("%s shares on %s: %s", user, node, share.String()))
			if want.share == 200 {
				Expect(d.revoke(other, user, res, "friend").Status).To(Equal(200))
			}

			// The typed proposal route: a write is a proposal for the owner
			// role and the editor, and never for a viewer or a stranger.
			prop := d.propose(other, user, res, "write", opID("acc-p"), map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{row}})
			if want.rows == 200 {
				Expect(prop.Status).To(Equal(200), user+": "+prop.String())
			} else {
				Expect(prop.Status).To(Equal(403), user+": "+prop.String())
			}

			// The chat consumer: the scripted model calls the tool as this key.
			before := 0
			if want.read == 200 || want.rows == 200 {
				before = len(d.pending(node, user))
			}
			d.chatTool(other, user, "rows-insert", map[string]any{"resourceId": res, "table": "Entries", "rows": []any{row}})
			if user != "outsider" {
				after := len(d.pending(node, user))
				Expect(after-before).To(Equal(want.proposals), fmt.Sprintf("%s chat proposals (%d -> %d)", user, before, after))
			}
		}

		// The same data through each node tells the same story: the owner role
		// and the editor wrote, the others did not.
		for _, n := range nodes {
			count := d.rowCount(n, "viewer", res)
			Expect(count).To(Equal(1 + 2))
		}
	})

	It("serves the browser module to every account with the feature and to nobody else, from the owner and each frontend", func() {
		for _, user := range []string{"member", "editor", "viewer", "friend", "admin"} {
			for _, n := range nodes {
				a := d.app(d.c.Session(n, user))
				Expect(a).NotTo(BeNil(), user+" on "+n)
				ui, _ := a["ui"].(map[string]any)
				Expect(ui).NotTo(BeNil())
				entry, _ := ui["entry"].(string)
				Expect(entry).To(MatchRegexp(`^/api/addons/tables/assets/[0-9a-f]{64}/`))
				asset, err := d.c.Session(n, user).Do("GET", entry, nil, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(asset.Status).To(Equal(200), user+" "+n)
				Expect(asset.Body).NotTo(BeEmpty())
				// The page of the SPA is the same shell for everyone; what the
				// account may do is the server's answer above.
				page, err := d.c.Session(n, user).Do("GET", "/app/addons/tables/databases", nil, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(page.Status).To(Equal(200), user+" "+n)
			}
		}
		for _, n := range nodes {
			r, err := d.c.Session(n, "outsider").Do("GET", "/api/addons/apps", nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.Status).To(Equal(403), n)
			anon, err := cluster.Client{Base: d.c.Nodes[n].URL()}.Do("GET", "/api/addons/apps", nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(anon.Status).To(Equal(401), n)
		}
	})

	It("fails every blocked consumer explicitly and never serves add-on tools to it", func() {
		member := func(n string) cluster.Client { return d.c.Token(n, "member") }
		for _, n := range nodes {
			for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/mcp/chat/completions"} {
				r, err := member(n).Do("POST", path, map[string]any{
					"model": "mock-model", "metadata": map[string]string{"addon_tools": "true"},
					"max_tokens": 5, "input": "hi", "messages": []map[string]any{{"role": "user", "content": "hi"}},
				}, nil)
				Expect(err).NotTo(HaveOccurred())
				Expect(r.Status).To(Equal(http.StatusBadRequest), n+" "+path+": "+r.String())
				Expect(errType(r)).To(Equal("addon_tools_unsupported"), n+" "+path)
			}
			// The chat route itself refuses an account without the feature
			// and an anonymous caller, with typed denials.
			out := d.chatTool(n, "outsider", "rows-query", map[string]any{"resourceId": res, "table": "Entries"})
			Expect(out.Status).To(Equal(403), n+": "+out.String())
			anon, err := cluster.Client{Base: d.c.Nodes[n].URL()}.Do("POST", "/v1/chat/completions", map[string]any{
				"model": "mock-model", "metadata": map[string]string{"addon_tools": "true"},
				"messages": []map[string]any{{"role": "user", "content": "hi"}},
			}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(anon.Status).To(Equal(401), n)
		}
	})
})

// revisionOf reads the revision as the owner of a database. It is used where
// the caller is whoever may read; the revision is the same for everyone.
func (d *deployment) revisionOf(res string) map[string]any {
	GinkgoHelper()
	return d.revision(cluster.Owner, "member", res)
}

// A person's rights change while the person's tabs and agents are working.
var _ = Describe("Tables authorization when rights change in the middle of a session", Ordered, func() {
	var (
		d   *deployment
		res string
		row = map[string]any{"Item": "x", "Qty": 1}
	)

	BeforeAll(func() {
		d = deploy(start("0.1.0"), "0.1.0")
		res = d.seedLedger(cluster.Owner, "member", "Shared", map[string]any{"Item": "pen", "Qty": 3})
		Expect(d.grant(cluster.FrontA, "member", res, "editor", "editor").Status).To(Equal(200))
	})

	// everywhere runs one check on the owner and on both frontends.
	everywhere := func(check func(node string)) {
		for _, n := range nodes {
			check(n)
		}
	}
	editorCanWrite := func(node string) int {
		return d.apply(node, "editor", res, opID("mid"), "rows-insert", d.revisionOf(res), map[string]any{"table": "Entries", "rows": []any{row}}).Status
	}

	It("denies the next call of a paused key on every node and accepts it again when resumed", func() {
		everywhere(func(n string) {
			Expect(d.read(d.c.Token(n, "editor"), res, map[string]any{"tool": "rows-query", "table": "Entries"}).Status).To(Equal(200), n)
		})
		key := d.c.Creds.Users["editor"].KeyID
		r := d.tab(cluster.FrontB, "editor", "PATCH", "/api/auth/api-keys/"+key, map[string]any{"disabled": true}, "")
		Expect(r.Status).To(Equal(200), r.String())
		everywhere(func(n string) {
			got := d.read(d.c.Token(n, "editor"), res, map[string]any{"tool": "rows-query", "table": "Entries"})
			Expect(got.Status).To(BeElementOf(401, 403), n+": "+got.String())
			chat := d.chatTool(n, "editor", "rows-insert", map[string]any{"resourceId": res, "table": "Entries", "rows": []any{row}})
			Expect(chat.Status).To(BeElementOf(401, 403), n)
		})
		// The person's own session is a different credential and still works.
		Expect(editorCanWrite(cluster.FrontA)).To(Equal(200))
		r = d.tab(cluster.FrontA, "editor", "PATCH", "/api/auth/api-keys/"+key, map[string]any{"disabled": false}, "")
		Expect(r.Status).To(Equal(200), r.String())
		everywhere(func(n string) {
			Expect(d.read(d.c.Token(n, "editor"), res, map[string]any{"tool": "rows-query", "table": "Entries"}).Status).To(Equal(200), n)
		})
	})

	It("denies the browser, the key and the chat everywhere when the add-ons feature is withdrawn", func() {
		admin := d.c.Token(cluster.FrontA, "admin")
		id := d.c.Creds.Users["editor"].ID
		r, err := admin.Do("PUT", "/api/auth/admin/users/"+id+"/permissions", map[string]bool{"addons": false}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(200), r.String())
		everywhere(func(n string) {
			Expect(editorCanWrite(n)).To(Equal(403), n)
			Expect(d.read(d.c.Token(n, "editor"), res, map[string]any{"tool": "rows-query", "table": "Entries"}).Status).To(Equal(403), n)
			Expect(d.chatTool(n, "editor", "rows-insert", map[string]any{"resourceId": res, "table": "Entries", "rows": []any{row}}).Status).To(Equal(403), n)
			apps, err := d.c.Session(n, "editor").Do("GET", "/api/addons/apps", nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(apps.Status).To(Equal(403), n)
		})
		r, err = admin.Do("PUT", "/api/auth/admin/users/"+id+"/permissions", map[string]bool{"addons": true}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(200), r.String())
		Expect(editorCanWrite(cluster.FrontB)).To(Equal(200))
	})

	It("lowers an editor to a viewer for the next call everywhere and drops the proposals the editor still held", func() {
		held := d.propose(cluster.FrontB, "editor", res, "write", opID("held"), map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{row}})
		Expect(held.Status).To(Equal(200), held.String())
		heldID, _ := body(held)["id"].(string)
		heldDigest, _ := body(held)["digest"].(string)
		Expect(heldID).NotTo(BeEmpty(), held.String())
		Expect(len(d.pending(cluster.Owner, "editor"))).To(BeNumerically(">=", 1))

		Expect(d.grant(cluster.FrontB, "member", res, "editor", "viewer").Status).To(Equal(200))
		everywhere(func(n string) {
			Expect(editorCanWrite(n)).To(Equal(403), n)
			Expect(d.read(d.c.Token(n, "editor"), res, map[string]any{"tool": "rows-query", "table": "Entries"}).Status).To(Equal(200), n)
		})
		// The proposal the editor held does not apply any more.
		Expect(d.confirm(cluster.FrontA, "editor", heldID, heldDigest).Status).NotTo(Equal(200))
		everywhere(func(n string) {
			for _, p := range d.pending(n, "editor") {
				Expect(p["id"]).NotTo(Equal(heldID), n)
			}
		})
		Expect(d.revoke(cluster.Owner, "member", res, "editor").Status).To(Equal(200))
		everywhere(func(n string) {
			Expect(d.read(d.c.Token(n, "editor"), res, map[string]any{"tool": "rows-query", "table": "Entries"}).Status).To(Equal(403), n)
		})
		Expect(d.grant(cluster.FrontA, "member", res, "editor", "editor").Status).To(Equal(200))
	})

	It("refuses the session and the key of a disabled account on every node", func() {
		admin := d.c.Token(cluster.FrontB, "admin")
		id := d.c.Creds.Users["editor"].ID
		r, err := admin.Do("PUT", "/api/auth/admin/users/"+id+"/status", map[string]string{"status": "disabled"}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(200), r.String())
		everywhere(func(n string) {
			Expect(editorCanWrite(n)).To(BeElementOf(401, 403), n)
			got := d.read(d.c.Token(n, "editor"), res, map[string]any{"tool": "rows-query", "table": "Entries"})
			Expect(got.Status).To(BeElementOf(401, 403), n)
		})
		r, err = admin.Do("PUT", "/api/auth/admin/users/"+id+"/status", map[string]string{"status": "active"}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Status).To(Equal(200), r.String())
		Expect(editorCanWrite(cluster.FrontA)).To(Equal(200))
	})
})

// Authority is the host's. Whatever a browser, a script or a model puts in a
// request about who it is or whether it was approved is only data.
var _ = Describe("Tables authority and approval cannot be forged", Ordered, func() {
	var (
		d   *deployment
		res string
		row = map[string]any{"Item": "x", "Qty": 1}
	)

	BeforeAll(func() {
		d = deploy(start("0.1.0"), "0.1.0")
		res = d.seedLedger(cluster.FrontA, "member", "Private", map[string]any{"Item": "pen", "Qty": 3})
		Expect(d.grant(cluster.Owner, "member", res, "editor", "editor").Status).To(Equal(200))
		Expect(d.grant(cluster.Owner, "member", res, "viewer", "viewer").Status).To(Equal(200))
	})

	It("ignores actor, forwarding and authority headers and fields on every node", func() {
		forged := map[string]string{
			"X-Forwarded-User": d.c.Creds.Users["member"].ID, "X-Forwarded-Actor": d.c.Creds.Users["member"].ID,
			"X-Addon-Actor": d.c.Creds.Users["member"].ID, "X-LocalAI-Actor": d.c.Creds.Users["member"].ID,
			"X-Remote-User": d.c.Creds.Users["member"].ID, "X-User-Id": d.c.Creds.Users["member"].ID,
			"X-Addon-Peer": "owner",
		}
		for _, n := range nodes {
			// A stranger with forged identity headers still reads nothing.
			stranger := d.c.Token(n, "friend")
			stranger.Headers = forged
			r := d.read(stranger, res, map[string]any{"tool": "rows-query", "table": "Entries"})
			Expect(r.Status).To(Equal(403), n+": "+r.String())
			Expect(r.String()).NotTo(ContainSubstring("pen"))
			// A viewer with them still cannot write.
			viewer := d.c.Session(n, "viewer")
			viewer.Headers = forged
			w, err := viewer.Do("POST", "/api/addons/resources/"+res+"/apply", map[string]any{
				"operationId": opID("forge"), "generation": d.gen, "revision": d.revisionOf(res), "tool": "rows-insert",
				"arguments": map[string]any{"table": "Entries", "rows": []any{row}},
			}, map[string]string{"X-LocalAI-Confirm": res})
			Expect(err).NotTo(HaveOccurred())
			Expect(w.Status).To(Equal(403), n+": "+w.String())
		}
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(1))
	})

	It("derives the action of a proposal from the tool kind, so an editor cannot confirm a schema change as a write", func() {
		rename := map[string]any{"tool": "databases-rename", "name": "Stolen"}
		for _, n := range nodes {
			// Model rules R4 and S4: the editor claims the data action for the
			// rename tool, and the schema action; both are refused.
			for _, action := range []string{"write", "schema"} {
				r := d.propose(n, "editor", res, action, opID("kind-e"), rename)
				Expect(r.Status).To(Equal(403), n+" editor "+action+": "+r.String())
			}
			// The owner role may not claim the data action for it either.
			r := d.propose(n, "member", res, "write", opID("kind-m"), rename)
			Expect(r.Status).To(Equal(403), n+" owner write: "+r.String())
		}
		Expect(d.pending(cluster.Owner, "editor")).To(BeEmpty())
		// The owner role proposes the rename under the schema action.
		r := d.propose(cluster.FrontA, "member", res, "schema", opID("kind-ok"), rename)
		Expect(r.Status).To(Equal(200), r.String())
	})

	It("denies authority, role and approval fields in the arguments of every consumer and creates nothing", func() {
		before := len(d.pending(cluster.Owner, "editor"))
		for _, field := range []map[string]any{
			{"actor": d.c.Creds.Users["member"].ID}, {"userId": d.c.Creds.Users["member"].ID}, {"role": "owner"},
			{"approved": true}, {"confirmed": true}, {"proposalId": "x"}, {"authority": map[string]any{"scope": "apply"}},
		} {
			args := map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{row}}
			for k, v := range field {
				args[k] = v
			}
			for _, n := range nodes {
				r := d.propose(n, "editor", res, "write", opID("fa"), args)
				// A field the tool does not declare is refused as invalid, with
				// the same typed 400 from every node, and nothing is stored.
				Expect(r.Status).To(Equal(400), fmt.Sprintf("%s %v: %s", n, field, r.String()))
				Expect(errType(r)).To(Equal("invalid_request"), fmt.Sprintf("%s %v: %s", n, field, r.String()))
				chatArgs := map[string]any{"resourceId": res, "table": "Entries", "rows": []any{row}}
				for k, v := range field {
					chatArgs[k] = v
				}
				// The chat consumer's own argument binding rejects the field
				// before the add-on is asked: the turn is served, nothing ran
				// (the pending list below proves it).
				chat := d.chatTool(n, "editor", "rows-insert", chatArgs)
				Expect(chat.Status).To(Equal(200), chat.String())
				Expect(chat.String()).NotTo(ContainSubstring("pen"), fmt.Sprintf("%s %v", n, field))
			}
		}
		Expect(len(d.pending(cluster.FrontB, "editor"))).To(Equal(before))
		// The tool of the arguments must be the declared one, exactly.
		for _, tool := range []string{"Rows-insert", "rows-insert ", "tables-drop-all", ""} {
			r := d.read(d.c.Token(cluster.FrontA, "editor"), res, map[string]any{"tool": tool, "table": "Entries"})
			Expect(r.Status).To(Equal(403), tool+": "+r.String())
			Expect(r.String()).NotTo(ContainSubstring("pen"))
		}
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(1))
	})

	It("applies a proposal only on the confirmation of the proposer's own tab, with its digest, exactly once", func() {
		op := opID("approve")
		p := d.propose(cluster.FrontA, "editor", res, "write", op, map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{row}})
		Expect(p.Status).To(Equal(200), p.String())
		id, _ := body(p)["id"].(string)
		digest, _ := body(p)["digest"].(string)
		Expect(id).To(Equal(op))
		bad := map[string]*cluster.Response{}
		// A key never approves, whoever it belongs to.
		// The header that names the proposal is sent as well: the key is what
		// fails, not a missing header.
		named := map[string]string{"X-LocalAI-Confirm": id, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"}
		keyed, _ := d.c.Token(cluster.FrontB, "editor").Do("POST", "/api/addons/proposals/"+id+"/confirm", map[string]any{"digest": digest}, named)
		bad["the proposer's key"] = keyed
		keyedOwner, _ := d.c.Token(cluster.Owner, "member").Do("POST", "/api/addons/proposals/"+id+"/confirm", map[string]any{"digest": digest}, named)
		bad["the owner's key"] = keyedOwner
		keyedAdmin, _ := d.c.Token(cluster.FrontA, "admin").Do("POST", "/api/addons/proposals/"+id+"/confirm", map[string]any{"digest": digest}, named)
		bad["an administrator's key"] = keyedAdmin
		// The resource owner's own tab is not the proposer's.
		bad["the resource owner's tab"] = d.confirm(cluster.FrontB, "member", id, digest)
		// A viewer's tab, an administrator's tab.
		bad["a viewer's tab"] = d.confirm(cluster.Owner, "viewer", id, digest)
		bad["an administrator's tab"] = d.confirm(cluster.FrontA, "admin", id, digest)
		// The proposer's tab without the confirmation header, with another ID
		// and with a tool-call marker.
		bad["no confirmation header"] = d.tab(cluster.FrontB, "editor", "POST", "/api/addons/proposals/"+id+"/confirm", map[string]any{"digest": digest}, "")
		bad["another proposal's header"] = d.tab(cluster.FrontB, "editor", "POST", "/api/addons/proposals/"+id+"/confirm", map[string]any{"digest": digest}, "other-proposal")
		marked, _ := d.c.Session(cluster.FrontB, "editor").Do("POST", "/api/addons/proposals/"+id+"/confirm", map[string]any{"digest": digest},
			map[string]string{"X-LocalAI-Confirm": id, "X-LocalAI-Tool-Call": "1"})
		bad["a tool-call marker"] = marked
		for what, r := range bad {
			Expect(r.Status).To(Equal(403), what+": "+r.String())
		}
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(1), "nothing was applied by a forged approval")
		// A wrong digest is not the proposal the person saw.
		wrong := d.confirm(cluster.FrontB, "editor", id, "sha256:"+strings.Repeat("0", 64))
		Expect(wrong.Status).NotTo(Equal(200), wrong.String())
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(1))
		// The proposer's own tab, through another node than the one that
		// stored the proposal, applies it once; repeats are the same receipt.
		ok := d.confirm(cluster.FrontB, "editor", id, digest)
		Expect(ok.Status).To(Equal(200), ok.String())
		Expect(body(ok)["state"]).To(Equal("applied"))
		for _, n := range nodes {
			again := d.confirm(n, "editor", id, digest)
			Expect(again.Status).To(Equal(200), n+": "+again.String())
			Expect(body(again)["state"]).To(Equal("applied"))
		}
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(2))
	})
})
