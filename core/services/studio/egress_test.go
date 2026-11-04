package studio_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/egress/audit"
	"github.com/mudler/LocalAI/core/services/studio/executionrequest"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	model "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the Sandbox network state of a chat", func() {
	ctx := context.Background()

	It("starts a chat with the instance default, capped by the ceiling, and a fork copies the preset and the allow rules", func() {
		st, _ := store.NewJSON("")
		settings := egress.Settings{Ceiling: egress.PresetRegistries, Default: egress.PresetAll}
		svc := newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) {
			d.EgressSettings = func(context.Context) (egress.Settings, error) { return settings, nil }
		})
		id := create(svc, alice)
		rec, err := svc.Get(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Egress.Preset).To(Equal(egress.PresetRegistries))

		Expect(st.UpdateEgress(ctx, alice, id, func(s *egress.ChatState) error {
			s.PutRule(egress.Rule{Host: "example.com", Port: 443, Allow: true})
			s.PutRule(egress.Rule{Host: "evil.example", Port: 443})
			s.PutCard(egress.Card{ID: "k", Host: "x.example", Port: 443, State: egress.CardDenied})
			return nil
		})).To(Succeed())
		fork, err := svc.Fork(ctx, alice, id, 0, "")
		Expect(err).NotTo(HaveOccurred())
		got, _ := svc.Get(ctx, alice, fork.ID)
		Expect(got.Egress.Preset).To(Equal(egress.PresetRegistries))
		Expect(got.Egress.Rules).To(ConsistOf(HaveField("Host", "example.com")))
		Expect(got.Egress.Cards).To(BeEmpty())
	})

	It("starts with off when nothing is set", func() {
		st, _ := store.NewJSON("")
		svc := newServiceOn(st, studiotest.NewGateHarness())
		rec, _ := svc.Get(ctx, alice, create(svc, alice))
		Expect(rec.Egress.Preset).To(Equal(egress.PresetOff))
	})

	It("prunes the audit lines older than the retention", func() {
		st, _ := store.NewJSON("")
		a, _ := audit.NewJSON("")
		now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
		svc := newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) {
			d.EgressAudit = a
			d.EgressAuditDays = func() int { return 2 }
			d.Clock = func() time.Time { return now }
		})
		Expect(a.Add(ctx, audit.Event{Host: "old", Verdict: "allow", At: now.Add(-72 * time.Hour)}, audit.Event{Host: "new", Verdict: "allow", At: now.Add(-time.Hour)})).To(Succeed())
		n, err := svc.PruneEgressAudit(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(1)))
	})

	It("prunes the audit of the commands on this machine with the same retention", func() {
		st, _ := store.NewJSON("")
		now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
		fx := &prunedHostExec{}
		svc := newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) {
			d.HostExec = fx
			d.EgressAuditDays = func() int { return 2 }
			d.Clock = func() time.Time { return now }
		})
		n, err := svc.PruneHostExecAudit(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(3)))
		Expect(fx.before).To(Equal(now.Add(-48 * time.Hour)))
	})
})

// prunedHostExec is a built-in bash whose audit records the prune cut-off.
type prunedHostExec struct {
	fakeHostExec
	before time.Time
}

func (p *prunedHostExec) Prune(_ context.Context, before time.Time) (int64, error) {
	p.before = before
	return 3, nil
}

