// SPDX-License-Identifier: MIT
package tables_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// callTool runs one model tool call as a user's API key, the credential of an
// unattended agent. The adapter is the real one over the real owner gateway.
func (h *host) callTool(user, name, body string) (tools.ToolResult, error) {
	GinkgoHelper()
	adapter := tools.NewToolAdapter(h.app.AddonGateway())
	return adapter.Execute(context.Background(), h.actor(user), name, json.RawMessage(body))
}

func (h *host) pending(user string) []map[string]any {
	GinkgoHelper()
	got := h.send("GET", "/api/addons/proposals?state=pending", "", h.bearer(user))
	Expect(got.code).To(Equal(200), "%v", got.body)
	var out []map[string]any
	list, _ := got.body["proposals"].([]any)
	for _, x := range list {
		out = append(out, x.(map[string]any))
	}
	return out
}

var _ = Describe("Tables tools under the real owner", Label("application"), func() {
	It("offers the manifest tools to a permitted user and nothing to a user without the feature or after revocation", func() {
		h := newHost()
		lib := h.library()
		h.start()
		DeferCleanup(h.stop)
		h.seed(lib)
		adapter := tools.NewToolAdapter(h.app.AddonGateway())
		ctx := context.Background()

		defs, e := adapter.Discover(ctx, h.actor("alice"))
		Expect(e).NotTo(HaveOccurred())
		Expect(defs).To(HaveLen(15))
		for _, d := range defs {
			Expect(d.Name).To(HavePrefix("addon__tables__"))
		}
		defs, e = adapter.Discover(ctx, h.actor("bob"))
		Expect(e).NotTo(HaveOccurred())
		Expect(defs).To(HaveLen(15))

		// Another user's identity cannot be claimed by changing a field of
		// the actor: the owner checks the stored credential.
		forged := h.actor("bob")
		forged.CredentialID = "key:" + h.keys["alice"]
		_, e = adapter.Discover(ctx, forged)
		Expect(e).To(HaveOccurred())

		// Revocation of the feature withdraws every tool at once.
		Expect(h.app.AuthDB().Where("user_id = ?", "bob").Delete(&auth.UserPermission{}).Error).To(Succeed())
		_, e = adapter.Discover(ctx, h.actor("bob"))
		Expect(e).To(HaveOccurred())
		Expect(gateway.StatusFor(e)).To(Equal(403))
		_, e = h.callTool("bob", "addon__tables__databases-create", `{"name":"Nope"}`)
		Expect(e).To(HaveOccurred())
		Expect(gateway.StatusFor(e)).To(Equal(403))
	})

	It("stores every change as a pending proposal and applies it only on the proposer's own session, exactly once", func() {
		h := newHost()
		lib := h.library()
		h.start()
		DeferCleanup(h.stop)
		h.seed(lib)

		created, e := h.callTool("alice", "addon__tables__databases-create", `{"name":"Ledger"}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(created.Kind).To(Equal(tools.ResultProposal))
		ref := created.Proposal
		Expect(ref.State).To(Equal("pending"))
		Expect(ref.Count).To(Equal(uint64(1)))
		Expect(ref.ResourceID).NotTo(BeEmpty())
		// Nothing exists yet, for anyone, including a read by the same agent.
		Expect(accessibleTo(h, "alice")).To(BeEmpty())
		_, e = h.callTool("alice", "addon__tables__databases-inspect", fmt.Sprintf(`{"resourceId":%q}`, ref.ResourceID))
		Expect(e).To(HaveOccurred())

		// The proposal waits in the proposer's own list, not in anyone else's.
		listed := h.pending("alice")
		Expect(listed).To(HaveLen(1))
		Expect(listed[0]["id"]).To(Equal(ref.ID))
		Expect(listed[0]["digest"]).To(Equal(ref.Digest))
		Expect(h.pending("bob")).To(BeEmpty())
		// The card's authoritative record is the same one the tool referenced.
		card := h.send("GET", "/api/addons/proposals/"+ref.ID, "", h.bearer("alice"))
		Expect(card.code).To(Equal(200))
		Expect(card.body["resourceId"]).To(Equal(ref.ResourceID))
		Expect(card.body["effects"].(map[string]any)["count"]).To(BeEquivalentTo(1))
		Expect(h.send("GET", "/api/addons/proposals/"+ref.ID, "", h.bearer("bob")).code).To(Equal(403))

		// A tool call, a key and another user's session cannot approve.
		Expect(h.send("POST", "/api/addons/proposals/"+ref.ID+"/confirm", fmt.Sprintf(`{"digest":%q}`, ref.Digest), h.bearer("alice")).code).To(Equal(403))
		toolCall := func(r *http.Request) { h.humanTab("alice", ref.ID)(r); r.Header.Set("X-LocalAI-Tool-Call", "1") }
		Expect(h.send("POST", "/api/addons/proposals/"+ref.ID+"/confirm", fmt.Sprintf(`{"digest":%q}`, ref.Digest), toolCall).code).To(Equal(403))
		Expect(h.confirm("bob", ref.ID, ref.Digest).code).To(Equal(403))
		Expect(accessibleTo(h, "alice")).To(BeEmpty())

		done := h.confirm("alice", ref.ID, ref.Digest)
		Expect(done.code).To(Equal(200), "%v", done.body)
		Expect(done.body["state"]).To(Equal("applied"))
		Expect(h.pending("alice")).To(BeEmpty())
		res := ref.ResourceID

		step := func(name, body string) *tools.ProposalRef {
			GinkgoHelper()
			r, e := h.callTool("alice", name, body)
			Expect(e).NotTo(HaveOccurred())
			Expect(r.Kind).To(Equal(tools.ResultProposal))
			ok := h.confirm("alice", r.Proposal.ID, r.Proposal.Digest)
			Expect(ok.code).To(Equal(200), "%v", ok.body)
			Expect(ok.body["state"]).To(Equal("applied"), "%v", ok.body)
			return r.Proposal
		}
		step("addon__tables__tables-create", fmt.Sprintf(`{"resourceId":%q,"table":"Entries"}`, res))
		step("addon__tables__columns-add", fmt.Sprintf(`{"resourceId":%q,"table":"Entries","column":"Memo","type":"text"}`, res))

		rows, e := h.callTool("alice", "addon__tables__rows-insert", fmt.Sprintf(`{"resourceId":%q,"table":"Entries","rows":[{"Memo":"rent"},{"Memo":"food"}]}`, res))
		Expect(e).NotTo(HaveOccurred())
		Expect(rows.Proposal.Count).To(Equal(uint64(2)))
		count := func() int {
			GinkgoHelper()
			r, e := h.callTool("alice", "addon__tables__rows-query", fmt.Sprintf(`{"resourceId":%q,"table":"Entries"}`, res))
			Expect(e).NotTo(HaveOccurred())
			Expect(r.Kind).To(Equal(tools.ResultData))
			var p page
			Expect(json.Unmarshal(r.Result, &p)).To(Succeed())
			return len(p.Rows)
		}
		Expect(count()).To(Equal(0), "a proposal changes nothing")
		Expect(h.confirm("alice", rows.Proposal.ID, rows.Proposal.Digest).code).To(Equal(200))
		Expect(count()).To(Equal(2))
		// A repeated confirmation returns the stored receipt and runs nothing.
		again := h.confirm("alice", rows.Proposal.ID, rows.Proposal.Digest)
		Expect(again.body["state"]).To(Equal("applied"))
		Expect(count()).To(Equal(2))
	})

	It("keeps the roles of the host for tool calls and rejects forged fields before the owner sees them", func() {
		h := newHost()
		lib := h.library()
		h.start()
		DeferCleanup(h.stop)
		h.seed(lib)
		ctx := context.Background()

		created, e := h.callTool("alice", "addon__tables__databases-create", `{"name":"Team"}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(h.confirm("alice", created.Proposal.ID, created.Proposal.Digest).code).To(Equal(200))
		res := created.Proposal.ResourceID
		mk := func(table string) {
			r, e := h.callTool("alice", "addon__tables__tables-create", fmt.Sprintf(`{"resourceId":%q,"table":%q}`, res, table))
			Expect(e).NotTo(HaveOccurred())
			Expect(h.confirm("alice", r.Proposal.ID, r.Proposal.Digest).code).To(Equal(200))
		}
		mk("Entries")

		// A stranger sees nothing: the same denial for every tool.
		for _, call := range []struct{ tool, body string }{
			{"addon__tables__databases-inspect", fmt.Sprintf(`{"resourceId":%q}`, res)},
			{"addon__tables__rows-query", fmt.Sprintf(`{"resourceId":%q,"table":"Entries"}`, res)},
			{"addon__tables__rows-insert", fmt.Sprintf(`{"resourceId":%q,"table":"Entries","rows":[{}]}`, res)},
			{"addon__tables__tables-drop", fmt.Sprintf(`{"resourceId":%q,"table":"Entries"}`, res)},
		} {
			_, e := h.callTool("bob", call.tool, call.body)
			Expect(e).To(HaveOccurred(), call.tool)
			Expect(gateway.StatusFor(e)).To(Equal(403), call.tool)
		}

		key := policy.ResourceKey{AddonID: "tables", ResourceID: res}
		Expect(h.app.AddonPolicy().SetGrant(ctx, h.actor("alice"), key, "bob", policy.RoleViewer)).To(Succeed())
		_, e = h.callTool("bob", "addon__tables__rows-query", fmt.Sprintf(`{"resourceId":%q,"table":"Entries"}`, res))
		Expect(e).NotTo(HaveOccurred())
		_, e = h.callTool("bob", "addon__tables__rows-insert", fmt.Sprintf(`{"resourceId":%q,"table":"Entries","rows":[{}]}`, res))
		Expect(gateway.StatusFor(e)).To(Equal(403))

		Expect(h.app.AddonPolicy().SetGrant(ctx, h.actor("alice"), key, "bob", policy.RoleEditor)).To(Succeed())
		r, e := h.callTool("bob", "addon__tables__rows-insert", fmt.Sprintf(`{"resourceId":%q,"table":"Entries","rows":[{}]}`, res))
		Expect(e).NotTo(HaveOccurred())
		Expect(r.Kind).To(Equal(tools.ResultProposal))
		// An editor never changes the schema, by tool or by any other name.
		_, e = h.callTool("bob", "addon__tables__tables-drop", fmt.Sprintf(`{"resourceId":%q,"table":"Entries"}`, res))
		Expect(gateway.StatusFor(e)).To(Equal(403))
		before := len(h.pending("bob"))

		// Forged identity, role and approval fields and approval aliases never
		// create a proposal and never reach the addon.
		for _, field := range []string{`"actor":"alice"`, `"userId":"alice"`, `"role":"owner"`, `"approved":true`, `"proposalId":"x"`, `"tool":"tables-drop"`} {
			_, e := h.callTool("bob", "addon__tables__rows-insert", fmt.Sprintf(`{"resourceId":%q,"table":"Entries","rows":[{}],%s}`, res, field))
			Expect(e).To(MatchError(tools.ErrForbiddenArgument), field)
		}
		for _, name := range []string{"confirm", "approve", "addon__tables__confirm", "addon__tables__share", "addon__tables__databases-delete", "addon__tables__databases-purge"} {
			_, e := h.callTool("bob", name, fmt.Sprintf(`{"resourceId":%q}`, res))
			Expect(e).To(MatchError(tools.ErrUnknownTool), name)
		}
		Expect(h.pending("bob")).To(HaveLen(before))
	})

	It("lists at most the bound of pending proposals and refuses more than the user's quota", func() {
		h := newHost()
		lib := h.library()
		h.start()
		DeferCleanup(h.stop)
		h.seed(lib)
		created, e := h.callTool("alice", "addon__tables__databases-create", `{"name":"Many"}`)
		Expect(e).NotTo(HaveOccurred())
		Expect(h.confirm("alice", created.Proposal.ID, created.Proposal.Digest).code).To(Equal(200))
		res := created.Proposal.ResourceID
		for _, step := range []string{
			fmt.Sprintf(`{"resourceId":%q,"table":"T"}`, res),
		} {
			r, e := h.callTool("alice", "addon__tables__tables-create", step)
			Expect(e).NotTo(HaveOccurred())
			Expect(h.confirm("alice", r.Proposal.ID, r.Proposal.Digest).code).To(Equal(200))
		}
		for i := 0; i < policy.MaxProposalsPerUser; i++ {
			_, e := h.callTool("alice", "addon__tables__rows-insert", fmt.Sprintf(`{"resourceId":%q,"table":"T","rows":[{}]}`, res))
			Expect(e).NotTo(HaveOccurred(), "proposal %d", i)
		}
		Expect(len(h.pending("alice"))).To(Equal(gateway.MaxPendingListed))
		_, e = h.callTool("alice", "addon__tables__rows-insert", fmt.Sprintf(`{"resourceId":%q,"table":"T","rows":[{}]}`, res))
		Expect(e).To(HaveOccurred())
		Expect(gateway.StatusFor(e)).To(Equal(403))
	})
})
