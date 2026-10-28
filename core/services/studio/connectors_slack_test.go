package studio_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	slack "github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/tests/studiobot"
)

// The Slack add-on through the connector manager: what core does with a
// thread, a mention, a listed channel and a file. Slack is the fake of
// slacktest; the add-on runtime and the chats are the real ones.

const (
	slackBot = "xoxb-core-bot"
	slackApp = "xapp-core-app"
)

type slackRig struct {
	*connRig
	slack *slacktest.Server
}

func newSlackRig(t *testing.T, answer string, allow []string) (*slackRig, connectors.Binding) {
	t.Helper()
	f := slacktest.New()
	t.Cleanup(f.Close)
	f.AddApp(slacktest.App{Bot: slackBot, AppToken: slackApp, UserID: "UBOT"})
	f.AddUser("U1", "ada", "Ada")
	f.AddUser("U2", "bo", "Bo")
	rt := slack.New(&slack.API{BaseURL: f.URL, FilesHost: f.FilesHost})
	t.Cleanup(rt.Close)
	r := newConnRigWith(t, newShared(t), connStore(t), studiobot.NewTransport(studiobot.Slack(rt)), connSay(answer), nil)
	b := r.bind(t, studio.ConnectorInput{Kind: "slack", Token: slackBot + " " + slackApp, Allow: allow})
	waitFor(t, "the Socket Mode link", func() bool { return f.Connected(slackApp) })
	return &slackRig{connRig: r, slack: f}, b
}

func (r *slackRig) answers() []slacktest.Post { return r.slack.Posts(slackBot) }

func TestSlackThreadIsOneChatForEveryoneInIt(t *testing.T) {
	r, b := newSlackRig(t, "Thread answer.", []string{"C1"})
	mention := slacktest.Mention("C1", "U1", "UBOT", "help us", "")
	r.slack.Push(slackApp, mention)
	waitFor(t, "the answer in the thread", func() bool { return len(r.answers()) == 1 })
	thread := mention["ts"].(string)
	if p := r.answers()[0]; p.Channel != "C1" || p.Thread != thread || p.Text != "Thread answer." {
		t.Fatalf("answer = %+v", p)
	}
	// Bo answers in the thread without a mention: the channel is listed, and
	// the thread goes on in the same chat.
	r.slack.Push(slackApp, slacktest.Reply("C1", "U2", "and me?", thread))
	waitFor(t, "the second answer", func() bool { return len(r.answers()) == 2 })
	chats, _ := r.svc.List(context.Background(), alice, 10)
	if len(chats) != 1 || chats[0].OriginID != b.ID || !strings.HasPrefix(chats[0].Title, "Slack · ") {
		t.Fatalf("chats = %+v", chats)
	}
	senders, _ := r.conns.ConnectorSenders(context.Background(), alice, b.ID)
	if len(senders) != 2 {
		t.Fatalf("senders = %+v", senders)
	}
}

func TestSlackListedUserIsAnsweredEverywhereAndAStrangerOnlyInAListedChannel(t *testing.T) {
	r, b := newSlackRig(t, "ok", []string{"U1", "C1"})
	r.slack.Push(slackApp, slacktest.DM("D2", "U2", "let me in"))
	r.slack.Push(slackApp, slacktest.Mention("C9", "U2", "UBOT", "in another channel", ""))
	r.slack.Push(slackApp, slacktest.DM("D1", "U1", "hi"))
	waitFor(t, "the answer to the listed user", func() bool { return len(r.answers()) == 1 })
	if p := r.answers()[0]; p.Channel != "D1" || p.Thread != "" {
		t.Fatalf("answer = %+v", p)
	}
	r.slack.Push(slackApp, slacktest.Mention("C1", "U2", "UBOT", "in the listed channel", ""))
	waitFor(t, "the answer in the listed channel", func() bool { return len(r.answers()) == 2 })
	time.Sleep(200 * time.Millisecond)
	for _, p := range r.answers() {
		if p.Channel == "D2" || p.Channel == "C9" {
			t.Fatalf("a stranger was answered: %+v", p)
		}
	}
	senders, _ := r.conns.ConnectorSenders(context.Background(), alice, b.ID)
	if len(senders) != 2 {
		t.Fatalf("senders = %+v", senders)
	}
}

func TestSlackFileReachesTheChatAsAnUpload(t *testing.T) {
	r, _ := newSlackRig(t, "Got it.", []string{"U1"})
	r.slack.AddFile("F1", "notes.txt", "text/plain", []byte("the plan"))
	ev := slacktest.DM("D1", "U1", "read this")
	ev["files"] = []any{map[string]any{"id": "F1"}, map[string]any{"id": "F404"}}
	r.slack.Push(slackApp, ev)
	waitFor(t, "the answer", func() bool {
		for _, p := range r.answers() {
			if p.Text == "Got it." {
				return true
			}
		}
		return false
	})
	texts := []string{}
	for _, p := range r.answers() {
		texts = append(texts, p.Text)
	}
	if !strings.Contains(strings.Join(texts, "|"), "could not read the file") {
		t.Fatalf("the missing file was not reported: %q", texts)
	}
	chats, _ := r.svc.List(context.Background(), alice, 10)
	if len(chats) != 1 {
		t.Fatalf("chats = %+v", chats)
	}
	msgs, err := r.svc.Messages(context.Background(), alice, chats[0].ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		found = found || (m.Role == "user" && strings.Contains(m.Text, "notes.txt"))
	}
	if !found {
		t.Fatalf("the file is not in the message of the chat: %+v", msgs)
	}
}

func TestSlackRefusedTokenSwitchesTheBindingOff(t *testing.T) {
	r, b := newSlackRig(t, "ok", []string{"U1"})
	r.slack.Refuse(slackBot, "token_revoked")
	r.slack.Disconnect(slackApp, "refresh_requested")
	waitFor(t, "the binding switched off", func() bool {
		got, err := r.conns.GetConnector(context.Background(), alice, b.ID)
		return err == nil && !got.Enabled
	})
}
