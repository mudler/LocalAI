// SPDX-License-Identifier: MIT
package tablesacceptance_test

import (
	"fmt"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// settle drives every batch worker to a committed row: a worker whose reply
// was lost looks its operation up, and repeats the same call under the same
// ID. It never invents a new ID.
type worker struct {
	op    string
	args  map[string]any
	rev   map[string]any
	done  bool
	reply *cluster.Response
}

var _ = Describe("Tables crash and recovery with the real process", Ordered, func() {
	var (
		d   *deployment
		res string
	)

	BeforeAll(func() {
		d = deploy(start("0.1.0"), "0.1.0")
		res = d.seedLedger(cluster.FrontA, "member", "Crash", map[string]any{"Item": "seed", "Qty": 0})
		Expect(d.grant(cluster.Owner, "member", res, "editor", "editor").Status).To(Equal(200))
	})

	// batch runs n writers through the three nodes at once. kill runs once the
	// first reply came back; it is the crash.
	batch := func(n int, kill func()) []*worker {
		workers := make([]*worker, n)
		var wg sync.WaitGroup
		gate := make(chan struct{})
		first := make(chan struct{}, 1)
		signal := func() {
			select {
			case first <- struct{}{}:
			default:
			}
		}
		for i := range workers {
			w := &worker{op: opID("crash"), args: map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": fmt.Sprintf("w%d", i), "Qty": i}}}}
			workers[i] = w
			wg.Add(1)
			go func(i int, w *worker) {
				defer GinkgoRecover()
				defer wg.Done()
				<-gate
				for attempt := 0; attempt < 30 && !w.done; attempt++ {
					node := nodes[(i+attempt)%3]
					rev, err := d.c.Token(node, "member").Do("POST", "/api/addons/dispatch", map[string]any{
						"addonId": "tables", "operationId": opID("rev"), "generation": d.gen, "resourceId": res, "action": "read", "arguments": map[string]any{"tool": "databases-inspect"},
					}, nil)
					if err != nil || rev.Status != 200 {
						signal()
						return
					}
					w.rev, _ = payload(rev)["revision"].(map[string]any)
					extra := map[string]string{"X-LocalAI-Confirm": res}
					r, err := d.c.Session(node, "member").Do("POST", "/api/addons/resources/"+res+"/apply", map[string]any{
						"operationId": w.op, "generation": d.gen, "revision": w.rev, "tool": "rows-insert", "arguments": w.args,
					}, extra)
					if err != nil {
						// The connection ended: the reply is lost.
						signal()
						return
					}
					w.reply = r
					switch {
					case r.Status == 200:
						w.done = true
					case r.Status == http.StatusConflict && errType(r) == "stale":
						continue
					default:
						// The reply is lost or the service is gone: this
						// worker stops. The same ID may be asked about later.
						signal()
						return
					}
					signal()
				}
			}(i, w)
		}
		go func() {
			<-first
			kill()
		}()
		close(gate)
		wg.Wait()
		lost := 0
		for _, w := range workers {
			if !w.done {
				lost++
			}
		}
		GinkgoWriter.Printf("batch: %d of %d writers ended without an acknowledged commit\n", lost, n)
		return workers
	}

	// finish repeats the same call under the same ID until every worker is
	// committed, then checks that nothing applied twice and nothing was
	// applied under an ID nobody asked for.
	finish := func(workers []*worker, receiptsBefore, rowsBefore int) {
		GinkgoHelper()
		for _, w := range workers {
			for attempt := 0; attempt < 40 && !w.done; attempt++ {
				node := nodes[attempt%3]
				// The status lookup is read-only and says what is known.
				status := d.tab(node, "member", "GET", "/api/addons/resources/"+res+"/operations/"+w.op+"?kind=apply", nil, res)
				Expect(status.Status).To(Equal(200), status.String())
				w.rev = d.revision(node, "member", res)
				r := d.apply(node, "member", res, w.op, "rows-insert", w.rev, w.args)
				switch {
				case r.Status == 200:
					w.done = true
				case r.Status == http.StatusConflict && errType(r) == "stale":
				case r.Status == http.StatusServiceUnavailable:
					time.Sleep(200 * time.Millisecond)
				default:
					Fail(fmt.Sprintf("%s: unexpected %d %s", w.op, r.Status, r.String()))
				}
			}
			Expect(w.done).To(BeTrue(), w.op)
		}
		Expect(len(d.receipts(res))).To(Equal(receiptsBefore+len(workers)), "one receipt per operation ID, none more")
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(rowsBefore + len(workers)))
		items := d.read(d.c.Token(cluster.FrontA, "member"), res, map[string]any{"tool": "rows-query", "table": "Entries", "limit": 200})
		Expect(items.Status).To(Equal(200), items.String())
		seen := map[string]int{}
		rows, _ := payload(items)["rows"].([]any)
		for _, r := range rows {
			v := r.(map[string]any)
			if item, ok := v["Item"].(string); ok {
				seen[item]++
			} else if values, ok := v["values"].(map[string]any); ok {
				if item, ok := values["Item"].(string); ok {
					seen[item]++
				}
			}
		}
		for item, n := range seen {
			Expect(n).To(Equal(1), "row "+item+" exists once")
		}
	}

	It("keeps every acknowledged change and applies no change twice when the Tables process is killed in the middle of a batch", func() {
		receiptsBefore, rowsBefore := len(d.receipts(res)), d.rowCount(cluster.Owner, "member", res)
		old := d.tablesPID()
		Expect(old).NotTo(BeZero())
		workers := batch(16, func() { _ = syscall.Kill(old, syscall.SIGKILL) })
		Expect(cluster.Alive(old) && d.tablesPID() == old).To(BeFalse(), "the killed process is gone")
		d.waitTables(old)
		finish(workers, receiptsBefore, rowsBefore)
	})

	It("keeps every acknowledged change and applies no change twice when the owner is killed in the middle of a batch and restarted", func() {
		receiptsBefore, rowsBefore := len(d.receipts(res)), d.rowCount(cluster.Owner, "member", res)
		owner := d.c.Nodes[cluster.Owner]
		workers := batch(16, func() { owner.Kill() })
		// The add-on does not outlive its owner.
		Eventually(func() int { return d.tablesPID() }, 20*time.Second, 100*time.Millisecond).Should(BeZero())
		for _, n := range []string{cluster.FrontA, cluster.FrontB} {
			r := d.read(d.c.Token(n, "member"), res, map[string]any{"tool": "rows-query", "table": "Entries"})
			Expect(r.Status).To(Equal(503), n+": "+r.String())
		}
		Expect(owner.Start()).To(Succeed())
		Expect(owner.Ready(120 * time.Second)).To(Succeed())
		d.waitState("running")
		finish(workers, receiptsBefore, rowsBefore)
	})

	It("blocks every other write while a confirmation is unresolved, keeps reading, and accepts writes again only after the owner role resolves it", func() {
		before := d.rowCount(cluster.Owner, "member", res)
		receiptsBefore := len(d.receipts(res))
		op := opID("frozen")
		p := d.propose(cluster.FrontA, "editor", res, "write", op, map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{map[string]any{"Item": "frozen", "Qty": 1}}})
		Expect(p.Status).To(Equal(200), p.String())
		digest, _ := body(p)["digest"].(string)

		// The process stops where it stands: the apply is handed over and
		// never answered. Then it is killed. The owner cannot know whether
		// the change ran.
		pid := d.tablesPID()
		Expect(syscall.Kill(pid, syscall.SIGSTOP)).To(Succeed())
		var confirmed *cluster.Response
		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			confirmed = d.confirm(cluster.FrontB, "editor", op, digest)
		}()
		Eventually(func() string {
			r, err := d.c.Token(cluster.FrontA, "editor").Do("GET", "/api/addons/proposals/"+op, nil, nil)
			if err != nil || r.Status != 200 {
				return ""
			}
			s, _ := body(r)["state"].(string)
			return s
		}, 30*time.Second, 50*time.Millisecond).Should(Equal("executing"))
		Expect(syscall.Kill(pid, syscall.SIGKILL)).To(Succeed())
		select {
		case <-done:
		case <-time.After(90 * time.Second):
			Fail("the confirmation did not end after the process was killed")
		}
		Expect(confirmed.Status).To(Equal(503), confirmed.String())
		Expect(errType(confirmed)).To(Equal("outcome_unknown"), confirmed.String())
		Expect(body(confirmed)["error"].(map[string]any)["operationId"]).To(Equal(op))
		d.waitTables(pid)

		// Reads continue; every other write is blocked, for the owner role
		// with the operation to resolve and for the others without it.
		for _, n := range nodes {
			Expect(d.read(d.c.Token(n, "editor"), res, map[string]any{"tool": "rows-query", "table": "Entries"}).Status).To(Equal(200), n)
		}
		ownerWrite := d.apply(cluster.FrontB, "member", res, opID("blk"), "rows-insert", d.revisionOf(res), map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "blocked"}}})
		Expect(ownerWrite.Status).To(Equal(403), ownerWrite.String())
		Expect(errType(ownerWrite)).To(Equal("outcome_blocked"))
		Expect(ownerWrite.String()).To(ContainSubstring(op))
		other := d.propose(cluster.FrontA, "editor", res, "write", opID("blk"), map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{map[string]any{"Item": "blocked"}}})
		Expect(other.Status).To(Equal(403), other.String())
		Expect(other.String()).NotTo(ContainSubstring(op), "the operation ID of another person is not disclosed")

		// The lookup finds no receipt: the process died before it committed.
		// That is not a proof, so the block stays until the owner resolves it.
		rec := d.tab(cluster.FrontA, "editor", "POST", "/api/addons/proposals/"+op+"/reconcile", map[string]any{}, op)
		Expect(rec.Status).To(Equal(200), rec.String())
		Expect(body(rec)["state"]).To(Equal("outcome-unknown"))
		Expect(d.apply(cluster.FrontB, "member", res, opID("blk"), "rows-insert", d.revisionOf(res), map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "blocked"}}}).Status).To(Equal(403))
		listed := d.tab(cluster.Owner, "member", "GET", "/api/addons/resources/"+res+"/proposals?state=unresolved", nil, res)
		Expect(listed.Status).To(Equal(200), listed.String())
		var entry map[string]any
		for _, e := range body(listed)["proposals"].([]any) {
			if m := e.(map[string]any); m["id"] == op {
				entry = m
			}
		}
		Expect(entry).NotTo(BeNil(), listed.String())
		Expect(d.tab(cluster.FrontB, "admin", "POST", "/api/addons/proposals/"+op+"/resolve", map[string]any{"digest": entry["digest"], "reason": "x"}, op).Status).To(Equal(403))
		ok := d.tab(cluster.FrontB, "member", "POST", "/api/addons/proposals/"+op+"/resolve", map[string]any{"digest": entry["digest"], "reason": "the process died before it committed"}, op)
		Expect(ok.Status).To(Equal(200), ok.String())
		Expect(body(ok)["state"]).To(Equal("abandoned-unknown"))
		// The unresolved change never ran, and writes are accepted again.
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(before))
		Expect(len(d.receipts(res))).To(Equal(receiptsBefore))
		after := d.apply(cluster.FrontA, "member", res, opID("after"), "rows-insert", d.revisionOf(res), map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "after"}}})
		Expect(after.Status).To(Equal(200), after.String())
	})
})

