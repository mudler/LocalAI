// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// trackerApp is a credentialed add-on made for these specs: one read tool and
// one write tool, a resource argument and its label. Nothing here depends on
// the add-on being any particular product.
func trackerApp() apps.App {
	schema := func(extra string) json.RawMessage {
		return json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string","maxLength":201}` + extra + `},"required":["repo"],"additionalProperties":false}`)
	}
	defs := []contract.ToolDefinition{
		{Name: "list-items", Description: "lists", Kind: contract.ToolKindRead, InputSchema: schema("")},
		{Name: "comment-item", Description: "comments", Kind: contract.ToolKindWrite, InputSchema: schema(`,"body":{"type":"string","maxLength":100}`)},
	}
	return apps.App{ID: "tracker", Name: "tracker", Generation: 1, Enabled: true, State: "running", Tools: []string{"list-items", "comment-item"},
		ToolDefinitions: defs, Credentialed: true, ResourceArgument: "repo", ResourceLabel: "project"}
}

// The result of a tool that runs with a credential says which connection
// acted, and the refusal for a missing selection is a plain note.
var _ = Describe("the connection that acted", func() {
	ctx := context.Background()
	var f *fake
	chat := func(connection tools.ConnectionNamer) *tools.ToolAdapter {
		return tools.NewChatToolAdapter(&actFake{fake: f}).WithConnectionNames(connection)
	}
	BeforeEach(func() {
		f = &fake{list: apps.List{Apps: []apps.App{trackerApp()}}}
		f.read = contract.Result{Payload: []byte(`{"files":[]}`)}
	})
	// The studio sets the binding in the context after it checked the model's
	// name against the chat's set; the adapter names it from there.
	names := map[string]string{"b-work": "Work", "b-home": "Home"}
	named := func(_ context.Context, _, bindingID string) string { return names[bindingID] }
	asWork := func() context.Context { return tools.WithConnection(ctx, "b-work") }

	It("states the connection and the repository in a read result", func() {
		a := chat(named)
		res, err := a.ExecuteChat(asWork(), member, tools.NewChatCall("bob", "chat-1"), "addon__tracker__list-items", args(`{"repo":"acme/widgets"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultData))
		Expect(res.Connection).To(Equal("as Work, project acme/widgets"))
		Expect(res.ConnectionID).To(Equal("b-work"))
		Expect(res.JSON()).To(ContainSubstring(`"connection":"as Work, project acme/widgets"`))
	})
	It("states it on a refusal too, so the person sees whose list refused", func() {
		f.readErr = &gateway.CredentialRefusal{Code: gateway.CodeResourceNotAllowed, Allowed: []string{"acme/widgets"}}
		res, err := chat(named).ExecuteChat(asWork(), member, tools.NewChatCall("bob", "chat-1"), "addon__tracker__list-items", args(`{"repo":"other/repo"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Code).To(Equal(tools.CodeResourceNotAllowed))
		Expect(res.Connection).To(Equal("as Work, project other/repo"))
		Expect(res.Allowed).To(Equal([]string{"acme/widgets"}))
	})
	It("states it on a proposal card", func() {
		f.proposal = gateway.Proposal{AddonID: "tracker", ResourceID: "r-0123456789", Action: "write", Digest: "d", State: gateway.ProposalPending, ConnectionID: "b-work", ConnectionName: "Work"}
		res, err := tools.NewToolAdapter(f).WithConnectionNames(named).ExecuteIn(asWork(), member, "", "addon__tracker__comment-item", args(`{"repo":"acme/widgets","body":"x"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultProposal))
		Expect(res.Proposal.Connection).To(Equal("as Work"), "the owner's record says it even with no chat")
	})
	It("says nothing when the call carries no connection", func() {
		res, err := chat(named).
			ExecuteChat(ctx, member, tools.NewChatCall("bob", "chat-1"), "addon__tracker__list-items", args(`{"repo":"acme/widgets"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Connection).To(BeEmpty())
	})
	It("turns a missing selection into a note the model can relay", func() {
		f.readErr = &gateway.CredentialRefusal{Code: gateway.CodeSelectConnection}
		res, err := chat(named).ExecuteChat(asWork(), member, tools.NewChatCall("bob", "chat-1"), "addon__tracker__list-items", args(`{"repo":"acme/widgets"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultError))
		Expect(res.Code).To(Equal(tools.CodeSelectConnection))
		Expect(res.Note).To(ContainSubstring("Tools menu"))
		Expect(res.Note).To(ContainSubstring("enable"))
		Expect(res.Note).To(ContainSubstring("do not pick one yourself"))
	})
})
