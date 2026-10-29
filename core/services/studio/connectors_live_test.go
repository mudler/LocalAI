package studio_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/tests/studiobot"
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

func TestProgressShowsTheToolsInOneWorkingMessageThatTheAnswerReplaces(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	release := make(chan struct{})
	r := newConnRig(t, sh, connStore(t), bot, toolThenWait(release, "All done.", "write_file"), nil)
	r.bind(t, studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyProgress, SendFiles: &filesOff})
	bot.say(1, 11, "Ada", "ada", "do it")
	waitFor(t, "the working message", func() bool { return len(bot.answers()) == 1 })
	g.Expect(bot.answers()[0]).To(gomega.Equal("Working…"))
	id := bot.firstID()
	waitFor(t, "the tool in the working message", func() bool { return strings.Contains(bot.lastEdit(id), "• write_file") })
	g.Expect(bot.typingCount()).To(gomega.BeNumerically(">=", 1), "typing shows at once")
	close(release)
	waitFor(t, "the answer in place of the working message", func() bool { return bot.lastEdit(id) == "All done." })
	time.Sleep(100 * time.Millisecond)
	g.Expect(bot.answers()).To(gomega.Equal([]string{"Working…"}), "the answer replaced the working message: no second message")
}

func TestStreamShowsTheAnswerAsItIsWritten(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	release := make(chan struct{})
	script := func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Say: "The first half"}, {Block: release}, {Say: " and the second half."}}
	}
	r := newConnRig(t, sh, connStore(t), bot, script, nil)
	r.bind(t, studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyStream})
	bot.say(1, 11, "Ada", "ada", "write")
	waitFor(t, "the working message", func() bool { return len(bot.answers()) == 1 })
	id := bot.firstID()
	waitFor(t, "a preview of the answer", func() bool { return strings.HasPrefix(bot.lastEdit(id), "The first half") })
	g.Expect(bot.lastEdit(id)).To(gomega.HaveSuffix("▍"), "a preview ends with a cursor")
	close(release)
	waitFor(t, "the whole answer", func() bool { return strings.Contains(bot.lastEdit(id), "second half.") })
	g.Expect(bot.lastEdit(id)).NotTo(gomega.ContainSubstring("▍"), "the answer has no cursor")
	g.Expect(bot.answers()).To(gomega.HaveLen(1))
}