var _ = Describe("egress.check and egress.audit on the chats service", func() {
	ctx := context.Background()
	var (
		svc      *studio.Service
		st       store.Store
		a        audit.Store
		settings egress.Settings
	)
	BeforeEach(func() {
		st, _ = store.NewJSON("")
		a, _ = audit.NewJSON("")
		settings = egress.Settings{Ceiling: egress.PresetRegistries, Default: egress.PresetRegistries}
		svc = newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) {
			d.EgressSettings = func(context.Context) (egress.Settings, error) { return settings, nil }
			d.EgressAudit = a
		})
	})
	req := func(host string) egress.Request {
		return egress.Request{Host: host, Port: 443, Kind: egress.KindConnect, TLS: true}
	}

	It("finds the chat by its action resource and decides by its preset under the ceiling, auditing each verdict", func() {
		id := create(svc, alice)
		rid := toolargs.ActionResourceID(id)
		v, err := svc.EgressCheck(ctx, alice, rid, "op1", req("PyPI.org."))
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(Equal(egress.Verdict{Action: egress.Allow, Reason: egress.ReasonPreset}))
		v, _ = svc.EgressCheck(ctx, alice, rid, "op1", req("example.com"))
		Expect(v.Action).To(Equal(egress.Block))
		lines, _ := a.Events(ctx, audit.Query{})
		Expect(lines).To(HaveLen(2))
		Expect(lines[1]).To(SatisfyAll(HaveField("User", alice), HaveField("Chat", id), HaveField("Host", "pypi.org"), HaveField("Verdict", "allow")))
		Expect(lines[0]).To(SatisfyAll(HaveField("Host", "example.com"), HaveField("Verdict", "block")))

		Expect(svc.EgressAudit(ctx, alice, rid, []studio.EgressEvent{{Host: "pypi.org", Port: 443, Kind: "connect", Verdict: "done", Reason: "preset", BytesDown: 99}})).To(Succeed())
		lines, _ = a.Events(ctx, audit.Query{Verdict: "done"})
		Expect(lines).To(ConsistOf(SatisfyAll(HaveField("Chat", id), HaveField("BytesDown", int64(99)))))
	})

	It("never finds another user's chat", func() {
		id := create(svc, alice)
		v, err := svc.EgressCheck(ctx, bob, toolargs.ActionResourceID(id), "op1", req("pypi.org"))
		Expect(err).NotTo(HaveOccurred())
		Expect(v).To(Equal(egress.Verdict{Action: egress.Block, Reason: "chat"}))
	})

	It("allows a host of the always-allowed list, audits it as allowed_host, and a change applies at the next check", func() {
		id := create(svc, alice)
		rid := toolargs.ActionResourceID(id)
		v, _ := svc.EgressCheck(ctx, alice, rid, "op1", req("Api.Example.com"))
		Expect(v.Action).To(Equal(egress.Block))
		settings.AllowedHosts = []egress.AllowedHost{{Host: "api.example.com"}}
		v, _ = svc.EgressCheck(ctx, alice, rid, "op1", req("Api.Example.com"))
		Expect(v).To(Equal(egress.Verdict{Action: egress.Allow, Reason: egress.ReasonAllowedHost}))
		lines, _ := a.Events(ctx, audit.Query{})
		Expect(lines[0]).To(SatisfyAll(HaveField("Host", "api.example.com"), HaveField("Verdict", "allow"), HaveField("Reason", "allowed_host")))
		view, err := svc.Egress(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(view.JSON().AllowedHosts).To(Equal([]string{"api.example.com"}))
		settings.AllowedHosts = nil
		v, _ = svc.EgressCheck(ctx, alice, rid, "op1", req("api.example.com"))
		Expect(v.Action).To(Equal(egress.Block))
		view, _ = svc.Egress(ctx, alice, id)
		Expect(view.JSON().AllowedHosts).To(BeEmpty())
		Expect(view.JSON().AllowedHosts).NotTo(BeNil(), "the wire form is a list, never null")
	})

	It("lowering the ceiling applies at the next check", func() {
		id := create(svc, alice)
		settings.Ceiling = egress.PresetOff
		v, _ := svc.EgressCheck(ctx, alice, toolargs.ActionResourceID(id), "op1", req("pypi.org"))
		Expect(v).To(Equal(egress.Verdict{Action: egress.Block, Reason: egress.ReasonCeiling}))
	})

	It("tells the model that the server's setting, not an approval, keeps the network off", func() {
		off := egress.DescribeLimit(egress.PresetOff, egress.PresetOff, egress.BuiltinRegistries(), false, "Sandbox")
		Expect(off).To(SatisfyAll(ContainSubstring("server's setting"), ContainSubstring("do not wait"), ContainSubstring("Settings tab of the add-on that runs the commands")))
		Expect(off).NotTo(ContainSubstring("asks the user"))
		capped := egress.DescribeLimit(egress.PresetRegistries, egress.PresetRegistries, egress.BuiltinRegistries(), false, "Sandbox")
		Expect(capped).To(SatisfyAll(ContainSubstring("package registries only"), ContainSubstring("not by a missing approval")))
	})

	It("tells the model the network of its commands", func() {
		Expect(egress.Describe(egress.PresetRegistries, egress.BuiltinRegistries(), true, "Sandbox")).To(ContainSubstring("package registries only"))
	})
})

