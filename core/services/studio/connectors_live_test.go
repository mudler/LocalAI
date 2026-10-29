package studio_test

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// What a sender sees while a turn runs (the reply modes), the files a turn
// sends, the conversation modes and the token check on save. The platform is
// the scripted Bot API; the Telegram add-on runtime and the chats are real.

var filesOff = false

// studiobotFor is the Telegram add-on over the scripted bot.
func studiobotFor(bot *scriptedBot) *studiobot.BotTransport {
	return studiobot.NewBotTransport(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
}

// setFail forces the answer of a method; nil clears it.
func (b *scriptedBot) setFail(method string, f func() (int, string)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if f == nil {
		delete(b.fail, method)
		return
	}
	b.fail[method] = f
}

func (b *scriptedBot) editsOf(id int64) []botEdit {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []botEdit
	for _, e := range b.edits {
		if e.ID == id {
			out = append(out, e)
		}
	}
	return out
}

func (b *scriptedBot) firstID() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sentIDs) == 0 {
		return 0
	}
	return b.sentIDs[0]
}

func (b *scriptedBot) sentFiles() []botFile {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]botFile(nil), b.files...)
}

func (b *scriptedBot) draftsSent() []botDraft {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]botDraft(nil), b.drafts...)
}

// lastDraft is the text of the latest draft, or "".
func (b *scriptedBot) lastDraft() string {
	d := b.draftsSent()
	if len(d) == 0 {
		return ""
	}
	return d[len(d)-1].Text
}

func (b *scriptedBot) parseModes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.modes...)
}

func (b *scriptedBot) editCount() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.edits) }

func (b *scriptedBot) typingCount() int { b.mu.Lock(); defer b.mu.Unlock(); return b.typing }

// lastEdit is the text the message shows after its edits, or "".
func (b *scriptedBot) lastEdit(id int64) string {
	e := b.editsOf(id)
	if len(e) == 0 {
		return ""
	}
	return e[len(e)-1].Text
}

// mention queues a group message that names the bot.
func (b *scriptedBot) mention(id int, chat, from int64, name, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updates = append(b.updates, map[string]any{"update_id": id, "message": map[string]any{
		"message_id": 1000 + id, "chat": map[string]any{"id": chat, "type": "group"}, "from": map[string]any{"id": from, "first_name": name},
		"text": "@helper_bot " + text, "entities": []any{map[string]any{"type": "mention", "offset": 0, "length": 11}}}})
}

// sayWithID queues a private message with its ID, as a reply when replyTo is set.
func (b *scriptedBot) sayWithID(id int, from int64, text string, replyTo int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := map[string]any{"message_id": 1000 + id, "chat": map[string]any{"id": from, "type": "private"}, "from": map[string]any{"id": from, "first_name": "Ada"}, "text": text}
	if replyTo != 0 {
		m["reply_to_message"] = map[string]any{"message_id": replyTo, "from": map[string]any{"id": 777, "is_bot": true}}
	}
	b.updates = append(b.updates, map[string]any{"update_id": id, "message": m})
}

func toolThenWait(release <-chan struct{}, answer string, tools ...string) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(workspace.Actor, string) []studiotest.ToolStep {
		var steps []studiotest.ToolStep
		for i, t := range tools {
			steps = append(steps, studiotest.ToolStep{Tool: t, Args: `{"path":"/notes/p` + string(rune('a'+i)) + `.md","content":"x"}`})
		}
		return append(steps, studiotest.ToolStep{Block: release}, studiotest.ToolStep{Say: answer})
	}
}

