package studio

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// settingsMem stands for the store of the per-person add-on settings.
type settingsMem struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *settingsMem) get(_ context.Context, user, addon, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[user+"/"+addon+"/"+key]
	return v, ok, nil
}

func (s *settingsMem) put(user, addon, key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[user+"/"+addon+"/"+key] = value
}

var _ = Describe("who may run an add-on tool without a confirmation", Label("approval"), func() {
	const addon = "pikvm"
	var (
		ctx      = context.Background()
		svc      *Service
		settings *settingsMem
		now      = time.Now()
	)
	BeforeEach(func() {
		svc, _ = newSkillService(&skillBroker{})
		settings = &settingsMem{}
		svc.d.UserSetting = settings.get
	})
	chatOf := func(id, owner, origin, kind string) {
		Expect(svc.d.Store.CreateChat(ctx, session.ChatRecord{ID: id, Principal: owner, Title: id, Created: now, Updated: now, Origin: origin, Kind: kind})).To(Succeed())
	}
	// grant stands for the grant a connector turn holds: who sent, and whether
	// every sender is paired with the bot.
	grant := func(chat string, paired bool) {
		svc.access.set(chat, turnGrant{Binding: "b1", Kind: "telegram", Senders: []string{"s1"}, Paired: paired})
	}

	// The senders. Each one is a chat as the host stores it, and the facts the
	// host holds for its turn. The allowlisted-unpaired and the anyone-mode
	// sender are both messaging chats whose senders are not paired: the host
	// tells them apart for other rules, never for this one.
	type sender struct {
		name   string
		setup  func(chat string)
		relaxs bool
	}
	senders := []sender{
		{"the owner in their own chat", func(c string) { chatOf(c, "alice", "", "") }, true},
		{"a paired sender in a messaging chat", func(c string) { chatOf(c, "alice", session.OriginConnector, ""); grant(c, true) }, true},
		{"an allowlisted sender who is not paired", func(c string) { chatOf(c, "alice", session.OriginConnector, ""); grant(c, false) }, false},
		{"a sender let in because the bot accepts anyone", func(c string) { chatOf(c, "alice", session.OriginConnector, ""); grant(c, false) }, false},
		{"a messaging turn whose grant is gone", func(c string) { chatOf(c, "alice", session.OriginConnector, "") }, false},
		{"a trigger that runs unattended", func(c string) { chatOf(c, "alice", session.OriginTrigger, "") }, false},
		{"an agent job", func(c string) { chatOf(c, "alice", session.OriginJob, "") }, false},
		{"a page of the workspace", func(c string) { chatOf(c, "alice", session.OriginPage, "") }, false},
		{"a sub-agent of the owner's chat", func(c string) { chatOf(c, "alice", "", session.KindSubAgent) }, false},
		{"another person's chat", func(c string) { chatOf(c, "bob", "", "") }, false},
	}
	for _, snd := range senders {
		for _, tool := range []string{"power-action", "send-key"} {
			for _, setting := range []string{contract.ApprovalAsk, contract.ApprovalSkip, ""} {
				It(snd.name+" with "+tool+" set to '"+setting+"'", func() {
					snd.setup("c1")
					if setting != "" {
						settings.put("alice", addon, contract.ApprovalSettingKey(tool), setting)
					}
					want := snd.relaxs && setting == contract.ApprovalSkip
					Expect(svc.ApprovalRelaxed(ctx, "alice", "c1", addon, tool)).To(Equal(want))
				})
			}
		}
	}

	It("reads the setting of the tool asked, not of another tool", func() {
		chatOf("c1", "alice", "", "")
		settings.put("alice", addon, contract.ApprovalSettingKey("power-action"), contract.ApprovalSkip)
		Expect(svc.ApprovalRelaxed(ctx, "alice", "c1", addon, "power-action")).To(BeTrue())
		Expect(svc.ApprovalRelaxed(ctx, "alice", "c1", addon, "send-key")).To(BeFalse())
		Expect(svc.ApprovalRelaxed(ctx, "alice", "c1", "other", "power-action")).To(BeFalse())
	})
	It("reads the setting of the owner of the chat only", func() {
		chatOf("c1", "alice", "", "")
		settings.put("bob", addon, contract.ApprovalSettingKey("power-action"), contract.ApprovalSkip)
		Expect(svc.ApprovalRelaxed(ctx, "alice", "c1", addon, "power-action")).To(BeFalse())
	})
	It("asks when the chat is unknown or unnamed", func() {
		settings.put("alice", addon, contract.ApprovalSettingKey("power-action"), contract.ApprovalSkip)
		Expect(svc.ApprovalRelaxed(ctx, "alice", "missing", addon, "power-action")).To(BeFalse())
		Expect(svc.ApprovalRelaxed(ctx, "alice", "", addon, "power-action")).To(BeFalse())
	})
	It("asks when the setting store cannot answer", func() {
		chatOf("c1", "alice", "", "")
		svc.d.UserSetting = nil
		Expect(svc.ApprovalRelaxed(ctx, "alice", "c1", addon, "power-action")).To(BeFalse())
	})
	It("lets a pairing that ended stop the relaxation", func() {
		chatOf("c1", "alice", session.OriginConnector, "")
		settings.put("alice", addon, contract.ApprovalSettingKey("power-action"), contract.ApprovalSkip)
		grant("c1", true)
		Expect(svc.ApprovalRelaxed(ctx, "alice", "c1", addon, "power-action")).To(BeTrue())
		svc.access.clear("c1")
		Expect(svc.ApprovalRelaxed(ctx, "alice", "c1", addon, "power-action")).To(BeFalse())
	})
	It("records a run without a confirmation in the decisions of the connection, with no arguments", func() {
		grant("c1", true)
		svc.RecordRelaxedRun("b9", "c1", "power-action", "applied")
		got := svc.access.recent("b9", 5)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Allowed).To(BeTrue())
		Expect(got[0].Capability).To(Equal(contract.CapToolsWrite))
		Expect(got[0].Reason).To(Equal("power-action: applied"))
		Expect(got[0].Senders).To(Equal([]string{"s1"}))
	})
})

