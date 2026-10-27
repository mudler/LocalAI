package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func TestAChatActsAsItsUserWithTheChatCredential(t *testing.T) {
	s := &studioAddons{mode: auth.AddonModeNamedUser}
	a, err := s.actor("alice")
	if err != nil || a.UserID != "alice" || a.CredentialID != auth.AddonChatsCredential || a.Mode != auth.AddonModeNamedUser {
		t.Fatalf("actor = %+v, %v", a, err)
	}
	for _, bad := range []string{"", studio.AnonymousPrincipal, auth.AddonLocalOwnerID, "legacy-api-key"} {
		if _, err := s.actor(bad); err == nil {
			t.Errorf("actor(%q) was accepted in named-user mode", bad)
		}
	}
	owner := &studioAddons{mode: auth.AddonModeLocalOwner}
	if a, err := owner.actor(studio.AnonymousPrincipal); err != nil || a.UserID != auth.AddonLocalOwnerID {
		t.Fatalf("local-owner actor = %+v, %v", a, err)
	}
	if _, err := (&studioAddons{}).actor("alice"); err == nil {
		t.Error("an unknown mode gave an actor")
	}
	if _, err := s.actor(""); err == nil || !strings.Contains(err.Error(), gateway.ErrForbidden.Error()) {
		t.Errorf("a refused actor is not a denial: %v", err)
	}
}

func TestAProposalReachesTheModelAsATextThatSaysNothingChanged(t *testing.T) {
	res := addontools.ToolResult{Kind: addontools.ResultProposal, Tool: "t", Proposal: &addontools.ProposalRef{ID: "p1", AddonID: "tables", Action: "rows-delete"}}
	got := studioAddonText(res)
	for _, want := range []string{"tables", "rows-delete", "p1", "Nothing has been changed", "Add-ons page"} {
		if !strings.Contains(got, want) {
			t.Errorf("proposal text lacks %q: %s", want, got)
		}
	}
	data := addontools.ToolResult{Kind: addontools.ResultData, Tool: "t", Result: []byte(`{"n":1}`)}
	if got := studioAddonText(data); !strings.Contains(got, `"n":1`) {
		t.Errorf("a read result lost its content: %s", got)
	}
}

func TestAChatCredentialMayConfirmButASessionOrKeyRulesStayTheSame(t *testing.T) {
	named := confirmCredential(auth.AddonModeNamedUser)
	chat := contract.Actor{UserID: "alice", CredentialID: auth.AddonChatsCredential, Mode: auth.AddonModeNamedUser}
	if !named(chat) {
		t.Error("the chat credential cannot confirm what the chat stored")
	}
	if !named(contract.Actor{UserID: "alice", CredentialID: "session:s1", Mode: auth.AddonModeNamedUser}) {
		t.Error("a browser session lost the right to confirm")
	}
	for _, id := range []string{"key:k1", "session:", "", "mcp:1", "Chats"} {
		if named(contract.Actor{UserID: "alice", CredentialID: id, Mode: auth.AddonModeNamedUser}) {
			t.Errorf("credential %q may confirm", id)
		}
	}
	owner := confirmCredential(auth.AddonModeLocalOwner)
	if !owner(contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}) {
		t.Error("the local owner cannot confirm")
	}
	if owner(chat) {
		t.Error("the chat credential confirms in local-owner mode, where chats act as the local owner")
	}
}

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

func TestAChangeToolIsAnActionForTheChatsSoAnUnattendedRunWithoutAnAgentDoesNotGetIt(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"resourceId":{"type":"string"},"table":{"type":"string"},"rows":{"type":"array","items":{"type":"object"}}},"required":["resourceId","table","rows"],"additionalProperties":false}`)
	read := json.RawMessage(`{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"],"additionalProperties":false}`)
	app := apps.App{ID: "tables", Generation: 1, Enabled: true, State: "running", Tools: []string{"rows-insert", "rows-get"},
		ToolDefinitions: []contract.ToolDefinition{
			{Name: "rows-insert", Kind: contract.ToolKindWrite, Description: "Insert rows.", InputSchema: schema},
			{Name: "rows-get", Kind: contract.ToolKindRead, Description: "Read rows.", InputSchema: read},
		}}
	s := &studioAddons{adapter: addontools.NewChatToolAdapter(changeOwner{app}), mode: auth.AddonModeNamedUser}
	got, err := s.Tools(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	action := map[string]bool{}
	for _, d := range got {
		action[d.Name] = d.Action
	}
	if !action["addon__tables__rows-insert"] || action["addon__tables__rows-get"] {
		t.Errorf("Action flags = %v, want the write tool marked and the read tool not", action)
	}
}

func TestAnAppliedChangeReachesTheModelAsADocumentThatSaysItCannotBeUndone(t *testing.T) {
	res := addontools.ToolResult{Kind: addontools.ResultApplied, Tool: "t", Applied: &addontools.AppliedRef{ID: "o1", AddonID: "tables", Action: "write", Count: 2}, Note: addontools.AppliedNote}
	got := studioAddonText(res)
	for _, want := range []string{`"kind":"applied"`, `"undoable":false`, "cannot be undone", "tables"} {
		if !strings.Contains(got, want) {
			t.Errorf("applied text lacks %q: %s", want, got)
		}
	}
}

func TestAnAddonThatNeedsTheWorkspaceIsUnavailableWhileFilesAreOff(t *testing.T) {
	s := &studioAddons{mode: auth.AddonModeNamedUser, hostOps: map[string][]string{"sandbox": {"workspace."}, "office": {"workspace."}, "fine-tuning": {"finetune."}}}
	ctx := context.Background()
	for _, id := range []string{"sandbox", "office"} {
		if got := s.Unavailable(ctx, "alice", id, false); !strings.Contains(got, "workspace") {
			t.Errorf("%s with files off: reason = %q", id, got)
		}
	}
	if got := s.Unavailable(ctx, "alice", "fine-tuning", false); got != "" {
		t.Errorf("an add-on that does not use the workspace was gated: %q", got)
	}
	if got := s.Unavailable(ctx, "alice", "office", true); got != "" {
		t.Errorf("office with files on: reason = %q", got)
	}
}
