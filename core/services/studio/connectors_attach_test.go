package studio_test

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	slack "github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// attach_file hands a file that already exists to the person who talks to a
// connector chat. Delivery is the typed send_file op, so the specs drive it
// through two add-ons (Telegram and Slack) and through a plain chat.

// attachScript returns the steps by a word of the message: "make" writes a
// report, the other words attach what the spec names.
func attachScript(steps map[string][]studiotest.ToolStep) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(_ workspace.Actor, text string) []studiotest.ToolStep {
		for k, s := range steps {
			if strings.Contains(text, k) {
				return s
			}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
}

func attach(path string) studiotest.ToolStep {
	return studiotest.ToolStep{Tool: "attach_file", Args: `{"path":"` + path + `"}`}
}

func write(path, body string) studiotest.ToolStep {
	return studiotest.ToolStep{Tool: "write_file", Args: `{"path":"` + path + `","content":"` + body + `"}`}
}

var _ = Describe("attach_file in a connector chat", func() {
	var (
		sh  *shared
		bot *scriptedBot
	)
	BeforeEach(func() { sh, bot = newShared(), newScriptedBot() })

	// telegram binds the sender 11 over the scripted bot.
	telegram := func(steps map[string][]studiotest.ToolStep, in studio.ConnectorInput) *connRig {
		r := newConnRig(sh, connStore(), bot, attachScript(steps), nil)
		in.Allow, in.Reply = []string{"11"}, connectors.ReplyOff
		r.bind(in)
		return r
	}
	ask := func(id int, text string) { bot.say(id, 11, "Ada", "ada", text) }
	answered := func(n int) { waitFor("the answer", func() bool { return len(bot.answers()) >= n }) }

	It("sends a file that an earlier turn made", func() {
		r := telegram(map[string][]studiotest.ToolStep{
			"make":  {write("/out/report.csv", "a,b"), {Say: "Made."}},
			"again": {attach("/out/report.csv"), {Say: "Here it is."}},
		}, studio.ConnectorInput{})
		ask(1, "make")
		waitFor("the first file", func() bool { return len(bot.sentFiles()) == 1 })
		answered(1)
		ask(2, "again")
		waitFor("the second file", func() bool { return len(bot.sentFiles()) == 2 })
		answered(2)
		f := bot.sentFiles()[1]
		Expect([]string{f.Method, f.Name, string(f.Data)}).To(Equal([]string{"sendDocument", "report.csv", "a,b"}))
		res := r.h.ResultsFor("attach_file")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred())
		Expect(res[0].Out).To(ContainSubstring("report.csv"))
		Expect(bot.answers()[1]).To(Equal("Here it is."), "the answer comes before the file, and no note follows")
	})

	It("attaches a file of a folder whose name has a space, by the path the shell shows", func() {
		ctx := context.Background()
		Expect(sh.files.EnsureHome(ctx, alice)).To(Succeed())
		home := session.HomePath(alice)
		_, err := sh.files.Store().Mkdir(ctx, sh.files.Actor(alice), home+"/Design Docs")
		Expect(err).NotTo(HaveOccurred())
		_, err = sh.files.Store().Put(ctx, sh.files.Actor(alice), home+"/Design Docs/overview.pdf", strings.NewReader("%PDF-1.4 x"), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
		r := telegram(map[string][]studiotest.ToolStep{
			"send": {attach("/workspace/home/Design Docs/overview.pdf"), attach("/Design Docs/overview.pdf"), {Say: "Here it is."}},
		}, studio.ConnectorInput{})
		ask(1, "send")
		waitFor("the file", func() bool { return len(bot.sentFiles()) == 1 })
		answered(1)
		res := r.h.ResultsFor("attach_file")
		Expect(res).To(HaveLen(2))
		Expect(res[0].Err).NotTo(HaveOccurred())
		Expect(res[1].Out).To(ContainSubstring("attached already"))
		Expect(bot.sentFiles()[0].Name).To(Equal("overview.pdf"))
	})

	Describe("an answer that shows a file nobody attached", func() {
		It("ends with one plain note", func() {
			telegram(map[string][]studiotest.ToolStep{
				"fake": {{Say: "Here it is, I'll send you one. 📎 `design-notes.pdf`"}},
			}, studio.ConnectorInput{})
			ask(1, "fake")
			waitFor("the note", func() bool { return len(bot.answers()) == 2 })
			Expect(bot.answers()[0]).To(Equal("Here it is, I'll send you one. 📎 `design-notes.pdf`"), "the answer is not changed")
			Expect(bot.answers()[1]).To(Equal("I could not attach the file. Ask me again and I will send it."))
			Expect(bot.sentFiles()).To(BeEmpty())
		})

		It("adds no note when the file went with the answer", func() {
			telegram(map[string][]studiotest.ToolStep{
				"make": {write("/out/report.csv", "a,b"), {Say: "📎 `report.csv`"}},
			}, studio.ConnectorInput{})
			ask(1, "make")
			waitFor("the file", func() bool { return len(bot.sentFiles()) == 1 })
			answered(1)
			time.Sleep(300 * time.Millisecond)
			Expect(bot.answers()).To(HaveLen(1))
		})

		It("adds no note when the binding sends no files", func() {
			r := newConnRig(sh, connStore(), bot, attachScript(map[string][]studiotest.ToolStep{
				"fake": {{Say: "📎 `design-notes.pdf`"}},
			}), nil)
			r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyOff, SendFiles: &filesOff})
			ask(1, "fake")
			answered(1)
			time.Sleep(300 * time.Millisecond)
			Expect(bot.answers()).To(HaveLen(1))
		})

		It("adds no note to an answer that only talks about files", func() {
			telegram(map[string][]studiotest.ToolStep{
				"talk": {{Say: "The file is `report.pdf`. I can send it if you want."}},
			}, studio.ConnectorInput{})
			ask(1, "talk")
			answered(1)
			time.Sleep(300 * time.Millisecond)
			Expect(bot.answers()).To(HaveLen(1))
		})
	})

	It("sends a file once when it was made and attached in the same turn", func() {
		telegram(map[string][]studiotest.ToolStep{
			"make": {write("/out/report.csv", "a,b"), attach("/out/report.csv"), attach("/out/report.csv"), {Say: "Made."}},
		}, studio.ConnectorInput{})
		ask(1, "make")
		waitFor("the file", func() bool { return len(bot.sentFiles()) >= 1 })
		answered(1)
		time.Sleep(300 * time.Millisecond)
		Expect(bot.sentFiles()).To(HaveLen(1))
	})

	It("tells the model why a file cannot be attached", func() {
		big := strings.Repeat("x", 3<<20)
		steps := []studiotest.ToolStep{
			write("/out/big.csv", big), write("/out/tool.exe", "MZ"), write("/out/empty.txt", ""),
			attach("/out/missing.csv"), attach("/out/big.csv"), attach("/out/tool.exe"), attach("/out"),
			{Say: "Could not."},
		}
		one := int64(1 << 20)
		r := telegram(map[string][]studiotest.ToolStep{"files": steps}, studio.ConnectorInput{FileSendMax: &one})
		ask(1, "files")
		answered(2) // the answer and the note about the files of the turn
		res := r.h.ResultsFor("attach_file")
		Expect(res).To(HaveLen(4))
		for _, x := range res {
			Expect(x.Err).To(HaveOccurred())
		}
		Expect(res[0].Err.Error()).To(ContainSubstring("not found"))
		Expect(res[1].Err.Error()).To(ContainSubstring("too large"))
		Expect(res[1].Err.Error()).To(ContainSubstring("1 MiB (limit set for this bot)"))
		Expect(res[2].Err.Error()).To(ContainSubstring("not sent"))
		Expect(res[3].Err.Error()).To(ContainSubstring("folder"))
		Expect(bot.sentFiles()).To(BeEmpty())
	})

	It("counts the files of a turn, those made and those attached", func() {
		var steps []studiotest.ToolStep
		for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
			steps = append(steps, write("/out/"+n+".txt", n))
		}
		telegram(map[string][]studiotest.ToolStep{
			"make": append(steps, studiotest.ToolStep{Say: "Made."}),
		}, studio.ConnectorInput{})
		ask(1, "make")
		waitFor("five files", func() bool { return len(bot.sentFiles()) == 5 })
		answered(2)
		Expect(bot.answers()[1]).To(ContainSubstring("1 more file"))
	})

	It("limits the files that one answer attaches, in one stable order", func() {
		r := telegram(map[string][]studiotest.ToolStep{
			"make": {write("/out/a.txt", "a"), write("/out/b.txt", "b"), write("/out/c.txt", "c"), write("/out/d.txt", "d"),
				write("/out/e.txt", "e"), write("/out/f.txt", "f"), {Say: "Made."}},
			"all": {attach("/out/b.txt"), attach("/out/a.txt"), attach("/out/c.txt"), attach("/out/d.txt"), attach("/out/e.txt"), attach("/out/f.txt"), {Say: "Sent."}},
		}, studio.ConnectorInput{})
		ask(1, "make")
		waitFor("the first five", func() bool { return len(bot.sentFiles()) == 5 })
		answered(2)
		ask(2, "all")
		waitFor("five more", func() bool { return len(bot.sentFiles()) == 10 })
		res := r.h.ResultsFor("attach_file")
		Expect(res).To(HaveLen(6))
		Expect(res[5].Err).To(HaveOccurred())
		Expect(res[5].Err.Error()).To(ContainSubstring("5 files"))
		var names []string
		for _, f := range bot.sentFiles()[5:] {
			names = append(names, f.Name)
		}
		Expect(names).To(Equal([]string{"b.txt", "a.txt", "c.txt", "d.txt", "e.txt"}), "the order of the calls")
	})

	It("names a file that the platform refused, with its reason", func() {
		bot.setFail("sendDocument", func() (int, string) {
			return http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: wrong file identifier"}`
		})
		telegram(map[string][]studiotest.ToolStep{
			"make": {write("/out/report.csv", "a"), attach("/out/report.csv"), {Say: "Done."}},
		}, studio.ConnectorInput{})
		ask(1, "make")
		waitFor("the note", func() bool { return len(bot.answers()) == 2 })
		Expect(bot.answers()[1]).To(ContainSubstring("I could not send report.csv"))
		Expect(bot.answers()[1]).To(ContainSubstring("wrong file identifier"))
	})

	It("is offered only when the binding sends files", func() {
		script := map[string][]studiotest.ToolStep{}
		r := telegram(script, studio.ConnectorInput{})
		ask(1, "hi")
		answered(1)
		chats, _ := r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(HaveLen(1))
		Expect(r.h.ToolNames(chats[0].ID)).To(ContainElement("attach_file"))

		sh2, bot2 := newShared(), newScriptedBot()
		r2 := newConnRig(sh2, connStore(), bot2, attachScript(script), nil)
		r2.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyOff, SendFiles: &filesOff})
		bot2.say(1, 11, "Ada", "ada", "hi")
		waitFor("the answer", func() bool { return len(bot2.answers()) >= 1 })
		chats, _ = r2.svc.List(context.Background(), alice, 10)
		Expect(chats).To(HaveLen(1))
		Expect(r2.h.ToolNames(chats[0].ID)).NotTo(ContainElement("attach_file"))
	})

	It("does not exist in a chat that a person opens in Studio", func() {
		r := newConnRig(sh, connStore(), bot, attachScript(nil), nil)
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "hi")
		idle(r.svc, alice, id)
		Expect(r.h.ToolNames(id)).NotTo(ContainElement("attach_file"))
	})

	Describe("with Slack", func() {
		It("sends a file that an earlier turn made through the same send_file op", func() {
			f := slacktest.New()
			DeferCleanup(f.Close)
			f.AddApp(slacktest.App{Bot: slackBot, AppToken: slackApp, UserID: "UBOT"})
			f.AddUser("U1", "ada", "Ada")
			rt := slack.New(&slack.API{BaseURL: f.URL, FilesHost: f.FilesHost})
			DeferCleanup(rt.Close)
			script := attachScript(map[string][]studiotest.ToolStep{
				"make":  {write("/out/notes.md", "# notes"), {Say: "Made."}},
				"again": {attach("/out/notes.md"), {Say: "Here."}},
			})
			r := newConnRigWith(newShared(), connStore(), studiobot.NewTransport(studiobot.Slack(rt)), script, nil)
			r.bind(studio.ConnectorInput{Kind: "slack", Token: slackBot + " " + slackApp, Allow: []string{"U1"}})
			waitFor("the Socket Mode link", func() bool { return f.Connected(slackApp) })
			f.Push(slackApp, slacktest.DM("D1", "U1", "make"))
			waitFor("the first upload", func() bool { return len(f.Uploads(slackBot)) == 1 })
			f.Push(slackApp, slacktest.DM("D1", "U1", "again"))
			waitFor("the second upload", func() bool { return len(f.Uploads(slackBot)) == 2 })
			up := f.Uploads(slackBot)[1]
			Expect([]string{up.Name, up.Channel, string(up.Data)}).To(Equal([]string{"notes.md", "D1", "# notes"}))
		})
	})

	Describe("with a scoped agent", func() {
		It("attaches only inside the agent's folder", func() {
			ctx := context.Background()
			Expect(sh.files.EnsureHome(ctx, alice)).To(Succeed())
			home := session.HomePath(alice)
			for _, d := range []string{"/Projects", "/Projects/Acme", "/Secret"} {
				_, err := sh.files.Store().Mkdir(ctx, sh.files.Actor(alice), home+d)
				Expect(err).NotTo(HaveOccurred())
			}
			_, err := sh.files.Store().Put(ctx, sh.files.Actor(alice), home+"/Secret/plan.csv", strings.NewReader("OUTSIDE-SECRET-MARKER"), workspace.PutOptions{Overwrite: true})
			Expect(err).NotTo(HaveOccurred())
			r := newConnRig(sh, connStore(), bot, attachScript(map[string][]studiotest.ToolStep{
				"steal": {attach("/Secret/plan.csv"), attach("/workspace/home/Secret/plan.csv"), attach("../Secret/plan.csv"), {Say: "No."}},
			}), nil)
			ag, err := r.svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "Support", Model: "m"},
				&studio.AgentFolderWrite{Path: "/Projects/Acme", Mode: agents.FolderWrite})
			Expect(err).NotTo(HaveOccurred())
			r.agent = ag
			r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyOff})
			ask(1, "steal")
			answered(1)
			time.Sleep(300 * time.Millisecond)
			res := r.h.ResultsFor("attach_file")
			Expect(res).To(HaveLen(3))
			for _, x := range res {
				Expect(x.Err).To(HaveOccurred())
				Expect(x.Err.Error()).NotTo(ContainSubstring("OUTSIDE-SECRET-MARKER"))
			}
			Expect(bot.sentFiles()).To(BeEmpty())
		})
	})
})