// toolBroker lists the tools of a few add-ons, as the chat service sees them.
type toolBroker struct {
	skillBroker
	tools []AddonTool
}

func (b *toolBroker) Tools(context.Context, string) ([]AddonTool, error) { return b.tools, nil }

var _ = Describe("the tools that run without asking, in the view of a connection", Label("approval"), func() {
	ctx := context.Background()
	var (
		svc      *Service
		settings *settingsMem
	)
	BeforeEach(func() {
		b := &toolBroker{tools: []AddonTool{
			{Name: "addon__pikvm__power-action", AddonID: "pikvm", Tool: "power-action", Action: true, Credentialed: true, ApprovalOverridable: true},
			{Name: "addon__pikvm__send-key", AddonID: "pikvm", Tool: "send-key", Action: true, Credentialed: true, ApprovalOverridable: true},
			{Name: "addon__pikvm__type-text", AddonID: "pikvm", Tool: "type-text", Action: true, Credentialed: true},
			{Name: "addon__pikvm__get-info", AddonID: "pikvm", Tool: "get-info", Credentialed: true},
			{Name: "addon__other__act", AddonID: "other", Tool: "act", Action: true, Credentialed: true, ApprovalOverridable: true},
		}}
		svc, _ = newSkillService(&b.skillBroker)
		svc.d.AddonTools = b
		settings = &settingsMem{}
		svc.d.UserSetting = settings.get
	})
	binding := func(mode string) connectors.Binding {
		return connectors.Binding{ID: "b1", Owner: "alice", AddonID: "pikvm", Kind: "pikvm", WriteMode: mode}
	}

	It("lists only the tools the owner can relax, and none by default", func() {
		got := svc.approvalsOf(ctx, binding(connectors.WritePropose))
		Expect(got).To(Equal([]ToolApproval{{Tool: "power-action"}, {Tool: "send-key"}}))
	})
	It("says the owner and paired senders run without asking once the owner chose skip", func() {
		settings.put("alice", "pikvm", contract.ApprovalSettingKey("power-action"), contract.ApprovalSkip)
		got := svc.approvalsOf(ctx, binding(connectors.WritePropose))
		Expect(got).To(Equal([]ToolApproval{
			{Tool: "power-action", Skip: true, RunsWithoutAsking: []string{"owner", "paired"}},
			{Tool: "send-key"},
		}))
	})
	It("says nothing runs while the write mode is off", func() {
		settings.put("alice", "pikvm", contract.ApprovalSettingKey("power-action"), contract.ApprovalSkip)
		got := svc.approvalsOf(ctx, binding(connectors.WriteOff))
		Expect(got[0]).To(Equal(ToolApproval{Tool: "power-action", Skip: true}))
	})
	It("shows the tools as asking when the setting cannot be read", func() {
		svc.d.UserSetting = nil
		Expect(svc.approvalsOf(ctx, binding(connectors.WritePropose))).To(Equal([]ToolApproval{{Tool: "power-action"}, {Tool: "send-key"}}))
	})
	It("is part of the access view of the binding", func() {
		settings.put("alice", "pikvm", contract.ApprovalSettingKey("send-key"), contract.ApprovalSkip)
		c := &Connectors{svc: svc, o: ConnectorOptions{Clock: time.Now, Transport: kindsStub{}}}
		v := c.accessOf(ctx, binding(connectors.WritePropose), "")
		Expect(v.Approvals).To(ContainElement(ToolApproval{Tool: "send-key", Skip: true, RunsWithoutAsking: []string{"owner", "paired"}}))
		Expect(v.Approvals).To(ContainElement(ToolApproval{Tool: "power-action"}))
	})
})

// kindsStub runs no connector kind.
type kindsStub struct{}

func (kindsStub) Kinds(context.Context) ([]ConnectorKind, error) { return nil, nil }
func (kindsStub) Call(context.Context, ConnectorCallScope, contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return contract.ConnectorResponse{}, nil
}
