package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	addontools "github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// syncBuf is a log sink that tests read while the service writes.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, l := range strings.Split(s.b.String(), "\n") {
		if strings.Contains(l, "studio: addon tool") {
			out = append(out, l)
		}
	}
	return out
}

// scriptBroker is an add-on broker whose calls end as a script says. It fills
// the outcome of the context the way the adapter of the add-ons does, from the
// same classification, so the studio side is tried on every ending.
type scriptBroker struct {
	skillBroker
	tool   AddonTool
	script func(ctx context.Context) (addontools.ToolResult, error)
	took   time.Duration
	clock  *fakeClock
}

func (b *scriptBroker) Tools(context.Context, string) ([]AddonTool, error) {
	return []AddonTool{b.tool}, nil
}

func (b *scriptBroker) Call(ctx context.Context, _, _, _ string, _ json.RawMessage) (string, error) {
	b.clock.advance(b.took)
	res, err := b.script(ctx)
	o := addontools.OutcomeFrom(ctx)
	o.Call(b.tool.Kind(), "acme/widgets")
	o.Connection("cred-1")
	result, reason, status := addontools.Classify(res, err)
	o.Finish(result, reason, status)
	return "TOOL-RESULT-TEXT-do-not-log " + res.Message, err
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// Kind is the kind the stand-in tool reports, from its flags.
func (t AddonTool) Kind() string {
	if t.Action {
		return contract.ToolKindWrite
	}
	return contract.ToolKindRead
}

var _ = Describe("the outcome of an add-on tool call in a chat", Label("tooloutcome"), func() {
	const (
		secretArgs = "SECRET-ARGUMENT-TEXT-do-not-log"
		secretTok  = "ghp_SECRETTOKEN0123456789"
	)
	var (
		ctx    = context.Background()
		svc    *Service
		logs   *syncBuf
		clock  *fakeClock
		broker *scriptBroker
		rec    session.ChatRecord
	)
	write := AddonTool{Name: "addon__tracker__create-issue", AddonID: "tracker", Tool: "create-issue", Description: "opens an issue",
		Schema: json.RawMessage(`{"type":"object"}`), Action: true, Permission: contract.CapToolsWrite}
	read := AddonTool{Name: "addon__tracker__list-issues", AddonID: "tracker", Tool: "list-issues", Description: "lists", Schema: json.RawMessage(`{"type":"object"}`)}

	pairedGrant := func() {
		svc.access.set("c1", turnGrant{Binding: "b-slack", Kind: "slack", Senders: []string{"U1"}, Caps: []string{contract.CapToolsWrite}, Sender: SenderPaired, Paired: true})
	}
	// setup makes the chat and its service; offer then makes the chat's tool for
	// t, as the chat service offers it. build does both.
	setup := func(t AddonTool, origin string, script func(context.Context) (addontools.ToolResult, error)) {
		broker = &scriptBroker{tool: t, script: script, took: 120 * time.Millisecond, clock: clock}
		svc, _ = newSkillService(&broker.skillBroker)
		svc.d.AddonTools = broker
		svc.d.Clock = clock.now
		logs = &syncBuf{}
		svc.log = slog.New(slog.NewTextHandler(logs, nil))
		rec = session.ChatRecord{ID: "c1", Principal: "alice", Origin: origin, OriginID: "b-slack"}
		if origin != "" {
			rec.AgentID = "ag1" // an action tool of an add-on is for a chat of a saved agent
		}
		now := clock.now()
		rec.Created, rec.Updated = now, now
		Expect(svc.d.Store.CreateChat(ctx, rec)).To(Succeed())
	}
	offer := func(t AddonTool) session.Tool {
		tools, _, _, err := svc.addonToolsHeld(ctx, workspace.Actor{Principal: "alice", ChatID: "c1"}, rec, &scope{addons: map[string]bool{"tracker": true}, addonNames: []string{"tracker"}})
		Expect(err).NotTo(HaveOccurred())
		for _, tl := range tools {
			if tl.Name == t.Name {
				return tl
			}
		}
		Fail("the tool was not offered: " + t.Name)
		return session.Tool{}
	}
	build := func(t AddonTool, origin string, grant bool, script func(context.Context) (addontools.ToolResult, error)) session.Tool {
		setup(t, origin, script)
		if grant {
			pairedGrant()
		}
		return offer(t)
	}
	run := func(t session.Tool) string {
		out, _ := t.Run(ctx, json.RawMessage(`{"title":"`+secretArgs+`"}`))
		return out
	}
	proposal := func(hold string) func(context.Context) (addontools.ToolResult, error) {
		return func(context.Context) (addontools.ToolResult, error) {
			return addontools.ToolResult{Kind: addontools.ResultProposal, Proposal: &addontools.ProposalRef{ID: "p1", Hold: hold}}, nil
		}
	}
	errored := func(code string, upstream int) func(context.Context) (addontools.ToolResult, error) {
		return func(context.Context) (addontools.ToolResult, error) {
			return addontools.ToolResult{Kind: addontools.ResultError, Code: code, Upstream: upstream, Message: "MESSAGE-" + secretTok}, nil
		}
	}
	BeforeEach(func() {
		clock = &fakeClock{t: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)}
	})

	DescribeTable("writes one line with the tool, the sender and the reason, and no content",
		func(t AddonTool, origin string, grant bool, script func(context.Context) (addontools.ToolResult, error), want ...string) {
			tool := build(t, origin, grant, script)
			run(tool)
			lines := logs.lines()
			Expect(lines).To(HaveLen(1), "one line for one call")
			for _, w := range want {
				Expect(lines[0]).To(ContainSubstring(w))
			}
			Expect(lines[0]).To(ContainSubstring("ms=120"))
			for _, leak := range []string{secretArgs, secretTok, "TOOL-RESULT-TEXT", "MESSAGE-"} {
				Expect(logs.b.String()).NotTo(ContainSubstring(leak), "the log must not hold content")
			}
			evs := svc.access.recentTools("cred-1", 20)
			Expect(evs).To(HaveLen(1))
			raw, _ := json.Marshal(evs)
			for _, leak := range []string{secretArgs, secretTok, "TOOL-RESULT-TEXT", "MESSAGE-"} {
				Expect(string(raw)).NotTo(ContainSubstring(leak), "the latest decisions must not hold content")
			}
		},
		Entry("a read that works, for the owner in the web UI", read, "", false, func(context.Context) (addontools.ToolResult, error) {
			return addontools.ToolResult{Kind: addontools.ResultData}, nil
		}, "addon=tracker", "tool=list-issues", "kind=read", "binding=cred-1", "resource=acme/widgets", "chat=studio", "sender=owner", "outcome=ok", "reason=-", "status=-"),
		Entry("a write that is applied, for a paired sender", write, session.OriginConnector, true, func(context.Context) (addontools.ToolResult, error) {
			return addontools.ToolResult{Kind: addontools.ResultApplied}, nil
		}, "tool=create-issue", "kind=write", "chat=connector", "sender=paired", "outcome=ok"),
		Entry("a write held because the connection only proposes", write, session.OriginConnector, true, proposal("write_mode_propose"),
			"outcome=held", "reason=write_mode_propose", "sender=paired"),
		Entry("a write held because the tool always asks", write, session.OriginConnector, true, proposal("approval_always"),
			"outcome=held", "reason=approval_always"),
		Entry("write mode off", write, session.OriginConnector, true, errored(addontools.CodeWriteNotAllowed, 0),
			"outcome=denied", "reason=write_mode_off"),
		Entry("the resource is not in the list of the agent", write, session.OriginConnector, true, errored(addontools.CodeResourceNotInAgentList, 0),
			"outcome=denied", "reason=resource_not_in_agent_list"),
		Entry("the service refused the token", write, session.OriginConnector, true, errored(addontools.CodeUpstreamRefused, 401),
			"outcome=error", "reason=upstream_401", "status=401"),
		Entry("the service refused for a missing permission", write, session.OriginConnector, true, errored(addontools.CodeUpstreamRefused, 403),
			"outcome=error", "reason=upstream_403", "status=403"),
		Entry("the service could not find the target", read, "", false, errored(addontools.CodeUpstreamRefused, 404),
			"outcome=error", "reason=upstream_404", "status=404", "kind=read"),
		Entry("the service did not accept the values", write, session.OriginConnector, true, errored(addontools.CodeUpstreamRefused, 422),
			"outcome=error", "reason=upstream_422", "status=422"),
	)

	It("records a call that the permission gate stopped, with the code of the link that refused", func() {
		setup(write, session.OriginConnector, func(context.Context) (addontools.ToolResult, error) {
			Fail("the add-on must not be called")
			return addontools.ToolResult{}, nil
		})
		// The sender held the permission when the tool was offered and lost it
		// before the call: the grant of the turn shrank.
		pairedGrant()
		tool := offer(write)
		svc.access.set("c1", turnGrant{Binding: "b-slack", Kind: "slack", Senders: []string{"U9"}, Sender: SenderListed,
			Denied: map[string]string{contract.CapToolsWrite: "needs pairing"}, DeniedCodes: map[string]string{contract.CapToolsWrite: CodeSenderNeedsPairing}})
		out, err := tool.Run(ctx, json.RawMessage(`{"title":"`+secretArgs+`"}`))
		Expect(err).To(HaveOccurred())
		Expect(out).To(BeEmpty())
		lines := logs.lines()
		Expect(lines).To(HaveLen(1))
		for _, w := range []string{"tool=create-issue", "outcome=denied", "reason=sender_needs_pairing", "sender=listed", "binding=-", "kind=-"} {
			Expect(lines[0]).To(ContainSubstring(w))
		}
		Expect(logs.b.String()).NotTo(ContainSubstring(secretArgs))
		evs := svc.access.recentTools("b-slack", 20)
		Expect(evs).To(HaveLen(1))
		Expect(evs[0].Reason).To(Equal(CodeSenderNeedsPairing))
	})

	It("records a call that an unattended run is not allowed to make", func() {
		setup(write, session.OriginJob, func(context.Context) (addontools.ToolResult, error) {
			Fail("the add-on must not be called")
			return addontools.ToolResult{}, nil
		})
		// Give the tool the policy an unattended run holds when its owner fixed
		// it to read and report only.
		gated := svc.gateAddonAct(rec, builtins.ActPolicy{ReadOnly: true}, session.Tool{Name: write.Name, Run: broker.rawRun()}, write)
		gated.Run = svc.outcomeRun(rec, workspace.Actor{Principal: "alice", ChatID: "c1"}, write, gated.Run)
		out, err := gated.Run(ctx, json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("read and report only"), "the sentence for the model is unchanged")
		lines := logs.lines()
		Expect(lines).To(HaveLen(1))
		for _, w := range []string{"chat=job", "sender=job", "outcome=denied", "reason=act_read_only"} {
			Expect(lines[0]).To(ContainSubstring(w))
		}
	})

	It("names a trigger as the sender of an unattended call", func() {
		tool := build(read, session.OriginTrigger, false, func(context.Context) (addontools.ToolResult, error) {
			return addontools.ToolResult{Kind: addontools.ResultData}, nil
		})
		run(tool)
		Expect(logs.lines()).To(HaveLen(1))
		Expect(logs.lines()[0]).To(ContainSubstring("sender=trigger"))
	})

	It("writes a repeated line once and says how many were held back on the next", func() {
		tool := build(write, session.OriginConnector, true, errored(addontools.CodeUpstreamRefused, 403))
		for range 5 {
			run(tool)
		}
		Expect(logs.lines()).To(HaveLen(1), "a loop that fails the same way writes one line")
		clock.advance(time.Minute)
		run(tool)
		lines := logs.lines()
		Expect(lines).To(HaveLen(2))
		Expect(lines[1]).To(ContainSubstring("repeated=4"))
		// The settings page keeps every call: it is bounded by count, not by time.
		Expect(svc.access.recentTools("cred-1", 50)).To(HaveLen(6))
		// A different ending is a different line.
		broker.script = errored(addontools.CodeUpstreamRefused, 404)
		run(tool)
		Expect(logs.lines()).To(HaveLen(3))
	})

	It("keeps the last 20 outcomes of a connection, newest first, for it and for the bot", func() {
		tool := build(write, session.OriginConnector, true, func(context.Context) (addontools.ToolResult, error) {
			return addontools.ToolResult{Kind: addontools.ResultProposal, Proposal: &addontools.ProposalRef{ID: "p", Hold: "approval_always"}}, nil
		})
		for i := range 25 {
			broker.took = time.Duration(i+1) * time.Second
			run(tool)
		}
		for _, id := range []string{"cred-1", "b-slack"} {
			evs := svc.access.recentTools(id, 50)
			Expect(evs).To(HaveLen(ToolOutcomesPerBinding), id)
			Expect(evs[0].At.After(evs[len(evs)-1].At)).To(BeTrue(), "newest first")
			Expect(evs[0].Tool).To(Equal("create-issue"))
			Expect(evs[0].Outcome).To(Equal(addontools.OutcomeHeld))
			Expect(evs[0].Reason).To(Equal("approval_always"))
		}
		Expect(svc.access.recentTools("other", 50)).To(BeEmpty())
	})
})

// rawRun is the call the broker would make, for a spec that wraps it by hand.
func (b *scriptBroker) rawRun() func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		return b.Call(ctx, "alice", "c1", b.tool.Name, args)
	}
}
