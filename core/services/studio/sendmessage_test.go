package studio_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// sendScript drives a chat in Studio: the word "call" makes one or more tool
// calls, and a loop run does what the spec set for it.
type sendScript struct {
	mu    sync.Mutex
	calls []studiotest.ToolStep
	run   []studiotest.ToolStep
}

func (s *sendScript) setCalls(steps ...studiotest.ToolStep) {
	s.mu.Lock()
	s.calls = steps
	s.mu.Unlock()
}

func (s *sendScript) setRun(steps ...studiotest.ToolStep) {
	s.mu.Lock()
	s.run = steps
	s.mu.Unlock()
}

func (s *sendScript) script(_ workspace.Actor, text string) []studiotest.ToolStep {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case strings.Contains(text, "[Loop run"):
		return append(append([]studiotest.ToolStep{}, s.run...), studiotest.ToolStep{Say: "run done"})
	case strings.HasSuffix(strings.TrimSpace(text), "call"):
		return append(append([]studiotest.ToolStep{}, s.calls...), studiotest.ToolStep{Say: "ok"})
	}
	return []studiotest.ToolStep{{Say: "ok"}}
}

// The model of a chat in Studio, and the runs of its loops, write to a
// connection of the owner with send_message; a loop may also report to one.
// The kinds here are made-up platforms: one receives messages, one is a
// service that agents use only as a tool.
var _ = Describe("writing to a connection from a chat in Studio", func() {
	ctx := context.Background()
	const bob = "bob"
	var (
		usersErr error // what the account check of the rig answers
		r        *connRig
		sc       *sendScript
		gate     *capSwitch
		fake     *postAddon
		host     *studio.Loops
		ls       loops.Store
		chat     string
		team     connectors.Binding // a connection that can receive messages
		spare    connectors.Binding // a second one, to name when another is refused
		svcB     connectors.Binding // a service that agents only use as a tool
		other    connectors.Binding // a connection of another owner
	)
	general := connectors.Channel{ID: "G1", Label: "General", Approved: true}
	ops := connectors.Channel{ID: "G2", Label: "Ops"} // listed, not approved
	alerts := connectors.Channel{ID: "G3", Label: "Alerts", Approved: true}

	setup := func(bind bool) {
		sc = &sendScript{}
		fake = &postAddon{}
		tp := studiobot.NewTransport(
			studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake},
			studiobot.Addon{ID: "service-addon", Kind: contract.ConnectorKind{ID: "servicey", Title: "Servicey", Delivery: contract.DeliveryPull, ToolOnly: true}, Runtime: &postAddon{}},
		)
		sh := newShared()
		usersErr = nil
		sh.users = func(string) error { return usersErr }
		r = newConnRigOpts(sh, connStore(), tp, sc.script, nil, func(o *studio.ConnectorOptions) { o.OutboxPoll = 10 * time.Millisecond })
		gate = &capSwitch{}
		r.svc.SetCapabilityGate(gate)
		gate.set(contract.CapTalkbackChannels, true)
		var err error
		ls, err = loops.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host, err = studio.NewLoops(r.svc, studio.LoopOptions{Store: ls, Clock: sh.clock.Now, Tick: time.Hour})
		Expect(err).NotTo(HaveOccurred())
		r.svc.SetLoops(host)
		DeferCleanup(host.Close)
		if bind {
			team = r.bind(studio.ConnectorInput{Kind: "chatty", Name: "Team chat", Token: "secret-token-1", Allow: []string{"11"}, Channels: []connectors.Channel{general, ops, alerts}})
		}
		chat = create(r.svc, alice)
	}
	// setupAll adds the connections that the refusals need.
	setupAll := func() {
		setup(true)
		spare = r.bind(studio.ConnectorInput{Kind: "chatty", Name: "Spare", Token: "secret-token-4", Allow: []string{"11"}, Channels: []connectors.Channel{general}})
		sb, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: "servicey", Name: "Code host", Token: "secret-token-2", ConfirmDuplicate: studio.DuplicateConfirmation})
		Expect(err).NotTo(HaveOccurred())
		svcB = sb.Binding
		agB, err := r.svc.SaveAgent(ctx, bob, agents.Agent{Name: "Other", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		ob, err := r.conns.CreateConnector(ctx, bob, studio.ConnectorInput{Kind: "chatty", Name: "Not yours", Token: "secret-token-3", AgentID: agB.ID, Enabled: true,
			Allow: []string{"11"}, Channels: []connectors.Channel{{ID: "B1", Label: "Bob room", Approved: true}}, ConfirmDuplicate: studio.DuplicateConfirmation, Reply: connectors.ReplyOff})
		Expect(err).NotTo(HaveOccurred())
		r.trust(ob.ID)
		other = ob.Binding
	}
	update := func(b connectors.Binding, f func(*studio.ConnectorInput)) {
		in := studio.ConnectorInput{Name: b.Name, AgentID: b.AgentID, Enabled: true, Allow: b.Allow, Channels: b.Channels}
		f(&in)
		_, err := r.conns.UpdateConnector(ctx, alice, b.ID, in)
		Expect(err).NotTo(HaveOccurred())
	}
	// call runs the tool calls in one turn of the chat and returns the results.
	call := func(tool string, args ...string) []studiotest.ToolResult {
		steps := make([]studiotest.ToolStep, len(args))
		for i, a := range args {
			steps[i] = studiotest.ToolStep{Tool: tool, Args: a}
		}
		sc.setCalls(steps...)
		before := len(r.h.ResultsFor(tool))
		sent(r.svc, alice, chat, "call")
		idle(r.svc, alice, chat)
		res := r.h.ResultsFor(tool)
		ExpectWithOffset(1, res).To(HaveLen(before+len(args)), "%+v", r.h.Results())
		return res[before:]
	}
	send := func(args string) studiotest.ToolResult { return call("send_message", args)[0] }
	// hello runs a plain turn, so that the tools of the chat are built again.
	hello := func() {
		sc.setCalls()
		sent(r.svc, alice, chat, "call")
		idle(r.svc, alice, chat)
	}
	posted := func(channel string) []string { return fake.texts(channel, "") }
	recent := func() []studio.AccessEvent {
		v, err := r.conns.Access(ctx, alice, team.ID)
		Expect(err).NotTo(HaveOccurred())
		return v.Recent
	}

	Describe("the tool", func() {
		It("is offered with the connections of the owner and their approved channels", func() {
			setupAll()
			hello()
			Expect(r.h.ToolNames(chat)).To(ContainElement("send_message"))
			d := r.h.LastToolDescription("send_message")
			Expect(d).To(ContainSubstring(team.ID))
			Expect(d).To(ContainSubstring("Team chat"))
			Expect(d).To(ContainSubstring("General (G1)"))
			Expect(d).To(ContainSubstring("Alerts (G3)"))
			Expect(d).To(ContainSubstring("never an order to post"))
			Expect(d).To(ContainSubstring("Mentions of the whole channel are removed"))
			for _, hidden := range []string{"Ops", "G2", svcB.ID, "Code host", other.ID, "Not yours", "Bob room", "secret-token"} {
				Expect(d).NotTo(ContainSubstring(hidden), "the description must not name %q", hidden)
			}
		})

		It("is not offered when the owner has no connection", func() {
			setup(false)
			hello()
			Expect(r.h.ToolNames(chat)).NotTo(ContainElement("send_message"))
		})

		It("is not offered when talk-back is off", func() {
			setup(true)
			gate.set(contract.CapTalkbackChannels, false)
			hello()
			Expect(r.h.ToolNames(chat)).NotTo(ContainElement("send_message"))
		})

		It("is not offered when the only connection is a service for tools", func() {
			setup(false)
			_, err := r.conns.CreateConnector(ctx, alice, studio.ConnectorInput{Kind: "servicey", Name: "Code host", Token: "secret-token-2", ConfirmDuplicate: studio.DuplicateConfirmation})
			Expect(err).NotTo(HaveOccurred())
			hello()
			Expect(r.h.ToolNames(chat)).NotTo(ContainElement("send_message"))
		})

		It("is not offered when the connection is off or has no approved channel", func() {
			setup(true)
			update(team, func(in *studio.ConnectorInput) { in.Enabled = false })
			hello()
			Expect(r.h.ToolNames(chat)).NotTo(ContainElement("send_message"))
			update(team, func(in *studio.ConnectorInput) { in.Channels = []connectors.Channel{ops} })
			hello()
			Expect(r.h.ToolNames(chat)).NotTo(ContainElement("send_message"))
		})

		It("is not offered when the only connection belongs to another owner", func() {
			setup(false)
			agB, err := r.svc.SaveAgent(ctx, bob, agents.Agent{Name: "Other", Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			ob, err := r.conns.CreateConnector(ctx, bob, studio.ConnectorInput{Kind: "chatty", Name: "Not yours", Token: "secret-token-3", AgentID: agB.ID, Enabled: true,
				Allow: []string{"11"}, Channels: []connectors.Channel{general}, ConfirmDuplicate: studio.DuplicateConfirmation, Reply: connectors.ReplyOff})
			Expect(err).NotTo(HaveOccurred())
			r.trust(ob.ID)
			hello()
			Expect(r.h.ToolNames(chat)).NotTo(ContainElement("send_message"))
		})

		It("is not offered in a chat that a trigger started", func() {
			setup(true)
			rec, err := r.svc.CreateAs(ctx, alice, studio.CreateInput{Model: "m"}, studio.Origin{Kind: session.OriginTrigger, ID: "t1"})
			Expect(err).NotTo(HaveOccurred())
			sent(r.svc, alice, rec.ID, "hello")
			idle(r.svc, alice, rec.ID)
			Expect(r.h.ToolNames(rec.ID)).NotTo(ContainElement("send_message"))
		})
	})

	Describe("a call", func() {
		It("writes in the approved channel, removes mentions of everyone and is audited without the text", func() {
			setupAll()
			res := send(fmt.Sprintf(`{"text":"The build is ready @channel","connection":%q,"channel":"General"}`, team.ID))
			Expect(res.Err).To(BeNil(), "%+v", res)
			Expect(res.Out).To(Equal("Sent."))
			waitFor("the message", func() bool { return len(posted("G1")) == 1 })
			Expect(posted("G1")[0]).To(ContainSubstring("The build is ready"))
			Expect(posted("G1")[0]).NotTo(ContainSubstring("@channel"))
			var found bool
			for _, e := range recent() {
				if e.Action == studio.SendStudio {
					found = true
					Expect(e.Allowed).To(BeTrue())
					Expect(e.Channel).To(Equal("G1"))
					Expect(e.Hash).NotTo(BeEmpty())
					Expect(fmt.Sprintf("%+v", e)).NotTo(ContainSubstring("build is ready"))
				}
			}
			Expect(found).To(BeTrue(), "the send is in the audit: %+v", recent())
		})

		It("takes the channel by its id, and the only approved channel when it is left out", func() {
			setupAll()
			Expect(send(fmt.Sprintf(`{"text":"by id","connection":%q,"channel":"G3"}`, team.ID)).Err).To(BeNil())
			Expect(send(fmt.Sprintf(`{"text":"only one","connection":%q}`, spare.ID)).Err).To(BeNil())
			waitFor("both messages", func() bool { return len(posted("G3")) == 1 && len(posted("G1")) == 1 })
		})

		It("asks for a channel when the connection has several, and names them", func() {
			setupAll()
			res := send(fmt.Sprintf(`{"text":"hi","connection":%q}`, team.ID))
			Expect(res.Err).To(MatchError(ContainSubstring("name the channel")))
			Expect(res.Err.Error()).To(ContainSubstring("General (G1)"))
			Expect(fake.count()).To(BeZero())
		})

		It("never guesses a connection: none is an error that names the valid ones", func() {
			setupAll()
			res := send(`{"text":"hi"}`)
			Expect(res.Err).To(MatchError(ContainSubstring("connection is needed")))
			Expect(res.Err.Error()).To(ContainSubstring(team.ID))
			Expect(res.Err.Error()).To(ContainSubstring(spare.ID))
			Expect(res.Err.Error()).NotTo(ContainSubstring(other.ID))
			Expect(fake.count()).To(BeZero())
		})

		It("refuses a connection of another owner and says nothing about it", func() {
			setupAll()
			res := send(fmt.Sprintf(`{"text":"hi","connection":%q,"channel":"B1"}`, other.ID))
			Expect(res.Err).To(MatchError(ContainSubstring("not one of the person's connections")))
			for _, secret := range []string{"Not yours", "Bob room", "B1"} {
				Expect(res.Err.Error()).NotTo(ContainSubstring(secret))
			}
			Expect(fake.count()).To(BeZero())
		})

		It("refuses a connection that is off, and audits the refusal", func() {
			setupAll()
			update(spare, func(in *studio.ConnectorInput) { in.Enabled = false })
			res := send(fmt.Sprintf(`{"text":"hi","connection":%q,"channel":"G1"}`, spare.ID))
			Expect(res.Err).To(MatchError(ContainSubstring("switched off")))
			Expect(fake.count()).To(BeZero())
			v, err := r.conns.Access(ctx, alice, spare.ID)
			Expect(err).NotTo(HaveOccurred())
			var refused bool
			for _, e := range v.Recent {
				refused = refused || (e.Action == studio.SendStudio && !e.Allowed && strings.Contains(e.Reason, "switched off"))
			}
			Expect(refused).To(BeTrue(), "%+v", v.Recent)
		})

		It("refuses a listed channel that the owner did not approve", func() {
			setupAll()
			res := send(fmt.Sprintf(`{"text":"hi","connection":%q,"channel":"Ops"}`, team.ID))
			Expect(res.Err).To(HaveOccurred())
			Expect(res.Err.Error()).To(ContainSubstring("listed"))
			Expect(fake.count()).To(BeZero())
		})

		It("refuses a channel that is not listed at all", func() {
			setupAll()
			res := send(fmt.Sprintf(`{"text":"hi","connection":%q,"channel":"Secret"}`, team.ID))
			Expect(res.Err).To(MatchError(ContainSubstring("not a channel the owner approved")))
			Expect(fake.count()).To(BeZero())
		})

		It("refuses a kind that cannot receive messages", func() {
			setupAll()
			res := send(fmt.Sprintf(`{"text":"hi","connection":%q,"channel":"x"}`, svcB.ID))
			Expect(res.Err).To(MatchError(ContainSubstring("cannot receive messages")))
			Expect(fake.count()).To(BeZero())
		})

		It("refuses an empty text and a control character in the channel", func() {
			setupAll()
			Expect(send(fmt.Sprintf(`{"text":"  ","connection":%q,"channel":"G1"}`, team.ID)).Err).To(HaveOccurred())
			Expect(send(fmt.Sprintf(`{"text":"hi","connection":%q,"channel":"G1\u0000"}`, team.ID)).Err).To(HaveOccurred())
			Expect(fake.count()).To(BeZero())
		})

		It("limits the messages of one chat, and a refused one does not count", func() {
			setupAll()
			args := make([]string, 7)
			for i := range args {
				args[i] = fmt.Sprintf(`{"text":"message %d","connection":%q,"channel":"G1"}`, i, team.ID)
			}
			res := call("send_message", args...)
			for i := range 5 {
				Expect(res[i].Err).To(BeNil(), "message %d: %+v", i, res[i])
			}
			Expect(res[5].Err).To(MatchError(ContainSubstring("the most it may")))
			Expect(res[6].Err).To(MatchError(ContainSubstring("the most it may")))
			waitFor("five messages", func() bool { return len(posted("G1")) == 5 })
			Expect(fake.count()).To(Equal(5))

			// Another chat of the same owner has its own budget.
			chat = create(r.svc, alice)
			Expect(send(fmt.Sprintf(`{"text":"other chat","connection":%q,"channel":"G1"}`, team.ID)).Err).To(BeNil())
		})
	})

	Describe("a loop report", func() {
		// startLoop arms a loop through the tool and returns the result.
		startLoop := func(report string) studiotest.ToolResult {
			args := `{"prompt":"Check the download.","first_run_after":"1m"`
			if report != "" {
				args += `,"report":` + report
			}
			return call("start_loop", args+`}`)[0]
		}
		only := func() loops.Loop {
			list, err := host.List(ctx, alice, chat)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			return list[0]
		}
		// run advances the clock and makes the loop take a run, then waits for it to end.
		run := func(id string) loops.Loop {
			before, _ := ls.Get(ctx, id)
			r.sh.clock.Advance(time.Minute + time.Second)
			until("the host to take a run", func() bool {
				Expect(host.Pass(ctx)).To(Succeed())
				cur, _ := ls.Get(ctx, id)
				return cur.Runs > before.Runs
			})
			var l loops.Loop
			until("the run to end", func() bool {
				l, _ = ls.Get(ctx, id)
				return !l.Running
			})
			return l
		}
		report := func(mode string) string {
			return fmt.Sprintf(`{"connection":%q,"channel":"General","mode":%q}`, team.ID, mode)
		}

		It("is stored with the destination of the owner and told to the model", func() {
			setupAll()
			res := startLoop(report("on_done"))
			Expect(res.Err).To(BeNil(), "%+v", res)
			Expect(res.Out).To(ContainSubstring("report to the channel General of the connection Team chat (on_done)"))
			l := only()
			Expect(l.Report).To(Equal(&loops.Report{Connection: team.ID, Channel: "G1", Mode: loops.ReportOnDone}))
			v := host.ReportOf(ctx, l)
			Expect(v.ConnectionName).To(Equal("Team chat"))
			Expect(v.ChannelLabel).To(Equal("General"))
		})

		It("sends nothing when there is no report, or its mode is never", func() {
			setupAll()
			Expect(startLoop("").Err).To(BeNil())
			l := only()
			Expect(l.Report).To(BeNil())
			sc.setRun(studiotest.ToolStep{Tool: "loop_done", Args: `{"summary":"all done"}`})
			run(l.ID)
			Consistently(fake.count, 300*time.Millisecond, 20*time.Millisecond).Should(BeZero())

			setupAll()
			Expect(startLoop(report("never")).Err).To(BeNil())
			l = only()
			Expect(l.Report).To(BeNil(), "a report with mode never is no report")
			sc.setRun(studiotest.ToolStep{Tool: "loop_done", Args: `{"summary":"all done"}`})
			run(l.ID)
			Consistently(fake.count, 300*time.Millisecond, 20*time.Millisecond).Should(BeZero())
		})

		It("refuses a destination that a trigger would refuse, and starts no loop", func() {
			setupAll()
			update(spare, func(in *studio.ConnectorInput) { in.Enabled = false })
			for what, rep := range map[string]string{
				"another owner":     fmt.Sprintf(`{"connection":%q,"channel":"B1","mode":"on_done"}`, other.ID),
				"a disabled one":    fmt.Sprintf(`{"connection":%q,"channel":"G1","mode":"on_done"}`, spare.ID),
				"a tool service":    fmt.Sprintf(`{"connection":%q,"channel":"x","mode":"on_done"}`, svcB.ID),
				"an unapproved one": fmt.Sprintf(`{"connection":%q,"channel":"Ops","mode":"on_done"}`, team.ID),
				"no connection":     `{"channel":"General","mode":"on_done"}`,
				"no channel":        fmt.Sprintf(`{"connection":%q,"mode":"on_done"}`, team.ID),
				"a bad mode":        fmt.Sprintf(`{"connection":%q,"channel":"General","mode":"always"}`, team.ID),
			} {
				res := startLoop(rep)
				Expect(res.Err).To(HaveOccurred(), what)
				Expect(res.Err.Error()).To(ContainSubstring("no loop was started"), what)
				list, err := host.List(ctx, alice, chat)
				Expect(err).NotTo(HaveOccurred())
				Expect(list).To(BeEmpty(), what)
			}
		})

		It("reports the summary when the loop is done, and only then", func() {
			setupAll()
			Expect(startLoop(report("on_done")).Err).To(BeNil())
			l := only()
			sc.setRun(studiotest.ToolStep{Tool: "loop_done", Args: `{"summary":"the file was moved"}`})
			Expect(run(l.ID).State).To(Equal(loops.StateEnded))
			waitFor("the report", func() bool { return len(posted("G1")) == 1 })
			Expect(posted("G1")[0]).To(ContainSubstring("the file was moved"))
			Expect(posted("G1")[0]).To(ContainSubstring("Check the download."))
			var audited bool
			for _, e := range recent() {
				audited = audited || (e.Action == studio.SendLoopReport && e.Allowed)
			}
			Expect(audited).To(BeTrue(), "%+v", recent())
		})

		It("does not report a problem to an on_done loop, nor a summary to an on_problem one", func() {
			setupAll()
			Expect(startLoop(report("on_done")).Err).To(BeNil())
			l := only()
			sc.setRun(studiotest.ToolStep{Err: errors.New("the model is down")})
			Expect(run(l.ID).Failures).To(Equal(1))
			Consistently(fake.count, 300*time.Millisecond, 20*time.Millisecond).Should(BeZero())
			Expect(host.RemoveAll(ctx, alice, chat)).To(Equal(1))

			setupAll()
			Expect(startLoop(report("on_problem")).Err).To(BeNil())
			l = only()
			sc.setRun(studiotest.ToolStep{Tool: "loop_done", Args: `{"summary":"fine"}`})
			run(l.ID)
			Consistently(fake.count, 300*time.Millisecond, 20*time.Millisecond).Should(BeZero())
		})

		It("reports a failed run", func() {
			setupAll()
			Expect(startLoop(report("on_problem")).Err).To(BeNil())
			l := only()
			sc.setRun(studiotest.ToolStep{Err: errors.New("the model is down")})
			run(l.ID)
			waitFor("the first report", func() bool { return len(posted("G1")) == 1 })
			Expect(posted("G1")[0]).To(ContainSubstring("a run failed"))
		})

		It("reports a loop that stops at the run limit", func() {
			setupAll()
			Expect(host).NotTo(BeNil())
			small, err := studio.NewLoops(r.svc, studio.LoopOptions{Store: ls, Clock: r.sh.clock.Now, Tick: time.Hour,
				Limits: func() studio.LoopLimits { return studio.LoopLimits{MaxRuns: 1} }})
			Expect(err).NotTo(HaveOccurred())
			r.svc.SetLoops(small)
			host = small
			DeferCleanup(small.Close)
			Expect(startLoop(report("on_problem")).Err).To(BeNil())
			l := only()
			sc.setRun(studiotest.ToolStep{Tool: "schedule_next", Args: `{"after":"1m"}`})
			Expect(run(l.ID).State).To(Equal(loops.StateEnded))
			waitFor("the report", func() bool { return len(posted("G1")) == 1 })
			Expect(posted("G1")[0]).To(ContainSubstring("1 runs"))
		})

		It("reports a loop that the system stops", func() {
			setupAll()
			Expect(startLoop(report("on_problem")).Err).To(BeNil())
			l := only()
			usersErr = studio.ErrUserDisabled
			r.sh.clock.Advance(2 * time.Minute)
			Expect(host.Pass(ctx)).To(Succeed())
			Expect(ls.Get(ctx, l.ID)).To(HaveField("State", loops.StatePaused))
			waitFor("the report", func() bool { return len(posted("G1")) == 1 })
			Expect(posted("G1")[0]).To(ContainSubstring("account is disabled"))
		})

		It("checks the destination again when it reports", func() {
			for what, change := range map[string]func(){
				"the connection was switched off": func() { update(team, func(in *studio.ConnectorInput) { in.Enabled = false }) },
				"the channel is no longer approved": func() {
					update(team, func(in *studio.ConnectorInput) {
						in.Channels = []connectors.Channel{{ID: "G1", Label: "General"}, alerts}
					})
				},
				"the channel was removed": func() {
					update(team, func(in *studio.ConnectorInput) { in.Channels = []connectors.Channel{alerts} })
				},
				"talk-back was switched off": func() { gate.set(contract.CapTalkbackChannels, false) },
			} {
				setupAll()
				Expect(startLoop(report("on_done")).Err).To(BeNil(), what)
				l := only()
				change()
				sc.setRun(studiotest.ToolStep{Tool: "loop_done", Args: `{"summary":"done"}`})
				Expect(run(l.ID).State).To(Equal(loops.StateEnded), what)
				waitFor("the refusal in the audit: "+what, func() bool {
					for _, e := range recent() {
						if e.Action == studio.SendLoopReport && !e.Allowed {
							return true
						}
					}
					return false
				})
				Expect(fake.count()).To(BeZero(), what)
			}
		})
	})
})
