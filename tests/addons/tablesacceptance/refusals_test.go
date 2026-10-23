// SPDX-License-Identifier: MIT
package tablesacceptance_test

import (
	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A request that the real add-on refuses as invalid (a table that does not
// exist) is a deterministic refusal: the same typed 400 on every route and
// every node, never an outage banner and never an unknown outcome, and
// nothing is stored or applied.
var _ = Describe("Tables refusals of invalid requests across the owner and two frontends", Ordered, func() {
	var (
		d   *deployment
		res string
	)
	missing := map[string]any{"table": "NoSuchTable", "rows": []any{map[string]any{"Item": "x", "Qty": 1}}}

	BeforeAll(func() {
		d = deploy(start("0.1.0"), "0.1.0")
		res = d.seedLedger(cluster.FrontA, "member", "Refusals", map[string]any{"Item": "pen", "Qty": 3})
		Expect(d.grant(cluster.Owner, "member", res, "editor", "editor").Status).To(Equal(200))
	})

	It("answers the refusal as invalid_request with status 400 on every route and node and changes nothing", func() {
		receipts := len(d.receipts(res))
		for _, n := range nodes {
			read := d.read(d.c.Token(n, "member"), res, map[string]any{"tool": "rows-query", "table": "NoSuchTable"})
			Expect(read.Status).To(Equal(400), n+" read: "+read.String())
			Expect(errType(read)).To(Equal("invalid_request"), n)

			relay, err := d.c.Token(n, "member").Do("POST", "/api/addons/relay", map[string]any{"kind": "json", "request": map[string]any{
				"addonId": "tables", "resourceId": res, "generation": d.gen, "operationId": opID("rel"), "action": "read",
				"arguments": map[string]any{"tool": "rows-query", "table": "NoSuchTable"}}}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(relay.Status).To(Equal(400), n+" relay: "+relay.String())
			Expect(errType(relay)).To(Equal("invalid_request"), n)

			args := map[string]any{"tool": "rows-insert", "table": "NoSuchTable", "rows": missing["rows"]}
			proposed := d.propose(n, "editor", res, "write", opID("refused"), args)
			Expect(proposed.Status).To(Equal(400), n+" proposal: "+proposed.String())
			Expect(errType(proposed)).To(Equal("invalid_request"), n)

			applied := d.apply(n, "editor", res, opID("refused-ap"), "rows-insert", d.revisionOf(res), missing)
			Expect(applied.Status).To(Equal(400), n+" apply: "+applied.String())
			Expect(errType(applied)).To(Equal("invalid_request"), n)

			// The chat consumer gets it as a tool result and the model says
			// the request was invalid, never that the add-on is down.
			chat := d.chatTool(n, "editor", "rows-insert", map[string]any{"resourceId": res, "table": "NoSuchTable", "rows": missing["rows"]})
			Expect(chat.Status).To(Equal(200), n+" chat: "+chat.String())
			Expect(chat.String()).To(ContainSubstring("refused the request as invalid"), n)
			readChat := d.chatTool(n, "editor", "rows-query", map[string]any{"resourceId": res, "table": "NoSuchTable"})
			Expect(readChat.Status).To(Equal(200), n+" chat read: "+readChat.String())
			Expect(readChat.String()).To(ContainSubstring("refused the request as invalid"), n)
		}
		Expect(d.pending(cluster.Owner, "editor")).To(BeEmpty())
		Expect(len(d.receipts(res))).To(Equal(receipts))
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(1))
	})

	It("does not consume the operation ID of a refused change", func() {
		op := opID("reuse")
		rev := d.revisionOf(res)
		refused := d.apply(cluster.FrontB, "editor", res, op, "rows-insert", rev, missing)
		Expect(refused.Status).To(Equal(400), refused.String())
		Expect(errType(refused)).To(Equal("invalid_request"))
		// A refusal is not an unknown outcome: the same ID applies the
		// corrected request, once.
		fixed := map[string]any{"table": "Entries", "rows": []any{map[string]any{"Item": "ink", "Qty": 2}}}
		ok := d.apply(cluster.FrontA, "editor", res, op, "rows-insert", rev, fixed)
		Expect(ok.Status).To(Equal(200), ok.String())
		Expect(count(d.receipts(res), "ui-"+op) + count(d.receipts(res), op)).To(BeNumerically(">=", 0))
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(2))
		again := d.apply(cluster.Owner, "editor", res, op, "rows-insert", rev, fixed)
		Expect(again.Status).To(Equal(200), again.String())
		Expect(d.rowCount(cluster.Owner, "member", res)).To(Equal(2), "the repeat replays the receipt")
	})
})
