package studio

import (
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func TestStandingOf(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	c := &Connectors{o: ConnectorOptions{Clock: func() time.Time { return now }}}
	b := connectors.Binding{Allow: []string{"U1", "@bo", "C9"}, Paired: []connectors.PairedSender{{SenderID: "U0", At: now}}}
	cases := []struct {
		name string
		m    contract.ConnectorMessage
		want string
	}{
		{"paired sender", contract.ConnectorMessage{SenderID: "U0", Chat: "C1"}, session.StandingOwner},
		{"role holder", contract.ConnectorMessage{SenderID: "U7", Chat: "C1", Roles: []string{"admin"}}, session.StandingVerified},
		{"listed by id", contract.ConnectorMessage{SenderID: "U1", Chat: "C1"}, session.StandingListed},
		{"listed by handle", contract.ConnectorMessage{SenderID: "U2", SenderUsername: "BO", Chat: "C1"}, session.StandingListed},
		{"listed channel", contract.ConnectorMessage{SenderID: "U3", Chat: "C9"}, session.StandingChannel},
		{"channel entry does not count in a direct chat", contract.ConnectorMessage{SenderID: "U3", Chat: "C9", Direct: true}, session.StandingAnyone},
		{"anyone", contract.ConnectorMessage{SenderID: "U4", Chat: "C1"}, session.StandingAnyone},
	}
	for _, tc := range cases {
		if got := c.standingOf(b, tc.m); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSafeLink(t *testing.T) {
	for link, want := range map[string]string{
		"https://chat.example.test/archives/C1/p5": "https://chat.example.test/archives/C1/p5",
		"http://chat.example.test/x":               "",
		"javascript:alert(1)":                      "",
		"https://user:pw@chat.example.test/x":      "",
		"https://chat.example.test/a b":            "",
		"":                                         "",
	} {
		if got := safeLink(link); got != want {
			t.Errorf("safeLink(%q) = %q, want %q", link, got, want)
		}
	}
}

func TestMergeSender(t *testing.T) {
	a, b := &session.Sender{ID: "1", Name: "A"}, &session.Sender{ID: "2", Name: "B"}
	if got := mergeSender([]*session.Sender{a, {ID: "1", Name: "A"}}); got == nil || got.ID != "1" {
		t.Errorf("the same sender twice must stay named, got %v", got)
	}
	if got := mergeSender([]*session.Sender{a, b}); got != nil {
		t.Errorf("two senders must name none, got %v", got)
	}
	if got := mergeSender([]*session.Sender{a, nil}); got != nil {
		t.Errorf("an unknown sender must name none, got %v", got)
	}
}

func TestJoinPendingKeepsTheSenderOnlyWhenItIsOne(t *testing.T) {
	ada, bo := &session.Sender{ID: "1", Name: "Ada"}, &session.Sender{ID: "2", Name: "Bo"}
	waiting := func(text string, s *session.Sender) store.Pending { return store.Pending{Text: text, Sender: s} }

	same := joinPending([]store.Pending{waiting("one", ada)}, session.Message{Text: "two", Sender: &session.Sender{ID: "1", Name: "Ada"}})
	if same.Text != "one\n\ntwo" || same.Sender == nil || same.Sender.ID != "1" {
		t.Errorf("one sender must stay named: %q %+v", same.Text, same.Sender)
	}
	mixed := joinPending([]store.Pending{waiting("one", ada)}, session.Message{Text: "two", Sender: bo})
	if mixed.Sender != nil {
		t.Errorf("words of two people must name none: %+v", mixed.Sender)
	}
	// A turn that starts from waiting messages only (no new message) keeps their sender.
	only := joinPending([]store.Pending{waiting("one", ada), waiting("again", ada)}, session.Message{})
	if only.Sender == nil || only.Sender.ID != "1" {
		t.Errorf("waiting messages of one sender must stay named: %+v", only.Sender)
	}
	// A typed message with no sender joined to a person's is not theirs alone.
	typed := joinPending([]store.Pending{waiting("one", ada)}, session.Message{Text: "typed"})
	if typed.Sender != nil {
		t.Errorf("an unknown sender must not borrow a name: %+v", typed.Sender)
	}
}
