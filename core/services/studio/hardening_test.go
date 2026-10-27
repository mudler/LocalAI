package studio_test

import (
	"context"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio"
)

// An administrator who switches the chats feature off for a user stops what
// that user's bots do by themselves, not only their page and API.
func TestABotStopsAnsweringWhenItsOwnerLosesTheChatsFeature(t *testing.T) {
	sh, bot := newShared(t), newScriptedBot(t)
	sh.users = func(string) error { return studio.ErrChatsOff }
	r := newConnRig(t, sh, connStore(t), bot, connSay("the agent ran"), nil)
	b := r.bind(t, studio.ConnectorInput{AcceptAny: true})
	bot.say(1, 1, "One", "", "hello")
	waitFor(t, "an answer", func() bool { return len(bot.answers()) == 1 })

	if got := bot.answers()[0]; got != "This bot is not available right now." {
		t.Fatalf("the bot answered %q, want the unavailable notice and no agent output", got)
	}
	if chats, _ := r.svc.List(context.Background(), alice, 10); len(chats) != 0 {
		t.Fatalf("the sender got %d chats of a user who lost the feature", len(chats))
	}
	if got, _ := r.conns.GetConnector(context.Background(), alice, b.ID); !got.Enabled {
		t.Fatal("the binding was switched off; an administrator can give the feature back")
	}
}