var _ = Describe("connectors live", func() {
	It("progress shows the tools in one working message that the answer replaces", func() {
		sh, bot := newShared(), newScriptedBot()
		release := make(chan struct{})
		r := newConnRig(sh, connStore(), bot, toolThenWait(release, "All done.", "write_file"), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyProgress, SendFiles: &filesOff})
		bot.say(1, 11, "Ada", "ada", "do it")
		waitFor("the working message", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(Equal("Working…"))
		id := bot.firstID()
		waitFor("the tool in the working message", func() bool { return strings.Contains(bot.lastEdit(id), "• write_file") })
		Expect(bot.typingCount()).To(BeNumerically(">=", 1), "typing shows at once")
		close(release)
		waitFor("the answer in place of the working message", func() bool { return bot.lastEdit(id) == "All done." })
		time.Sleep(100 * time.Millisecond)
		Expect(bot.answers()).To(Equal([]string{"Working…"}), "the answer replaced the working message: no second message")
	})

	It("stream in a group shows the answer as it is written in a working message that the answer replaces", func() {
		sh, bot := newShared(), newScriptedBot()
		release := make(chan struct{})
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "The first half"}, {Block: release}, {Say: " and the **second** half."}}
		}
		r := newConnRig(sh, connStore(), bot, script, nil)
		r.bind(studio.ConnectorInput{Allow: []string{"-600"}, Reply: connectors.ReplyStream})
		bot.mention(1, -600, 21, "Ann", "write")
		waitFor("the working message", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(Equal("Working… ▉"), "the working message is a preview with a cursor")
		id := bot.firstID()
		waitFor("a preview of the answer", func() bool { return bot.lastEdit(id) == "The first half ▉" })
		Expect(bot.editsOf(id)[0].Mode).To(BeEmpty(), "a preview is plain text")
		close(release)
		waitFor("the whole answer", func() bool { return strings.Contains(bot.lastEdit(id), "second") })
		e := bot.editsOf(id)
		Expect(e[len(e)-1].Text).To(Equal("The first half and the *second* half."))
		Expect(e[len(e)-1].Mode).To(Equal("MarkdownV2"), "the answer is MarkdownV2")
		Expect(bot.answers()).To(HaveLen(1))
		Expect(bot.draftsSent()).To(BeEmpty(), "a group has no drafts")
	})

	It("stream in a private chat shows the reasoning and then the answer in one draft, and sends the answer after it", func() {
		sh, bot := newShared(), newScriptedBot()
		thought, half := make(chan struct{}), make(chan struct{})
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Think: "checking "}, {Think: "the notes"}, {Block: thought},
				{Say: "The first half"}, {Block: half}, {Say: " and the **second** half."}}
		}
		r := newConnRig(sh, connStore(), bot, script, nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyStream})
		bot.say(1, 11, "Ada", "ada", "write")
		waitFor("the reasoning in the draft", func() bool { return bot.lastDraft() == "checking the notes ▉" })
		Expect(bot.draftsSent()[0].Text).To(Equal("Working… ▉"), "the first draft shows that the bot works")
		Expect(bot.answers()).To(BeEmpty(), "no working message")
		close(thought)
		waitFor("the answer in the draft", func() bool { return bot.lastDraft() == "The first half ▉" })
		close(half)
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(Equal("The first half and the *second* half."))
		Expect(bot.parseModes()).To(Equal([]string{"MarkdownV2"}))
		Expect(bot.editCount()).To(BeZero(), "a draft is never edited")
		drafts := bot.draftsSent()
		for _, d := range drafts {
			Expect(d.ID).NotTo(BeZero())
			Expect(d.ID).To(Equal(drafts[0].ID), "one draft for the whole answer")
			Expect(d.Chat).To(Equal(int64(11)))
		}
	})

	It("a draft the platform refuses gives way to a working message for the rest of the turn", func() {
		sh, bot := newShared(), newScriptedBot()
		bot.setFail("sendMessageDraft", func() (int, string) {
			return http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: drafts are not available"}`
		})
		release := make(chan struct{})
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "Half"}, {Block: release}, {Say: " done."}}
		}
		r := newConnRig(sh, connStore(), bot, script, nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyStream})
		bot.say(1, 11, "Ada", "ada", "go")
		waitFor("the working message", func() bool { return len(bot.answers()) == 1 })
		id := bot.firstID()
		waitFor("the preview in the working message", func() bool { return bot.lastEdit(id) == "Half ▉" })
		close(release)
		waitFor("the answer in place of the working message", func() bool { return bot.lastEdit(id) == "Half done." })
		Expect(bot.answers()).To(HaveLen(1))
	})

	It("sends a draft that shows nothing new again after the heartbeat, and stops with the turn", func() {
		sh, bot := newShared(), newScriptedBot()
		release := make(chan struct{})
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "Waiting"}, {Block: release}, {Say: " done."}}
		}
		r := newConnRigOpts(sh, connStore(), studiobotFor(bot), script, nil, func(o *studio.ConnectorOptions) { o.DraftHeartbeat = 150 * time.Millisecond })
		r.bot = bot
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyStream})
		bot.say(1, 11, "Ada", "ada", "go")
		waitFor("the draft sent again", func() bool {
			n := 0
			for _, d := range bot.draftsSent() {
				if d.Text == "Waiting ▉" {
					n++
				}
			}
			return n >= 3
		})
		close(release)
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		n := len(bot.draftsSent())
		time.Sleep(400 * time.Millisecond)
		Expect(bot.draftsSent()).To(HaveLen(n), "no heartbeat after the answer")
	})

	It("coalesces previews to the interval of the kind", func() {
		sh, bot := newShared(), newScriptedBot()
		gates := make([]chan struct{}, 30)
		var steps []studiotest.ToolStep
		for i := range gates {
			gates[i] = make(chan struct{})
			steps = append(steps, studiotest.ToolStep{Say: "w "}, studiotest.ToolStep{Block: gates[i]})
		}
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return append(steps, studiotest.ToolStep{Say: "end"})
		}
		tr := studiobot.NewTransport(studiobot.TelegramPaced(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()}))
		r := newConnRigOpts(sh, connStore(), tr, script, nil, nil)
		r.bot = bot
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyStream})
		bot.say(1, 11, "Ada", "ada", "go")
		waitFor("the first draft", func() bool { return len(bot.draftsSent()) == 1 })
		for _, g := range gates {
			close(g)
			time.Sleep(50 * time.Millisecond)
		}
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		drafts := bot.draftsSent()
		Expect(len(drafts)).To(BeNumerically("<=", 6), "30 changes in 1.5 s make few drafts")
		for i := 1; i < len(drafts); i++ {
			Expect(drafts[i].At.Sub(drafts[i-1].At)).To(BeNumerically(">=", 370*time.Millisecond))
		}
	})

	It("a preview shows the end of a long answer, and the answer goes whole", func() {
		sh, bot := newShared(), newScriptedBot()
		release := make(chan struct{})
		long := strings.Repeat("é", 3100)
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "start" + long}, {Block: release}}
		}
		r := newConnRig(sh, connStore(), bot, script, nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyStream})
		bot.say(1, 11, "Ada", "ada", "go")
		waitFor("a preview of the answer", func() bool { return strings.HasPrefix(bot.lastDraft(), "éé") })
		d := bot.lastDraft()
		Expect(utf8.RuneCountInString(d)).To(Equal(3000))
		Expect(d).To(HaveSuffix("é ▉"))
		close(release)
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(Equal("start" + long))
	})

	It("an edit that fails sends the answer as a new message", func() {
		sh, bot := newShared(), newScriptedBot()
		bot.setFail("editMessageText", func() (int, string) {
			return http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`
		})
		release := make(chan struct{})
		r := newConnRig(sh, connStore(), bot, toolThenWait(release, "Still here.", "write_file"), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyProgress, SendFiles: &filesOff})
		bot.say(1, 11, "Ada", "ada", "go")
		waitFor("the working message", func() bool { return len(bot.answers()) == 1 })
		close(release)
		waitFor("the answer", func() bool { return len(bot.answers()) == 2 })
		Expect(bot.answers()).To(Equal([]string{"Working…", "Still here."}))
	})

	It("typing shows at once for a short turn", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("Quick."), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyOff})
		bot.say(1, 11, "Ada", "ada", "hi")
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.typingCount()).To(BeNumerically(">=", 1), "a turn shorter than the typing period shows typing")
		Expect(bot.answers()).To(Equal([]string{"Quick."}), "the off mode sends no working message")
	})

	It("the edits of a binding are spaced out", func() {
		sh, bot := newShared(), newScriptedBot()
		release := make(chan struct{})
		every := 300 * time.Millisecond
		tools := []string{"write_file", "read_file", "write_file", "read_file", "write_file", "read_file"}
		r := newConnRigOpts(sh, connStore(), studiobotFor(bot), toolThenWait(release, "ok", tools...), nil, func(o *studio.ConnectorOptions) { o.EditEvery = every })
		r.bot = bot
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyProgress, SendFiles: &filesOff})
		bot.say(1, 11, "Ada", "ada", "busy")
		waitFor("the working message", func() bool { return len(bot.answers()) == 1 })
		id := bot.firstID()
		time.Sleep(1200 * time.Millisecond)
		edits := bot.editsOf(id)
		Expect(len(edits)).To(BeNumerically("<=", 5), "edits are coalesced")
		for i := 1; i < len(edits); i++ {
			Expect(edits[i].At.Sub(edits[i-1].At)).To(BeNumerically(">=", every-30*time.Millisecond))
		}
		Expect(bot.lastEdit(id)).To(ContainSubstring("read_file"), "the last edit shows the latest tools")
		close(release)
		waitFor("the answer", func() bool { return bot.lastEdit(id) == "ok" })
	})

	It("the files of a turn follow the answer", func() {
		sh, bot := newShared(), newScriptedBot()
		big := strings.Repeat("x", 1<<20+10)
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{
				{Tool: "write_file", Args: `{"path":"/out/report.csv","content":"a,b\n1,2\n"}`},
				{Tool: "write_file", Args: `{"path":"/out/tool.exe","content":"MZ"}`},
				{Tool: "write_file", Args: `{"path":"/out/big.csv","content":"` + big + `"}`},
				{Say: "Here are the files."},
			}
		}
		r := newConnRig(sh, connStore(), bot, script, nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyOff})
		bot.say(1, 11, "Ada", "ada", "make files")
		waitFor("the file", func() bool { return len(bot.sentFiles()) == 1 })
		waitFor("the note", func() bool { return len(bot.answers()) == 2 })
		f := bot.sentFiles()[0]
		Expect([]string{f.Method, f.Field, f.Name, string(f.Data)}).To(Equal([]string{"sendDocument", "document", "report.csv", "a,b\n1,2\n"}))
		got := bot.answers()
		Expect(got[0]).To(Equal("Here are the files."), "the answer comes first")
		Expect(got[1]).To(ContainSubstring("I made tool.exe, but files of this type are not sent here."))
		Expect(got[1]).To(ContainSubstring("big.csv is too large to send here"))
	})

	It("a binding with files off sends none", func() {
		sh, bot := newShared(), newScriptedBot()
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "write_file", Args: `{"path":"/out/report.csv","content":"a"}`}, {Say: "Done."}}
		}
		r := newConnRig(sh, connStore(), bot, script, nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyOff, SendFiles: &filesOff})
		bot.say(1, 11, "Ada", "ada", "make a file")
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		time.Sleep(200 * time.Millisecond)
		Expect(bot.sentFiles()).To(BeEmpty())
		Expect(bot.answers()).To(HaveLen(1))
	})

	It("the chat mode gives a group one chat", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"-600"}, Conversation: connectors.ConversationChat})
		bot.mention(1, -600, 21, "Ann", "first")
		waitFor("the first answer", func() bool { return len(bot.answers()) == 1 })
		bot.mention(2, -600, 22, "Ben", "second")
		waitFor("the second answer", func() bool { return len(bot.answers()) == 2 })
		chats, _ := r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(HaveLen(1), "both people of the group share one chat")
		Expect(r.h.Sent(chats[0].ID)).To(Equal([]string{"first", "second"}), "the mention is taken out of the text")
	})

	It("the sender mode gives each person of a group their own chat", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"-600"}})
		bot.mention(1, -600, 21, "Ann", "first")
		waitFor("the first answer", func() bool { return len(bot.answers()) == 1 })
		bot.mention(2, -600, 22, "Ben", "second")
		waitFor("the second answer", func() bool { return len(bot.answers()) == 2 })
		chats, _ := r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(HaveLen(2))
	})

	It("the window mode starts a new chat after a quiet time", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Conversation: connectors.ConversationWindow, WindowMinutes: 30})
		bot.say(1, 11, "Ada", "ada", "one")
		waitFor("the first answer", func() bool { return len(bot.answers()) == 1 })
		sh.clock.Advance(10 * time.Minute)
		bot.say(2, 11, "Ada", "ada", "two")
		waitFor("the second answer", func() bool { return len(bot.answers()) == 2 })
		chats, _ := r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(HaveLen(1), "inside the window the chat goes on")
		sh.clock.Advance(31 * time.Minute)
		bot.say(3, 11, "Ada", "ada", "three")
		waitFor("the third answer", func() bool { return len(bot.answers()) == 3 })
		chats, _ = r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(HaveLen(2), "after the window a new chat starts")
	})

	It("the thread mode starts a chat per message and follows replies", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("ok"), nil)
		r.bind(studio.ConnectorInput{Allow: []string{"11"}, Conversation: connectors.ConversationThread})
		bot.sayWithID(1, 11, "question one", 0)
		waitFor("the first answer", func() bool { return len(bot.answers()) == 1 })
		answerOne := bot.firstID()
		bot.sayWithID(2, 11, "question two", 0)
		waitFor("the second answer", func() bool { return len(bot.answers()) == 2 })
		bot.sayWithID(3, 11, "more on one", answerOne)
		waitFor("the third answer", func() bool { return len(bot.answers()) == 3 })
		chats, _ := r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(HaveLen(2))
		sent := map[string]bool{}
		for _, c := range chats {
			sent[strings.Join(r.h.Sent(c.ID), "|")] = true
		}
		Expect(sent).To(HaveKey("question one|more on one"), "the reply to the first answer goes on in its chat")
		Expect(sent).To(HaveKey("question two"))
	})

	It("a saved token is checked with the platform", func() {
		sh, bot := newShared(), newScriptedBot()
		tr := studiobotFor(bot)
		r := newConnRigOpts(sh, connStore(), tr, connSay("ok"), nil, func(o *studio.ConnectorOptions) { o.VerifyToken = studio.VerifyTokenWithMe(tr) })
		r.bot = bot
		b := r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		ctx := context.Background()
		in := studio.ConnectorInput{Name: "Support bot", AgentID: r.agent.ID, Token: "777:other", Enabled: true}
		bot.setFail("getMe", func() (int, string) {
			return http.StatusUnauthorized, `{"ok":false,"error_code":401,"description":"Unauthorized"}`
		})
		_, err := r.conns.CreateConnector(ctx, alice, in)
		Expect(err).To(MatchError(connectors.ErrInvalid))
		Expect(err).To(MatchError(studio.ErrTokenRefused))
		_, err = r.conns.UpdateConnector(ctx, alice, b.ID, in)
		Expect(err).To(MatchError(studio.ErrTokenRefused), "a new token on an update is checked too")
		in.Token = ""
		_, err = r.conns.UpdateConnector(ctx, alice, b.ID, in)
		Expect(err).NotTo(HaveOccurred(), "an update that keeps the token does not ask the platform")
		// A binding whose add-on does not run is saved; it is checked when it runs.
		tr.SetRunning(false)
		in.Token = "777:other"
		_, err = r.conns.CreateConnector(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
		tr.SetRunning(true)
		bot.setFail("getMe", nil)
		info, err := r.conns.CreateConnector(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
		Expect([]any{info.ReplyMode(), info.ConversationMode(), info.SendsFiles()}).To(Equal([]any{connectors.ReplyProgress, connectors.ConversationSender, true}), "the defaults")
	})
})
