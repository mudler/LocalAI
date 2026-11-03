// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func githubApp() apps.App {
	GinkgoHelper()
	raw, e := os.ReadFile("../../../../addons/github/manifest.json")
	Expect(e).NotTo(HaveOccurred())
	m, e := contract.ParseManifest(raw)
	Expect(e).NotTo(HaveOccurred())
	slot := m.Capabilities.Credentials[0]
	return apps.App{ID: "github", Name: "github", Generation: 1, Enabled: true, State: "running", Tools: m.Capabilities.Tools,
		ToolDefinitions: m.Capabilities.ToolDefinitions, Credentialed: true, ResourceArgument: slot.ResourceArgument, ResourceLabel: slot.ResourceLabel}
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
		f = &fake{list: apps.List{Apps: []apps.App{githubApp()}}}
		f.read = contract.Result{Payload: []byte(`{"files":[]}`)}
	})
	named := func(context.Context, string, string) (string, string) { return "b-work", "Work" }

	It("states the connection and the repository in a read result", func() {
		a := chat(named)
		res, err := a.ExecuteChat(ctx, member, tools.NewChatCall("bob", "chat-1"), "addon__github__list-tree", args(`{"repo":"acme/widgets"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultData))
		Expect(res.Connection).To(Equal("as Work, repository acme/widgets"))
		Expect(res.ConnectionID).To(Equal("b-work"))
		Expect(res.JSON()).To(ContainSubstring(`"connection":"as Work, repository acme/widgets"`))
	})
	It("states it on a refusal too, so the person sees whose list refused", func() {
		f.readErr = &gateway.CredentialRefusal{Code: gateway.CodeResourceNotAllowed, Allowed: []string{"acme/widgets"}}
		res, err := chat(named).ExecuteChat(ctx, member, tools.NewChatCall("bob", "chat-1"), "addon__github__list-tree", args(`{"repo":"other/repo"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Code).To(Equal(tools.CodeResourceNotAllowed))
		Expect(res.Connection).To(Equal("as Work, repository other/repo"))
		Expect(res.Allowed).To(Equal([]string{"acme/widgets"}))
	})
	It("states it on a proposal card", func() {
		f.proposal = gateway.Proposal{AddonID: "github", ResourceID: "r-0123456789", Action: "write", Digest: "d", State: gateway.ProposalPending, ConnectionID: "b-work", ConnectionName: "Work"}
		res, err := tools.NewToolAdapter(f).WithConnectionNames(named).ExecuteIn(ctx, member, "", "addon__github__comment-issue", args(`{"repo":"acme/widgets","number":1,"body":"x"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultProposal))
		Expect(res.Proposal.Connection).To(Equal("as Work"), "the owner's record says it even with no chat")
	})
	It("says nothing when no connection is named, and for an add-on with no credential", func() {
		res, err := chat(func(context.Context, string, string) (string, string) { return "", "" }).
			ExecuteChat(ctx, member, tools.NewChatCall("bob", "chat-1"), "addon__github__list-tree", args(`{"repo":"acme/widgets"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Connection).To(BeEmpty())
	})
	It("turns a missing selection into a note the model can relay", func() {
		f.readErr = &gateway.CredentialRefusal{Code: gateway.CodeSelectConnection}
		res, err := chat(named).ExecuteChat(ctx, member, tools.NewChatCall("bob", "chat-1"), "addon__github__list-tree", args(`{"repo":"acme/widgets"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultError))
		Expect(res.Code).To(Equal(tools.CodeSelectConnection))
		Expect(res.Note).To(ContainSubstring("Tools menu"))
		Expect(res.Note).To(ContainSubstring("do not pick one yourself"))
	})
})