var _ = Describe("the network line of a turn", func() {
	sandboxed := func(settings egress.Settings) (*studio.Service, *studiotest.ToolHarness) {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}
		f := newFiles()
		return newToolService(func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }, func(d *studio.Deps) {
			d.Files = f
			d.AddonTools = isolatedAddons{fa, "isolated"}
			d.EgressSettings = func(context.Context) (egress.Settings, error) { return settings, nil }
		})
	}
	turn := func(svc *studio.Service, h *studiotest.ToolHarness) string {
		id := create(svc, alice)
		sent(svc, alice, id, "hello")
		idle(svc, alice, id)
		return h.TurnPrompt(id) + "\n" + strings.Join(h.Sent(id), "\n")
	}

	It("says a blocked host asks the user when a card can appear", func() {
		svc, h := sandboxed(egress.Settings{Ceiling: egress.PresetAll})
		Expect(turn(svc, h)).To(ContainSubstring("Network for commands: off. A blocked host asks the user"))
	})

	It("does not promise a card when the ceiling is off", func() {
		svc, h := sandboxed(egress.Settings{Ceiling: egress.PresetOff})
		got := turn(svc, h)
		Expect(got).To(ContainSubstring("Network for commands: off."))
		Expect(got).To(ContainSubstring("no user approval changes that"))
		Expect(got).NotTo(ContainSubstring("A blocked host asks the user"))
	})
})

// controlledAddons is a broker that also says which host controls each
// add-on is entitled to, as the apps list does.
type controlledAddons struct {
	isolatedAddons
	controls map[string][]string
}

func (c controlledAddons) Controls(_ context.Context, _, addonID string) []string {
	return c.controls[addonID]
}

// reportingAddons is a broker whose runner reports its typed execution status.
type reportingAddons struct {
	controlledAddons
	status model.Value
	// enforced is what the runner says about the network policy.
	enforced bool
}

func (r reportingAddons) NewExecutionObservation(ctx context.Context, user, _ string) (*executionrequest.Request, string, func() studio.ExecutionDetail) {
	b := model.Binding{Actor: user, Addon: "sandbox", Generation: "1", Incarnation: "one", ExecutionTool: "bash", StatusTool: "status"}
	req := executionrequest.New(ctx, b, "q", model.Eligible, executionrequest.IO{
		Validate: func(context.Context, model.Binding) error { return nil },
		Read: func(context.Context, model.Binding, model.ObservationID, time.Time) (model.Payload, error) {
			return model.ValidPayload(r.status), nil
		},
	}, nil)
	return req, b.Addon, func() studio.ExecutionDetail { return studio.ExecutionDetail{NetworkPolicyEnforced: r.enforced} }
}

func (r reportingAddons) Isolation(ctx context.Context, user, addonID string) string {
	v, _ := studio.ExecutionObservation(ctx, user, addonID)
	if v.Readiness != model.Available {
		return ""
	}
	return string(v.Isolation)
}

