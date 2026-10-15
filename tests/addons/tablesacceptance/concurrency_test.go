// SPDX-License-Identifier: MIT
package tablesacceptance_test

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Tables staleness, replay and races across the owner and two frontends", Ordered, func() {
	var (
		d   *deployment
		res string
	)

	BeforeAll(func() {
		d = deploy(start("0.1.0"), "0.1.0")
		res = d.seedLedger(cluster.FrontA, "member", "Shared", map[string]any{"Item": "pen", "Qty": 3})
		Expect(d.grant(cluster.Owner, "member", res, "editor", "editor").Status).To(Equal(200))
	})

	It("refuses a change prepared on an old schema or data revision from two tabs of two people and applies nothing", func() {
		// Both people open the database at the same revision.
		ownerView := d.revision(cluster.FrontA, "member", res)
		editorView := d.revision(cluster.FrontB, "editor", res)
		Expect(editorView).To(Equal(ownerView))

		// The editor changes the data first, through another node.
		first := d.apply(cluster.Owner, "editor", res, opID("st-1"), "rows-insert", editorView, map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "ink", "Qty": 1}}})
		Expect(first.Status).To(Equal(200), first.String())
		ids, _ := body(first)["result"].(map[string]any)["ids"].([]any)
		Expect(ids).To(HaveLen(1))

		// The owner's stale tab: a data change and a schema change both fail
		// as stale and change nothing.
		for _, c := range []struct {
			tool string
			args map[string]any
		}{
			{"rows-insert", map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "late", "Qty": 9}}}},
			{"rows-update", map[string]any{"table": "Entries", "ids": ids, "set": map[string]any{"Qty": 7}}},
			{"columns-add", map[string]any{"table": "Entries", "column": "Note", "type": "text"}},
		} {
			op := opID("st-old")
			receiptsBefore := len(d.receipts(res))
			r := d.apply(cluster.FrontB, "member", res, op, c.tool, ownerView, c.args)
			Expect(len(d.receipts(res))).To(Equal(receiptsBefore), "a stale change leaves no receipt")
			Expect(r.Status).To(Equal(http.StatusConflict), c.tool+": "+r.String())
			Expect(errType(r)).To(Equal("stale"), c.tool)
			_ = op
		}
		Expect(d.rowCount(cluster.FrontA, "member", res)).To(Equal(2))

		// The owner changes the schema; the editor's old view is stale for a
		// row change that uses the new column too, and never half-applies.
		Expect(d.applyOK(cluster.FrontA, "member", res, "columns-add", map[string]any{"table": "Entries", "column": "Note", "type": "text"})).NotTo(BeNil())
		op := opID("st-schema")
		r := d.apply(cluster.FrontB, "editor", res, op, "rows-insert", editorView, map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "x", "Note": "n"}}})
		Expect(r.Status).To(Equal(http.StatusConflict), r.String())
		Expect(errType(r)).To(Equal("stale"))
		Expect(d.rowCount(cluster.Owner, "editor", res)).To(Equal(2))

		// A proposal prepared before the revision moved is rejected when it is
		// confirmed, and the data stays as it is.
		held := d.propose(cluster.FrontA, "editor", res, "write", opID("st-prop"), map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{map[string]any{"Item": "held", "Qty": 2}}})
		Expect(held.Status).To(Equal(200), held.String())
		heldID, _ := body(held)["id"].(string)
		heldDigest, _ := body(held)["digest"].(string)
		d.applyOK(cluster.Owner, "member", res, "columns-add", map[string]any{"table": "Entries", "column": "Tag", "type": "text"})
		receiptsAtHold := len(d.receipts(res))
		got := d.confirm(cluster.FrontB, "editor", heldID, heldDigest)
		Expect(body(got)["state"]).To(Equal("rejected"), got.String())
		Expect(len(d.receipts(res))).To(Equal(receiptsAtHold), "the rejected proposal left no receipt")
		Expect(d.rowCount(cluster.FrontA, "member", res)).To(Equal(2))

		// With the revision it read, the same person applies.
		ok := d.apply(cluster.FrontB, "editor", res, opID("st-fresh"), "rows-insert", d.revision(cluster.FrontB, "editor", res),
			map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "fresh", "Qty": 4}}})
		Expect(ok.Status).To(Equal(200), ok.String())
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(3))
	})

	It("replays an operation ID as one receipt from every node, conflicts on a different request and never applies twice or under a new ID", func() {
		before := d.rowCount(cluster.Owner, "member", res)
		receiptsBefore := len(d.receipts(res))
		rev := d.revision(cluster.FrontA, "member", res)
		args := map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "once", "Qty": 1}}}
		op := opID("replay")
		first := d.apply(cluster.FrontA, "editor", res, op, "rows-insert", rev, args)
		Expect(first.Status).To(Equal(200), first.String())
		hash := body(first)["requestHash"]
		Expect(hash).NotTo(BeEmpty())
		// No sticky session: the replays alternate over every node and over
		// the credential kinds, and each is the same receipt.
		for _, n := range append(append([]string{}, nodes...), nodes...) {
			again := d.apply(n, "editor", res, op, "rows-insert", rev, args)
			Expect(again.Status).To(Equal(200), n+": "+again.String())
			Expect(body(again)["revision"]).To(Equal(body(first)["revision"]))
			Expect(body(again)["requestHash"]).To(Equal(hash))
			Expect(body(again)["result"]).To(Equal(body(first)["result"]))
		}
		// Another request under the same ID is a conflict on every node.
		for _, n := range nodes {
			other := d.apply(n, "editor", res, op, "rows-insert", rev, map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "other", "Qty": 2}}})
			Expect(other.Status).To(Equal(http.StatusConflict), n+": "+other.String())
			Expect(errType(other)).To(Equal("operation_conflict"))
		}
		Expect(d.rowCount(cluster.FrontB, "member", res)).To(Equal(before + 1))
		Expect(len(d.receipts(res))).To(Equal(receiptsBefore+1), "the add-on committed the operation once")

		// A burst from the three nodes at once, one fresh ID each: every
		// acknowledged ID is applied exactly once, and the add-on holds no
		// receipt that no one asked for (no retry under a new ID).
		var wg sync.WaitGroup
		results := make([]*cluster.Response, 12)
		ops := make([]string, 12)
		gate := make(chan struct{})
		for i := range results {
			wg.Add(1)
			ops[i] = opID("burst")
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				<-gate
				// Revisions race, so a stale answer is correct; the caller
				// retries with the revision it reads, under the same ID.
				for attempt := 0; attempt < 40; attempt++ {
					n := nodes[(i+attempt)%len(nodes)]
					rev := d.revision(n, "member", res)
					r := d.apply(n, "member", res, ops[i], "rows-insert", rev, map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": fmt.Sprintf("b%d", i), "Qty": i}}})
					// A stale answer retries with the revision just read. A
					// busy owner (the add-on admits four calls at once) answers
					// outcome_unknown: the documented recovery is the same call
					// under the same ID, which the add-on applies once.
					stale := r.Status == http.StatusConflict && errType(r) == "stale"
					unknown := r.Status == http.StatusServiceUnavailable && errType(r) == "outcome_unknown"
					if !stale && !unknown {
						results[i] = r
						return
					}
				}
			}(i)
		}
		close(gate)
		wg.Wait()
		applied := 0
		for i, r := range results {
			Expect(r).NotTo(BeNil(), ops[i])
			Expect(r.Status).To(Equal(200), ops[i]+": "+r.String())
			applied++
		}
		Expect(len(d.receipts(res))).To(Equal(receiptsBefore+1+applied), "one receipt per acknowledged ID and none more")
		Expect(d.rowCount(cluster.FrontA, "member", res)).To(Equal(before + 1 + applied))
	})

	It("settles a revocation that races a confirmation: one outcome, applied at most once, and no work after the revocation", func() {
		unresolved := ""
		blockedSeen := 0
		for round := 0; round < 6; round++ {
			Expect(d.grant(cluster.Owner, "member", res, "editor", "editor").Status).To(Equal(200))
			if unresolved != "" {
				// A confirmation that the revocation cut short is unresolved
				// until its receipt is read, and it blocks every other write
				// on the database. The block is the host's, not the add-on's.
				blocked := d.propose(cluster.FrontB, "editor", res, "write", opID("blocked"), map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{map[string]any{"Item": "blocked"}}})
				Expect(blocked.Status).To(Equal(403), blocked.String())
				blockedSeen++
				rec := d.tab(nodes[round%3], "editor", "POST", "/api/addons/proposals/"+unresolved+"/reconcile", map[string]any{}, unresolved)
				Expect(rec.Status).To(Equal(200), rec.String())
				if body(rec)["state"] == "outcome-unknown" {
					// The add-on has no receipt, which does not prove it never
					// ran: the block stays until the owner role resolves it.
					stillBlocked := d.propose(cluster.FrontA, "editor", res, "write", opID("still"), map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{map[string]any{"Item": "blocked"}}})
					Expect(stillBlocked.Status).To(Equal(403), stillBlocked.String())
					listed := d.tab(nodes[(round+1)%3], "member", "GET", "/api/addons/resources/"+res+"/proposals?state=unresolved", nil, res)
					Expect(listed.Status).To(Equal(200), listed.String())
					entries, _ := body(listed)["proposals"].([]any)
					Expect(entries).NotTo(BeEmpty(), listed.String())
					// Abandoned earlier rounds stay listed; this one is the open one.
					var entry map[string]any
					for _, e := range entries {
						if m := e.(map[string]any); m["id"] == unresolved {
							entry = m
						}
					}
					Expect(entry).NotTo(BeNil(), listed.String())
					Expect(entry["state"]).To(Equal("outcome-unknown"))
					// Neither the proposer nor an administrator can resolve it.
					for _, user := range []string{"editor", "admin"} {
						denied := d.tab(cluster.FrontB, user, "POST", "/api/addons/proposals/"+unresolved+"/resolve", map[string]any{"digest": entry["digest"], "reason": "not mine"}, unresolved)
						Expect(denied.Status).To(Equal(403), user+": "+denied.String())
					}
					done := d.tab(nodes[(round+2)%3], "member", "POST", "/api/addons/proposals/"+unresolved+"/resolve", map[string]any{"digest": entry["digest"], "reason": "checked the rows"}, unresolved)
					Expect(done.Status).To(Equal(200), done.String())
					Expect(body(done)["state"]).To(Equal("abandoned-unknown"), done.String())
				}
				unresolved = ""
			}
			before := d.rowCount(cluster.Owner, "member", res)
			receiptsBefore := len(d.receipts(res))
			op := opID("race")
			p := d.propose(cluster.FrontA, "editor", res, "write", op, map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{map[string]any{"Item": "race", "Qty": round}}})
			Expect(p.Status).To(Equal(200), p.String())
			digest, _ := body(p)["digest"].(string)

			var confirmed, revoked *cluster.Response
			var wg sync.WaitGroup
			gate := make(chan struct{})
			wg.Add(2)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				<-gate
				confirmed = d.confirm(nodes[round%3], "editor", op, digest)
			}()
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				<-gate
				revoked = d.revoke(nodes[(round+1)%3], "member", res, "editor")
			}()
			close(gate)
			wg.Wait()
			Expect(revoked.Status).To(Equal(200), revoked.String())

			// The revocation is acknowledged: the editor is out everywhere.
			for _, n := range nodes {
				Expect(d.read(d.c.Token(n, "editor"), res, map[string]any{"tool": "rows-query", "table": "Entries"}).Status).To(Equal(403), n)
			}
			after := d.rowCount(cluster.FrontB, "member", res)
			switch {
			case confirmed.Status == 200:
				Expect(body(confirmed)["state"]).To(Equal("applied"))
				Expect(after).To(Equal(before+1), fmt.Sprintf("round %d: applied once", round))
			case confirmed.Status == 503 && errType(confirmed) == "outcome_unknown":
				unresolved = op
				Expect(after).To(BeElementOf(before, before+1), fmt.Sprintf("round %d: unknown, at most once", round))
				Expect(len(d.receipts(res))).To(Equal(receiptsBefore + after - before))
			default:
				Expect(confirmed.Status).To(BeElementOf(403, 409, 503), confirmed.String())
				Expect(after).To(Equal(before), fmt.Sprintf("round %d: denied, nothing applied (%s)", round, confirmed.String()))
				Expect(len(d.receipts(res))).To(Equal(receiptsBefore))
			}
			// The same confirmation again, after the revocation, does nothing.
			// It may return the stored record of what already happened, and it
			// can only say "applied" when the row is there.
			late := d.confirm(nodes[(round+2)%3], "editor", op, digest)
			if late.Status == 200 && body(late)["state"] == "applied" {
				Expect(after).To(Equal(before+1), "an applied record needs its row: "+late.String())
			}
			Expect(late.Status == 200 && body(late)["state"] == "applied" && after == before).To(BeFalse(), late.String())
			Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(after))
			if late.Status == 200 && (body(late)["state"] == "applied" || body(late)["state"] == "rejected") {
				// The repeated call read the receipt: nothing is left to settle.
				unresolved = ""
			}
		}
		// Leave the grant as the other specs expect it.
		Expect(d.grant(cluster.Owner, "member", res, "editor", "editor").Status).To(Equal(200))
	})
})