var _ = Describe("Tables when the add-on is disabled, updated, removed and installed again", Ordered, func() {
	var (
		d   *deployment
		res string
	)

	BeforeAll(func() {
		d = deploy(start("0.1.0", "0.2.0"), "0.1.0")
		res = d.seedLedger(cluster.FrontA, "member", "Lifecycle", map[string]any{"Item": "kept", "Qty": 1})
		Expect(d.grant(cluster.Owner, "member", res, "editor", "editor").Status).To(Equal(200))
		Expect(d.grant(cluster.Owner, "member", res, "viewer", "viewer").Status).To(Equal(200))
	})

	It("denies the open tabs of a disabled add-on everywhere and serves them again, with the same data, when it is enabled", func() {
		gen := d.gen
		tabRev := d.revisionOf(res)
		Expect(d.lifecycle(cluster.FrontB, "disable", gen, "").Status).To(Equal(200))
		for _, n := range nodes {
			w := d.apply(n, "editor", res, opID("dis"), "rows-insert", tabRev, map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "x"}}})
			Expect(w.Status).To(BeElementOf(403, 503), n+": "+w.String())
			r := d.read(d.c.Token(n, "viewer"), res, map[string]any{"tool": "rows-query", "table": "Entries"})
			Expect(r.Status).To(BeElementOf(403, 503), n+": "+r.String())
			// A disabled add-on offers the model no tools: the turn is a
			// plain answer and no tool ran.
			chat := d.chatTool(n, "member", "rows-query", map[string]any{"resourceId": res, "table": "Entries"})
			Expect(chat.Status).To(Equal(200), n+": "+chat.String())
			Expect(chat.String()).To(ContainSubstring("This is a mocked response."))
			Expect(chat.String()).NotTo(ContainSubstring("kept"), n)
		}
		// A member does not see a disabled add-on; the administrator does.
		for _, n := range nodes {
			Expect(d.app(d.c.Session(n, "member"))).To(BeNil(), n)
			Expect(d.app(d.c.Session(n, "admin"))).NotTo(BeNil(), n)
		}
		Expect(d.lifecycle(cluster.FrontA, "enable", gen, "").Status).To(Equal(200))
		d.waitState("running")
		for _, n := range nodes {
			Expect(d.rowCount(n, "viewer", res)).To(Equal(1), n)
		}
		// Nothing the disabled add-on was asked left a proposal behind.
		Expect(d.pending(cluster.Owner, "member")).To(BeEmpty())
		Expect(d.pending(cluster.Owner, "editor")).To(BeEmpty())
	})

	It("keeps data and grants when the add-on is removed and installed again, and refuses what the old generation prepared", func() {
		oldGen := d.gen
		oldRev := d.revisionOf(res)
		held := d.propose(cluster.FrontA, "editor", res, "write", opID("old-gen"), map[string]any{"tool": "rows-insert", "table": "Entries", "rows": []any{map[string]any{"Item": "held"}}})
		Expect(held.Status).To(Equal(200), held.String())
		heldID, _ := body(held)["id"].(string)
		heldDigest, _ := body(held)["digest"].(string)

		Expect(d.lifecycle(cluster.FrontB, "disable", oldGen, "").Status).To(Equal(200))
		Expect(d.lifecycle(cluster.FrontA, "uninstall", 0, "").Status).To(Equal(200))
		for _, n := range nodes {
			r := d.read(d.c.Token(n, "viewer"), res, map[string]any{"tool": "rows-query", "table": "Entries"})
			Expect(r.Status).To(BeElementOf(403, 503), n+": "+r.String())
		}
		Expect(d.lifecycle(cluster.FrontA, "install", 0, "0.1.0").Status).To(Equal(200))
		cur := d.app(d.c.Token(cluster.FrontA, "admin"))
		Expect(cur).NotTo(BeNil())
		Expect(d.lifecycle(cluster.FrontB, "enable", uint64(cur["generation"].(float64)), "").Status).To(Equal(200))
		d.waitState("running")
		Expect(d.gen).NotTo(Equal(oldGen), "the new installation is a new generation")

		// Data and grants outlived the removal, for every role and node.
		for _, n := range nodes {
			Expect(d.rowCount(n, "viewer", res)).To(Equal(1), n)
			Expect(d.rowCount(n, "editor", res)).To(Equal(1), n)
			Expect(d.rowCount(n, "member", res)).To(Equal(1), n)
			stranger := d.read(d.c.Token(n, "friend"), res, map[string]any{"tool": "rows-query", "table": "Entries"})
			Expect(stranger.Status).To(Equal(403), n)
		}
		// The tab and the proposal of the old generation do nothing.
		stale := d.tab(cluster.FrontB, "editor", "POST", "/api/addons/resources/"+res+"/apply", map[string]any{
			"operationId": opID("stale-gen"), "generation": oldGen, "revision": oldRev, "tool": "rows-insert", "arguments": map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "old"}}},
		}, res)
		Expect(stale.Status).To(Equal(409), stale.String())
		Expect(errType(stale)).To(Equal("stale"))
		got := d.confirm(cluster.FrontA, "editor", heldID, heldDigest)
		Expect(got.Status).To(Equal(403), got.String())
		Expect(errType(got)).To(Equal("addon_error"))
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(1))
		// The proposal of the old generation is still listed, unchanged and
		// never applied: it names the generation it was made for, which no
		// confirmation can reach any more, and it expires.
		left := d.pending(cluster.Owner, "editor")
		Expect(left).To(HaveLen(1))
		Expect(left[0]["id"]).To(Equal(heldID))
		Expect(left[0]["generation"]).To(BeNumerically("==", oldGen))
		again := d.confirm(cluster.FrontB, "editor", heldID, heldDigest)
		Expect(again.Status).To(Equal(403), again.String())
		// New work in the new generation succeeds.
		Expect(d.apply(cluster.FrontB, "editor", res, opID("new-gen"), "rows-insert", d.revisionOf(res), map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "new"}}}).Status).To(Equal(200))
	})
})