var _ = Describe("whether the network settings apply to the commands of a chat", func() {
	egressOf := func(status model.Value, enforced bool, controls ...string) studio.EgressView {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runAddon()}}
		f := newFiles()
		svc, _ := newToolService(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, func(d *studio.Deps) {
			d.Files = f
			d.AddonTools = reportingAddons{controlledAddons{isolatedAddons{fa, "isolated"}, map[string][]string{"sandbox": controls}}, status, enforced}
		})
		v, err := svc.Egress(context.Background(), alice, create(svc, alice))
		Expect(err).NotTo(HaveOccurred())
		return v
	}
	isolated := model.Value{Readiness: model.Available, Isolation: model.Isolated}

	It("applies when the isolated runner reports that it enforces the policy", func() {
		v := egressOf(isolated, true, "network-policy")
		Expect(v.Applies).To(BeTrue())
		Expect(v.AppliesReason).To(BeEmpty())
		Expect(v.JSON().Addon).To(Equal("sandbox"))
	})
	It("does not apply when the runner does not report enforcement", func() {
		v := egressOf(isolated, false, "network-policy")
		Expect(v.Applies).To(BeFalse())
		Expect(v.AppliesReason).To(Equal(studio.EgressAppliesNone))
	})
	It("does not trust an enforcement report from a runner that is not isolated", func() {
		v := egressOf(model.Value{Readiness: model.Available, Isolation: model.Host}, true, "network-policy")
		Expect(v.Applies).To(BeFalse())
	})
	It("does not trust an enforcement report from a runner that is not available", func() {
		v := egressOf(model.Value{Readiness: model.Unavailable}, true, "network-policy")
		Expect(v.Applies).To(BeFalse())
	})
	It("applies but draws no row without the network-policy control", func() {
		v := egressOf(isolated, true, "tool-providers")
		Expect(v.Applies).To(BeTrue())
		Expect(v.JSON().Addon).To(BeEmpty())
	})
})

var _ = Describe("the add-on that draws the network row of a chat", func() {
	runner := func(addonID string) []studio.AddonTool {
		bash := addonTool(addonID, "bash")
		bash.Action, bash.Shadows = true, "bash"
		return []studio.AddonTool{bash}
	}
	service := func(addonID, mode string, controls map[string][]string) *studio.Service {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: runner(addonID)}}
		f := newFiles()
		svc, _ := newToolService(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, func(d *studio.Deps) {
			d.Files = f
			d.AddonTools = controlledAddons{isolatedAddons{fa, mode}, controls}
		})
		return svc
	}

	It("names the runner control without claiming host network enforcement", func() {
		svc := service("sandbox", "isolated", map[string][]string{"sandbox": {"network-policy", "tool-providers"}})
		v, err := svc.Egress(context.Background(), alice, create(svc, alice))
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Applies).To(BeFalse())
		Expect(v.JSON().Addon).To(Equal("sandbox"))
	})

	It("is any add-on with the control, not one name", func() {
		svc := service("devbox", "isolated", map[string][]string{"devbox": {"network-policy"}})
		v, _ := svc.Egress(context.Background(), alice, create(svc, alice))
		Expect(v.JSON().Addon).To(Equal("devbox"))
	})

	It("is none when the runner has no network policy and enforcement is unknown", func() {
		svc := service("sandbox", "isolated", map[string][]string{"sandbox": {"tool-providers"}})
		v, _ := svc.Egress(context.Background(), alice, create(svc, alice))
		Expect(v.Applies).To(BeFalse())
		Expect(v.JSON().Addon).To(BeEmpty())
	})

	It("is none when commands run on this machine", func() {
		svc := service("sandbox", "host", map[string][]string{"sandbox": {"network-policy"}})
		v, _ := svc.Egress(context.Background(), alice, create(svc, alice))
		Expect(v.Applies).To(BeFalse())
		Expect(v.AppliesReason).To(Equal(studio.EgressAppliesHost))
		Expect(v.JSON().Addon).To(BeEmpty())
	})
})
