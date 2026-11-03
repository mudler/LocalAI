package studio_test

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/addons/testhost"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The file limit of a binding is the smallest of the host ceiling, the ability
// that the kind declares and the owner's setting.
// The specs run the same checks over two real add-ons (Telegram and Slack, on
// their fakes) and over a kind that only a fake declares, so nothing in the
// host knows a platform.

// limitRig is a connector chat on one platform, as the specs of the limits
// see it.
type limitRig struct {
	r     *connRig
	title string
	// ask sends the message from the person.
	ask func(text string)
	// files is how many files reached the person.
	files func() int
	// said are the messages the bot sent.
	said func() []string
}

type limitSetup struct {
	declared *contract.ConnectorFiles
	input    studio.ConnectorInput
	ceiling  int64
	script   func(workspace.Actor, string) []studiotest.ToolStep
}

func (s limitSetup) options(o *studio.ConnectorOptions) {
	if s.ceiling > 0 {
		o.FileCeiling = func(context.Context) (int64, error) { return s.ceiling, nil }
	}
}

var platforms = map[string]func(limitSetup) limitRig{
	"Telegram": func(s limitSetup) limitRig {
		bot := newScriptedBot()
		addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
		if s.declared != nil {
			addon.Files = s.declared
		}
		r := newConnRigOpts(newShared(), connStore(), studiobot.NewTransport(addon), s.script, nil, s.options)
		r.bot = bot
		s.input.Allow = []string{"11"}
		r.bind(s.input)
		n := 0
		return limitRig{r: r, title: "Telegram",
			ask:   func(text string) { n++; bot.say(n, 11, "Ada", "ada", text) },
			files: func() int { return len(bot.sentFiles()) },
			said:  bot.answers}
	},
	"Slack": func(s limitSetup) limitRig {
		r, _ := newSlackRigDeclaring(s.script, studio.ConnectorInput{Allow: []string{"U1"}, FileReceiveMax: s.input.FileReceiveMax, FileSendMax: s.input.FileSendMax}, s.declared, s.options)
		return limitRig{r: r.connRig, title: "Slack",
			ask:   func(text string) { r.slack.Push(slackApp, slacktest.DM("D1", "U1", text)) },
			files: func() int { return len(r.slack.Uploads(slackBot)) },
			said: func() []string {
				var out []string
				for _, p := range r.answers() {
					out = append(out, p.Text)
				}
				return out
			}}
	},
}

// fakeRuntime is a connector add-on that a spec scripts.
type fakeRuntime func(contract.ConnectorRequest) contract.ConnectorResponse

func (f fakeRuntime) Connector(_ context.Context, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return f(req), nil
}

func ptr(n int64) *int64 { return &n }

