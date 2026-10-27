package application

import (
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio"
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
