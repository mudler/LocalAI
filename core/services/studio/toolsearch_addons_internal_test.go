package studio

import (
	"context"
	"encoding/json"
	"time"

	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A deferred tool is served by the function of the typed tool, so a sender of a
// messaging chat who may not use the tool is held back at the offer and at the
// call, and the tool search cannot lend the tool to them.
var _ = Describe("a deferred write tool in a messaging chat", func() {
	ctx := context.Background()
	write := AddonTool{Name: "addon__notes__add-note", AddonID: "notes", Tool: "add-note", Description: "adds a note",
		Schema: json.RawMessage(`{"type":"object"}`), Action: true, Permission: contract.CapToolsWrite, Deferred: true}

	var (
		svc *Service
		rec session.ChatRecord
	)
	offered := func() []session.Tool {
		tools, _, _, err := svc.addonToolsHeld(ctx, workspace.Actor{Principal: "alice", ChatID: "c1"}, rec, &scope{addons: map[string]bool{"notes": true}, addonNames: []string{"notes"}})
		Expect(err).NotTo(HaveOccurred())
		return tools
	}
	BeforeEach(func() {
		clock := &fakeClock{t: time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)}
		broker := &scriptBroker{tool: write, took: time.Millisecond, clock: clock, script: func(context.Context) (addontools.ToolResult, error) {
			return addontools.ToolResult{Kind: addontools.ResultData, Message: "ok"}, nil
		}}
		svc, _ = newSkillService(&broker.skillBroker)
		svc.d.AddonTools = broker
		svc.d.Clock = clock.now
		rec = session.ChatRecord{ID: "c1", Principal: "alice", Origin: session.OriginConnector, OriginID: "b-slack", AgentID: "ag1"}
		rec.Created, rec.Updated = clock.now(), clock.now()
		Expect(svc.d.Store.CreateChat(ctx, rec)).To(Succeed())
	})

	It("is not offered, so it is not in the deferred set, to a sender who lacks tools.write", func() {
		svc.access.set("c1", turnGrant{Binding: "b-slack", Kind: "slack", Senders: []string{"U1"}, Sender: SenderListed})
		Expect(deferrableAddonTools(offered())).To(BeEmpty())
	})

	It("is deferrable for a paired sender, and refuses a call once the grant is gone", func() {
		svc.access.set("c1", turnGrant{Binding: "b-slack", Kind: "slack", Senders: []string{"U1"}, Caps: []string{contract.CapToolsWrite}, Sender: SenderPaired, Paired: true})
		got := deferrableAddonTools(offered())
		Expect(got).To(HaveLen(1))
		Expect(got[0].Name).To(Equal(write.Name))
		out, err := got[0].Run(ctx, json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("ok"))

		svc.access.clear("c1")
		_, err = got[0].Run(ctx, json.RawMessage(`{}`))
		Expect(err).To(HaveOccurred(), "the grant of the turn is checked again at the call, deferred or not")
	})
})