var _ = Describe("file limits in a connector chat", func() {
	body := func(n int) string { return strings.Repeat("x", n) }
	// makeAndAttach writes a file of n bytes and attaches it by hand too.
	makeAndAttach := func(n int) func(workspace.Actor, string) []studiotest.ToolStep {
		return attachScript(map[string][]studiotest.ToolStep{
			"go": {write("/out/data.csv", body(n)), attach("/out/data.csv"), {Say: "Done."}},
		})
	}
	// refused returns the text that the model got when the file was too large.
	refused := func(r limitRig) string {
		res := r.r.h.ResultsFor("attach_file")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(HaveOccurred())
		return res[0].Err.Error()
	}

	DescribeTable("the layer that is smallest sets the limit and is named in the sentences",
		func(platform string, s limitSetup, size int, want string) {
			s.script = makeAndAttach(size)
			r := platforms[platform](s)
			r.ask("go")
			waitFor("the note about the file", func() bool { return len(r.said()) >= 2 })
			Expect(refused(r)).To(ContainSubstring(want), "what the model reads")
			Expect(strings.Join(r.said(), "\n")).To(ContainSubstring("the limit is "+want), "what the person reads")
			Expect(r.files()).To(BeZero(), "a file over the limit is not sent")
		},
		Entry("an add-on that declares a lower limit: Telegram", "Telegram", limitSetup{declared: &contract.ConnectorFiles{Send: &contract.FileAbility{MaxBytes: 300}}}, 400, "300 bytes (limit of Telegram)"),
		Entry("an add-on that declares a lower limit: Slack", "Slack", limitSetup{declared: &contract.ConnectorFiles{Send: &contract.FileAbility{MaxBytes: 300}}}, 400, "300 bytes (limit of Slack)"),
		Entry("the owner lowers the declared limit: Telegram", "Telegram", limitSetup{declared: &contract.ConnectorFiles{Send: &contract.FileAbility{MaxBytes: 300}}, input: studio.ConnectorInput{FileSendMax: ptr(100)}}, 150, "100 bytes (limit set for this bot)"),
		Entry("the owner lowers the declared limit: Slack", "Slack", limitSetup{declared: &contract.ConnectorFiles{Send: &contract.FileAbility{MaxBytes: 300}}, input: studio.ConnectorInput{FileSendMax: ptr(100)}}, 150, "100 bytes (limit set for this bot)"),
		Entry("the host ceiling is the smallest: Telegram", "Telegram", limitSetup{declared: &contract.ConnectorFiles{Send: &contract.FileAbility{MaxBytes: 300}}, ceiling: 50}, 100, "50 bytes (limit of the platform)"),
		Entry("the host ceiling is the smallest: Slack", "Slack", limitSetup{declared: &contract.ConnectorFiles{Send: &contract.FileAbility{MaxBytes: 300}}, ceiling: 50}, 100, "50 bytes (limit of the platform)"),
		Entry("an add-on that declares more than the old inline cap: its own limit", "Telegram", limitSetup{declared: &contract.ConnectorFiles{Send: &contract.FileAbility{MaxBytes: 50 << 20}}, ceiling: 2 << 20}, 3<<20, "2 MiB (limit of the platform)"),
	)

	DescribeTable("sends a file that is within the limit",
		func(platform string) {
			r := platforms[platform](limitSetup{
				declared: &contract.ConnectorFiles{Send: &contract.FileAbility{MaxBytes: 300}},
				input:    studio.ConnectorInput{FileSendMax: ptr(200)},
				script:   makeAndAttach(150),
			})
			r.ask("go")
			waitFor("the file", func() bool { return r.files() >= 1 })
			Expect(r.r.h.ResultsFor("attach_file")[0].Err).NotTo(HaveOccurred())
		},
		Entry("Telegram", "Telegram"),
		Entry("Slack", "Slack"),
	)

	Describe("the model note", func() {
		It("states the limit in effect instead of a fixed number", func() {
			r := platforms["Telegram"](limitSetup{
				declared: &contract.ConnectorFiles{Send: &contract.FileAbility{MaxBytes: 300 << 10}},
				input:    studio.ConnectorInput{FileSendMax: ptr(100 << 10)},
				script:   connSay("ok"),
			})
			r.ask("hello")
			waitFor("the turn", func() bool { return len(r.said()) >= 1 })
			chats, err := r.r.svc.List(context.Background(), alice, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(chats).To(HaveLen(1))
			Expect(r.r.h.TurnPrompt(chats[0].ID)).To(ContainSubstring("100 KiB (limit set for this bot) each"))
		})
	})

	Describe("a kind that only a fake declares", func() {
		// chatty is a push kind with its own limits; the fake serves the op
		// media with a file of the size a spec sets.
		var (
			size  atomic.Int64
			sent  atomic.Value
			calls atomic.Int32
		)
		chatty := studiobot.Addon{ID: "chatty-addon",
			Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPush,
				Files: &contract.ConnectorFiles{Receive: &contract.FileAbility{MaxBytes: 300}, Send: &contract.FileAbility{MaxBytes: 700}}},
			Files: &contract.ConnectorFiles{Receive: &contract.FileAbility{MaxBytes: 300}, Send: &contract.FileAbility{MaxBytes: 700}},
			// The fake writes the file to the host by the handle, as an add-on does.
			WithHost: func(h testhost.Host) contract.ConnectorRuntime {
				return fakeRuntime(func(req contract.ConnectorRequest) contract.ConnectorResponse {
					switch req.Op {
					case contract.ConnectorOpMedia:
						var res contract.ConnectorFilePutResult
						put := contract.ConnectorFilePut{Handle: req.Transfer.Handle, Data: []byte(strings.Repeat("y", int(size.Load()))), Final: true}
						if err := h.Do(context.Background(), req.Transfer.Handle, contract.ConnectorFilePutOp, put, &res); err != nil {
							return contract.ConnectorResponse{Error: &contract.ConnectorError{Code: contract.ConnectorTooLarge, Message: err.Error()}}
						}
						return contract.ConnectorResponse{Received: &contract.ConnectorReceived{Name: "in.txt", MIME: "text/plain"}}
					case contract.ConnectorOpSend:
						calls.Add(1)
						sent.Store(req.Text)
					}
					return contract.ConnectorResponse{}
				})
			}}

		bindChatty := func(in studio.ConnectorInput) (*connRig, connectors.Binding, error) {
			r := newConnRigOpts(newShared(), connStore(), studiobot.NewTransport(chatty), connSay("ok"), nil, nil)
			in.Kind, in.Allow = "chatty", []string{"11"}
			if in.Name == "" {
				in.Name = "Chatty bot"
			}
			if in.Token == "" {
				in.Token = "tok"
			}
			in.Enabled = true
			if r.agent.ID == "" {
				ag, err := r.svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Support", Model: "m"})
				Expect(err).NotTo(HaveOccurred())
				r.agent = ag
			}
			in.AgentID = r.agent.ID
			info, err := r.conns.CreateConnector(context.Background(), alice, in)
			if err == nil {
				r.trust(info.ID)
			}
			return r, info.Binding, err
		}

		It("resolves the limits of its own declaration, with the title it declares", func() {
			r, b, err := bindChatty(studio.ConnectorInput{})
			Expect(err).NotTo(HaveOccurred())
			l := r.conns.FileLimits(context.Background(), b)
			Expect(l.Receive).To(Equal(studio.FileLimit{Bytes: 300, Layer: studio.LayerAddon}))
			Expect(l.Send).To(Equal(studio.FileLimit{Bytes: 700, Layer: studio.LayerAddon}))
			Expect([]int64{l.ReceiveMax, l.SendMax}).To(Equal([]int64{300, 700}))
			Expect(l.KindTitle).To(Equal("Chatty"))
		})

		It("tells a sender whose file is over the limit of the kind which limit it is", func() {
			size.Store(400)
			r, b, err := bindChatty(studio.ConnectorInput{})
			Expect(err).NotTo(HaveOccurred())
			Expect(r.conns.Ingest(context.Background(), "chatty-addon", contract.ConnectorInbound{BindingID: b.ID,
				ConnectorMessage: contract.ConnectorMessage{Chat: "11", SenderID: "11", Direct: true, Media: []string{"m1"}}})).To(Succeed())
			waitFor("the sentence", func() bool { return calls.Load() >= 1 })
			Expect(sent.Load()).To(Equal("That file is too large: I can read files of up to 300 bytes here (limit of Chatty)."))
		})

		It("lets the owner lower the limit for a bot, and the sentence says so", func() {
			size.Store(150)
			r, b, err := bindChatty(studio.ConnectorInput{FileReceiveMax: ptr(100)})
			Expect(err).NotTo(HaveOccurred())
			Expect(r.conns.FileLimits(context.Background(), b).Receive).To(Equal(studio.FileLimit{Bytes: 100, Layer: studio.LayerOwner}))
			Expect(r.conns.Ingest(context.Background(), "chatty-addon", contract.ConnectorInbound{BindingID: b.ID,
				ConnectorMessage: contract.ConnectorMessage{Chat: "11", SenderID: "11", Direct: true, Media: []string{"m1"}}})).To(Succeed())
			waitFor("the sentence", func() bool { return calls.Load() >= 1 })
			Expect(sent.Load()).To(Equal("That file is too large: I can read files of up to 100 bytes here (limit set for this bot)."))
		})

		It("refuses a limit above the one of the kind: an owner can only lower it", func() {
			_, _, err := bindChatty(studio.ConnectorInput{FileSendMax: ptr(701)})
			Expect(err).To(MatchError(connectors.ErrInvalid))
			Expect(err).To(MatchError(ContainSubstring("at most 700 bytes")))
			_, _, err = bindChatty(studio.ConnectorInput{FileReceiveMax: ptr(300), FileSendMax: ptr(700)})
			Expect(err).NotTo(HaveOccurred())
		})

		It("takes no file from a sender, and sends none, when the kind declares no files ability", func() {
			var (
				said  atomic.Value
				heard atomic.Int32
			)
			mute := studiobot.Addon{ID: "mute-addon",
				Kind: contract.ConnectorKind{ID: "mute", Title: "Mute", Delivery: contract.DeliveryPush},
				Runtime: fakeRuntime(func(req contract.ConnectorRequest) contract.ConnectorResponse {
					if req.Op == contract.ConnectorOpSend {
						said.Store(req.Text)
						heard.Add(1)
					}
					return contract.ConnectorResponse{}
				})}
			r := newConnRigOpts(newShared(), connStore(), studiobot.NewTransport(mute), connSay("ok"), nil, nil)
			ag, err := r.svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Support", Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			info, err := r.conns.CreateConnector(context.Background(), alice, studio.ConnectorInput{Kind: "mute", Name: "m", Token: "t", AgentID: ag.ID, Allow: []string{"11"}, Enabled: true})
			Expect(err).NotTo(HaveOccurred())
			r.trust(info.ID)
			l := r.conns.FileLimits(context.Background(), info.Binding)
			Expect([]int64{l.Receive.Bytes, l.Send.Bytes, l.ReceiveMax, l.SendMax}).To(Equal([]int64{0, 0, 0, 0}))
			Expect(r.conns.Ingest(context.Background(), "mute-addon", contract.ConnectorInbound{BindingID: info.ID,
				ConnectorMessage: contract.ConnectorMessage{Chat: "11", SenderID: "11", Direct: true, Media: []string{"m1"}}})).To(Succeed())
			waitFor("the sentence", func() bool { return heard.Load() >= 1 })
			Expect(said.Load()).To(Equal("I cannot take files on this platform."))
		})

		It("bounds what an owner may set by the host ceiling", func() {
			r := newConnRigOpts(newShared(), connStore(), studiobot.NewTransport(chatty), connSay("ok"), nil, func(o *studio.ConnectorOptions) {
				o.FileCeiling = func(context.Context) (int64, error) { return 200, nil }
			})
			ag, err := r.svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Support", Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.conns.CreateConnector(context.Background(), alice, studio.ConnectorInput{Kind: "chatty", Name: "c", Token: "t", AgentID: ag.ID, FileSendMax: ptr(300)})
			Expect(err).To(MatchError(ContainSubstring("at most 200 bytes")))
		})
	})
})
