package studio

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type switchAddons struct{}

func (switchAddons) Tools(context.Context, string) ([]AddonTool, error) {
	return []AddonTool{
		{Name: "addon__tables__rows-query", AddonID: "tables", Tool: "rows-query", ResourceScoped: true},
		{Name: "addon__tables__rows-insert", AddonID: "tables", Tool: "rows-insert", Action: true, ResourceScoped: true},
		{Name: "addon__office__write", AddonID: "office", Tool: "write", Action: true},
	}, nil
}
func (switchAddons) Call(context.Context, string, string, string, json.RawMessage) (string, error) {
	return "", nil
}

// The Tools-write capability is the one a sender needs to change things with
// an add-on tool. When the only add-ons of the agent that change things work
// on resources, and the list of the agent leaves nothing to change, the
// capability is off and the access view says which link refused.
func TestResourcesSwitch(t *testing.T) {
	ctx := context.Background()
	store, err := agents.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{d: Deps{Agents: store, AddonTools: switchAddons{}}}
	put := func(id string, res resourcesets.Sets, addons ...string) {
		t.Helper()
		if err := store.Put(ctx, agents.Agent{ID: id, Principal: principal("alice"), Name: id, Addons: addons, Resources: res, Created: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	put("none", nil, "tables")
	put("read", resourcesets.Sets{"tables": {{ID: "db-a", Mode: "read"}}}, "tables")
	put("write", resourcesets.Sets{"tables": {{ID: "db-a", Mode: "read"}, {ID: "db-b", Mode: "write"}}}, "tables")
	put("mixed", nil, "tables", "office")
	put("other", nil, "office")
	put("empty", nil)
	sw := resourcesSwitch(s)
	cases := []struct {
		agent, capability, code string
	}{
		{"none", contract.CapToolsWrite, CodeAgentResourcesNone},
		{"read", contract.CapToolsWrite, CodeAgentResourcesReadOnly},
		{"write", contract.CapToolsWrite, ""},
		{"mixed", contract.CapToolsWrite, ""},
		{"other", contract.CapToolsWrite, ""},
		{"empty", contract.CapToolsWrite, ""},
		{"none", contract.CapMemoryWrite, ""},
		{"missing", contract.CapToolsWrite, ""},
		{"", contract.CapToolsWrite, ""},
	}
	for _, c := range cases {
		b := connectors.Binding{Owner: "alice", AgentID: c.agent}
		got := sw(ctx, b, c.capability)
		if got.Code != c.code {
			t.Errorf("%s %s: code %q, want %q", c.agent, c.capability, got.Code, c.code)
		}
		if c.code != "" {
			want := &Fix{Kind: FixOpenAgent, Target: c.agent, Field: "resources"}
			if got.Fix == nil || *got.Fix != *want {
				t.Errorf("%s: fix %+v, want %+v", c.agent, got.Fix, want)
			}
			if blockMessage(c.code, c.capability) == "" || BlockSay(c.code, c.capability) == "" {
				t.Errorf("%s: no sentence", c.code)
			}
			if chainRank(c.code) != chainRank(CodeAgentPermissionOff) {
				t.Errorf("%s stands at the agent link of the chain", c.code)
			}
		}
	}
}