func TestAnEditThatFailsSendsTheAnswerAsANewMessage(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	bot.setFail("editMessageText", func() (int, string) {
		return http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`
	})
	release := make(chan struct{})
	r := newConnRig(t, sh, connStore(t), bot, toolThenWait(release, "Still here.", "write_file"), nil)
	r.bind(t, studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyProgress, SendFiles: &filesOff})
	bot.say(1, 11, "Ada", "ada", "go")
	waitFor(t, "the working message", func() bool { return len(bot.answers()) == 1 })
	close(release)
	waitFor(t, "the answer", func() bool { return len(bot.answers()) == 2 })
	g.Expect(bot.answers()).To(gomega.Equal([]string{"Working…", "Still here."}))
}

func TestTypingShowsAtOnceForAShortTurn(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay("Quick."), nil)
	r.bind(t, studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyOff})
	bot.say(1, 11, "Ada", "ada", "hi")
	waitFor(t, "the answer", func() bool { return len(bot.answers()) == 1 })
	g.Expect(bot.typingCount()).To(gomega.BeNumerically(">=", 1), "a turn shorter than the typing period shows typing")
	g.Expect(bot.answers()).To(gomega.Equal([]string{"Quick."}), "the off mode sends no working message")
}

func TestTheEditsOfABindingAreSpacedOut(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	release := make(chan struct{})
	every := 300 * time.Millisecond
	tools := []string{"write_file", "read_file", "write_file", "read_file", "write_file", "read_file"}
	r := newConnRigOpts(t, sh, connStore(t), studiobotFor(bot), toolThenWait(release, "ok", tools...), nil, func(o *studio.ConnectorOptions) { o.EditEvery = every })
	r.bot = bot
	r.bind(t, studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyProgress, SendFiles: &filesOff})
	bot.say(1, 11, "Ada", "ada", "busy")
	waitFor(t, "the working message", func() bool { return len(bot.answers()) == 1 })
	id := bot.firstID()
	time.Sleep(1200 * time.Millisecond)
	edits := bot.editsOf(id)
	g.Expect(len(edits)).To(gomega.BeNumerically("<=", 5), "edits are coalesced")
	for i := 1; i < len(edits); i++ {
		g.Expect(edits[i].At.Sub(edits[i-1].At)).To(gomega.BeNumerically(">=", every-30*time.Millisecond))
	}
	g.Expect(bot.lastEdit(id)).To(gomega.ContainSubstring("read_file"), "the last edit shows the latest tools")
	close(release)
	waitFor(t, "the answer", func() bool { return bot.lastEdit(id) == "ok" })
}

func TestTheFilesOfATurnFollowTheAnswer(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	big := strings.Repeat("x", 1<<20+10)
	script := func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{
			{Tool: "write_file", Args: `{"path":"/out/report.csv","content":"a,b\n1,2\n"}`},
			{Tool: "write_file", Args: `{"path":"/out/tool.exe","content":"MZ"}`},
			{Tool: "write_file", Args: `{"path":"/out/big.csv","content":"` + big + `"}`},
			{Say: "Here are the files."},
		}
	}
	r := newConnRig(t, sh, connStore(t), bot, script, nil)
	r.bind(t, studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyOff})
	bot.say(1, 11, "Ada", "ada", "make files")
	waitFor(t, "the file", func() bool { return len(bot.sentFiles()) == 1 })
	waitFor(t, "the note", func() bool { return len(bot.answers()) == 2 })
	f := bot.sentFiles()[0]
	g.Expect([]string{f.Method, f.Field, f.Name, string(f.Data)}).To(gomega.Equal([]string{"sendDocument", "document", "report.csv", "a,b\n1,2\n"}))
	got := bot.answers()
	g.Expect(got[0]).To(gomega.Equal("Here are the files."), "the answer comes first")
	g.Expect(got[1]).To(gomega.ContainSubstring("I made tool.exe, but files of this type are not sent here."))
	g.Expect(got[1]).To(gomega.ContainSubstring("big.csv is too large to send here"))
}

func TestABindingWithFilesOffSendsNone(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	script := func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Tool: "write_file", Args: `{"path":"/out/report.csv","content":"a"}`}, {Say: "Done."}}
	}
	r := newConnRig(t, sh, connStore(t), bot, script, nil)
	r.bind(t, studio.ConnectorInput{Allow: []string{"11"}, Reply: connectors.ReplyOff, SendFiles: &filesOff})
	bot.say(1, 11, "Ada", "ada", "make a file")
	waitFor(t, "the answer", func() bool { return len(bot.answers()) == 1 })
	time.Sleep(200 * time.Millisecond)
	g.Expect(bot.sentFiles()).To(gomega.BeEmpty())
	g.Expect(bot.answers()).To(gomega.HaveLen(1))
}

func TestTheChatModeGivesAGroupOneChat(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay("ok"), nil)
	r.bind(t, studio.ConnectorInput{Allow: []string{"-600"}, Conversation: connectors.ConversationChat})
	bot.mention(1, -600, 21, "Ann", "first")
	waitFor(t, "the first answer", func() bool { return len(bot.answers()) == 1 })
	bot.mention(2, -600, 22, "Ben", "second")
	waitFor(t, "the second answer", func() bool { return len(bot.answers()) == 2 })
	chats, _ := r.svc.List(context.Background(), alice, 10)
	g.Expect(chats).To(gomega.HaveLen(1), "both people of the group share one chat")
	g.Expect(r.h.Sent(chats[0].ID)).To(gomega.Equal([]string{"first", "second"}), "the mention is taken out of the text")
}

func TestTheSenderModeGivesEachPersonOfAGroupTheirOwnChat(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay("ok"), nil)
	r.bind(t, studio.ConnectorInput{Allow: []string{"-600"}})
	bot.mention(1, -600, 21, "Ann", "first")
	waitFor(t, "the first answer", func() bool { return len(bot.answers()) == 1 })
	bot.mention(2, -600, 22, "Ben", "second")
	waitFor(t, "the second answer", func() bool { return len(bot.answers()) == 2 })
	chats, _ := r.svc.List(context.Background(), alice, 10)
	g.Expect(chats).To(gomega.HaveLen(2))
}

func TestTheWindowModeStartsANewChatAfterAQuietTime(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay("ok"), nil)
	r.bind(t, studio.ConnectorInput{Allow: []string{"11"}, Conversation: connectors.ConversationWindow, WindowMinutes: 30})
	bot.say(1, 11, "Ada", "ada", "one")
	waitFor(t, "the first answer", func() bool { return len(bot.answers()) == 1 })
	sh.clock.Advance(10 * time.Minute)
	bot.say(2, 11, "Ada", "ada", "two")
	waitFor(t, "the second answer", func() bool { return len(bot.answers()) == 2 })
	chats, _ := r.svc.List(context.Background(), alice, 10)
	g.Expect(chats).To(gomega.HaveLen(1), "inside the window the chat goes on")
	sh.clock.Advance(31 * time.Minute)
	bot.say(3, 11, "Ada", "ada", "three")
	waitFor(t, "the third answer", func() bool { return len(bot.answers()) == 3 })
	chats, _ = r.svc.List(context.Background(), alice, 10)
	g.Expect(chats).To(gomega.HaveLen(2), "after the window a new chat starts")
}

func TestTheThreadModeStartsAChatPerMessageAndFollowsReplies(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	r := newConnRig(t, sh, connStore(t), bot, connSay("ok"), nil)
	r.bind(t, studio.ConnectorInput{Allow: []string{"11"}, Conversation: connectors.ConversationThread})
	bot.sayWithID(1, 11, "question one", 0)
	waitFor(t, "the first answer", func() bool { return len(bot.answers()) == 1 })
	answerOne := bot.firstID()
	bot.sayWithID(2, 11, "question two", 0)
	waitFor(t, "the second answer", func() bool { return len(bot.answers()) == 2 })
	bot.sayWithID(3, 11, "more on one", answerOne)
	waitFor(t, "the third answer", func() bool { return len(bot.answers()) == 3 })
	chats, _ := r.svc.List(context.Background(), alice, 10)
	g.Expect(chats).To(gomega.HaveLen(2))
	sent := map[string]bool{}
	for _, c := range chats {
		sent[strings.Join(r.h.Sent(c.ID), "|")] = true
	}
	g.Expect(sent).To(gomega.HaveKey("question one|more on one"), "the reply to the first answer goes on in its chat")
	g.Expect(sent).To(gomega.HaveKey("question two"))
}

func TestASavedTokenIsCheckedWithThePlatform(t *testing.T) {
	g := gomega.NewWithT(t)
	sh, bot := newShared(t), newScriptedBot(t)
	tr := studiobotFor(bot)
	r := newConnRigOpts(t, sh, connStore(t), tr, connSay("ok"), nil, func(o *studio.ConnectorOptions) { o.VerifyToken = studio.VerifyTokenWithMe(tr) })
	r.bot = bot
	b := r.bind(t, studio.ConnectorInput{Allow: []string{"11"}})
	ctx := context.Background()
	in := studio.ConnectorInput{Name: "Support bot", AgentID: r.agent.ID, Token: "777:other", Enabled: true}
	bot.setFail("getMe", func() (int, string) {
		return http.StatusUnauthorized, `{"ok":false,"error_code":401,"description":"Unauthorized"}`
	})
	_, err := r.conns.CreateConnector(ctx, alice, in)
	g.Expect(err).To(gomega.MatchError(connectors.ErrInvalid))
	g.Expect(err).To(gomega.MatchError(studio.ErrTokenRefused))
	_, err = r.conns.UpdateConnector(ctx, alice, b.ID, in)
	g.Expect(err).To(gomega.MatchError(studio.ErrTokenRefused), "a new token on an update is checked too")
	in.Token = ""
	_, err = r.conns.UpdateConnector(ctx, alice, b.ID, in)
	g.Expect(err).NotTo(gomega.HaveOccurred(), "an update that keeps the token does not ask the platform")
	// A binding whose add-on does not run is saved; it is checked when it runs.
	tr.SetRunning(false)
	in.Token = "777:other"
	_, err = r.conns.CreateConnector(ctx, alice, in)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	tr.SetRunning(true)
	bot.setFail("getMe", nil)
	info, err := r.conns.CreateConnector(ctx, alice, in)
	g.Expect(err).NotTo(gomega.HaveOccurred())
	g.Expect([]any{info.ReplyMode(), info.ConversationMode(), info.SendsFiles()}).To(gomega.Equal([]any{connectors.ReplyProgress, connectors.ConversationSender, true}), "the defaults")
}
