package application

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type changeOwner struct{ app apps.App }

func (o changeOwner) Apps(context.Context, contract.Actor) (apps.List, error) {
	return apps.List{Apps: []apps.App{o.app}}, nil
}
func (changeOwner) Dispatch(context.Context, contract.Actor, contract.Request) (contract.Result, error) {
	return contract.Result{}, nil
}
func (changeOwner) Propose(context.Context, contract.Actor, contract.Request) (gateway.Proposal, error) {
	return gateway.Proposal{}, nil
}

type forbiddenOwner struct{ changeOwner }

func (forbiddenOwner) Apps(context.Context, contract.Actor) (apps.List, error) {
	return apps.List{}, gateway.ErrForbidden
}

var _ = Describe("the studio add-ons", func() {
	It("lets a chat act as its user with the chat credential", func() {
		s := &studioAddons{mode: auth.AddonModeNamedUser}
		a, err := s.actor("alice")
		Expect(err).ToNot(HaveOccurred())
		Expect(a.UserID).To(Equal("alice"))
		Expect(a.CredentialID).To(Equal(auth.AddonChatsCredential))
		Expect(a.Mode).To(Equal(auth.AddonModeNamedUser))
		for _, bad := range []string{"", studio.AnonymousPrincipal, auth.AddonLocalOwnerID, "legacy-api-key"} {
			_, err := s.actor(bad)
			Expect(err).To(HaveOccurred(), "actor(%q) was accepted in named-user mode", bad)
		}
		owner := &studioAddons{mode: auth.AddonModeLocalOwner}
		oa, err := owner.actor(studio.AnonymousPrincipal)
		Expect(err).ToNot(HaveOccurred(), "local-owner actor")
		Expect(oa.UserID).To(Equal(auth.AddonLocalOwnerID))
		_, err = (&studioAddons{}).actor("alice")
		Expect(err).To(HaveOccurred(), "an unknown mode gave an actor")
		_, err = s.actor("")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(gateway.ErrForbidden.Error()), "a refused actor is not a denial")
	})

	It("shows a proposal to the model as a text that says nothing changed", func() {
		res := addontools.ToolResult{Kind: addontools.ResultProposal, Tool: "t", Proposal: &addontools.ProposalRef{ID: "p1", AddonID: "tables", Action: "rows-delete"}}
		got := studioAddonText(res)
		for _, want := range []string{"tables", "rows-delete", "p1", "Nothing has been changed", "Add-ons page"} {
			Expect(got).To(ContainSubstring(want), "proposal text")
		}
		data := addontools.ToolResult{Kind: addontools.ResultData, Tool: "t", Result: []byte(`{"n":1}`)}
		Expect(studioAddonText(data)).To(ContainSubstring(`"n":1`), "a read result lost its content")
	})

	It("lets a chat credential confirm while session and key rules stay the same", func() {
		named := confirmCredential(auth.AddonModeNamedUser)
		chat := contract.Actor{UserID: "alice", CredentialID: auth.AddonChatsCredential, Mode: auth.AddonModeNamedUser}
		Expect(named(chat)).To(BeTrue(), "the chat credential cannot confirm what the chat stored")
		Expect(named(contract.Actor{UserID: "alice", CredentialID: "session:s1", Mode: auth.AddonModeNamedUser})).To(BeTrue(), "a browser session lost the right to confirm")
		for _, id := range []string{"key:k1", "session:", "", "mcp:1", "Chats"} {
			Expect(named(contract.Actor{UserID: "alice", CredentialID: id, Mode: auth.AddonModeNamedUser})).To(BeFalse(), "credential %q may confirm", id)
		}
		owner := confirmCredential(auth.AddonModeLocalOwner)
		Expect(owner(contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner})).To(BeTrue(), "the local owner cannot confirm")
		Expect(owner(chat)).To(BeFalse(), "the chat credential confirms in local-owner mode, where chats act as the local owner")
	})

	It("marks a change tool as an action, so an unattended run without an agent does not get it", func() {
		schema := json.RawMessage(`{"type":"object","properties":{"resourceId":{"type":"string"},"table":{"type":"string"},"rows":{"type":"array","items":{"type":"object"}}},"required":["resourceId","table","rows"],"additionalProperties":false}`)
		read := json.RawMessage(`{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"],"additionalProperties":false}`)
		app := apps.App{ID: "tables", Generation: 1, Enabled: true, State: "running", Tools: []string{"rows-insert", "rows-get"},
			ToolDefinitions: []contract.ToolDefinition{
				{Name: "rows-insert", Kind: contract.ToolKindWrite, Description: "Insert rows.", InputSchema: schema},
				{Name: "rows-get", Kind: contract.ToolKindRead, Description: "Read rows.", InputSchema: read},
			}}
		s := &studioAddons{adapter: addontools.NewChatToolAdapter(changeOwner{app}), mode: auth.AddonModeNamedUser}
		got, err := s.Tools(context.Background(), "alice")
		Expect(err).ToNot(HaveOccurred())
		action := map[string]bool{}
		for _, d := range got {
			action[d.Name] = d.Action
		}
		Expect(action["addon__tables__rows-insert"]).To(BeTrue(), "Action flags = %v, want the write tool marked", action)
		Expect(action["addon__tables__rows-get"]).To(BeFalse(), "Action flags = %v, want the read tool not marked", action)
	})

	It("marks the Sandbox's bash tool, and only it, as the tool that runs commands", func() {
		obj := json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"],"additionalProperties":false}`)
		app := apps.App{ID: "sandbox", Generation: 1, Enabled: true, State: "running", Tools: []string{"bash", "read"},
			ToolDefinitions: []contract.ToolDefinition{
				{Name: "bash", Kind: contract.ToolKindAction, Description: "Run a shell script.", InputSchema: obj},
				{Name: "read", Kind: contract.ToolKindAction, Description: "Read a file.", InputSchema: obj},
			}}
		s := &studioAddons{adapter: addontools.NewChatToolAdapter(changeOwner{app}), mode: auth.AddonModeNamedUser}
		got, err := s.Tools(context.Background(), "alice")
		Expect(err).ToNot(HaveOccurred())
		runs := map[string]bool{}
		for _, d := range got {
			runs[d.Name] = d.Runs
		}
		Expect(runs).To(Equal(map[string]bool{"addon__sandbox__bash": true, "addon__sandbox__read": false}))
	})

	It("shows an applied change to the model as a document that says it cannot be undone", func() {
		res := addontools.ToolResult{Kind: addontools.ResultApplied, Tool: "t", Applied: &addontools.AppliedRef{ID: "o1", AddonID: "tables", Action: "write", Count: 2}, Note: addontools.AppliedNote}
		got := studioAddonText(res)
		for _, want := range []string{`"kind":"applied"`, `"undoable":false`, "cannot be undone", "tables"} {
			Expect(got).To(ContainSubstring(want), "applied text")
		}
	})

	It("makes an add-on that needs the workspace unavailable while files are off", func() {
		s := &studioAddons{mode: auth.AddonModeNamedUser, hostOps: map[string][]string{"sandbox": {"workspace."}, "office": {"workspace."}, "fine-tuning": {"finetune."}}}
		ctx := context.Background()
		for _, id := range []string{"sandbox", "office"} {
			Expect(s.Unavailable(ctx, "alice", id, false)).To(ContainSubstring("workspace"), "%s with files off", id)
		}
		Expect(s.Unavailable(ctx, "alice", "fine-tuning", false)).To(BeEmpty(), "an add-on that does not use the workspace was gated")
		Expect(s.Unavailable(ctx, "alice", "office", true)).To(BeEmpty(), "office with files on")
	})

	It("gives a user without the add-ons feature no add-on tools", func() {
		s := &studioAddons{adapter: addontools.NewChatToolAdapter(forbiddenOwner{}), mode: auth.AddonModeNamedUser}
		got, err := s.Tools(context.Background(), "alice")
		Expect(err).ToNot(HaveOccurred(), "a refused user is not an error for the chats")
		Expect(got).To(BeEmpty(), "tools")
	})
})
