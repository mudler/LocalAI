package notify_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/testutil"
)

func recv(t *testing.T, ch <-chan notify.Notification) notify.Notification {
	t.Helper()
	select {
	case n, ok := <-ch:
		if !ok {
			t.Fatal("the subscription closed")
		}
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("no notification arrived")
		return notify.Notification{}
	}
}

func none(t *testing.T, ch <-chan notify.Notification) {
	t.Helper()
	select {
	case n := <-ch:
		t.Fatalf("a notification arrived that should not have: %+v", n)
	case <-time.After(50 * time.Millisecond):
	}
}

func newService(t *testing.T, fan notify.Fanout, st notify.Store) *notify.Service {
	t.Helper()
	if st == nil {
		var err error
		if st, err = notify.NewJSON(""); err != nil {
			t.Fatal(err)
		}
	}
	return notify.NewService(notify.Options{Store: st, Fanout: fan, Clock: func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }})
}

func TestAddCutsTheTextAndStampsTheNotification(t *testing.T) {
	svc := newService(t, nil, nil)
	n, err := svc.Add(context.Background(), notify.Notification{Principal: "u1", Kind: notify.KindRunEnded, Status: notify.StatusOK,
		Title: strings.Repeat("é", 300), Body: strings.Repeat("b", 1000), Link: notify.ChatLink("c1"), Read: true})
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(n.Title)) != notify.MaxTitleRunes || len([]rune(n.Body)) != notify.MaxBodyRunes || n.Read || n.ID == 0 || n.Created.IsZero() {
		t.Fatalf("stored %+v", n)
	}
	got, _ := svc.List(context.Background(), "u1", false, 0)
	if len(got) != 1 || got[0].ID != n.ID {
		t.Fatalf("list = %+v", got)
	}
}

func TestALocalSubscriberHearsOnlyItsOwnerAndStopsAfterCancel(t *testing.T) {
	svc := newService(t, nil, nil)
	mine, cancel := svc.Subscribe("u1")
	other, cancelOther := svc.Subscribe("u2")
	defer cancelOther()
	_, _ = svc.Add(context.Background(), notify.Notification{Principal: "u1", Title: "hello"})
	if recv(t, mine).Title != "hello" {
		t.Fatal("wrong notification")
	}
	none(t, other)
	cancel()
	if _, ok := <-mine; ok {
		t.Fatal("the channel should be closed after cancel")
	}
	_, _ = svc.Add(context.Background(), notify.Notification{Principal: "u1", Title: "again"})
}

func TestANotificationAddedOnOneFrontendReachesTheClientsOfAnother(t *testing.T) {
	net := testutil.NewFakeBus()
	one := newService(t, notify.NewBroadcastFanout(net, nil), nil)
	two := newService(t, notify.NewBroadcastFanout(net, nil), nil)
	ch, cancel := two.Subscribe("u/1")
	defer cancel()
	other, cancelOther := two.Subscribe("u/2")
	defer cancelOther()
	if _, err := one.Add(context.Background(), notify.Notification{Principal: "u/1", Kind: notify.KindRunFailed, Status: notify.StatusFailed, Title: "Nightly failed", Link: "/app/chats/c9"}); err != nil {
		t.Fatal(err)
	}
	n := recv(t, ch)
	if n.Title != "Nightly failed" || n.Kind != notify.KindRunFailed || n.Link != "/app/chats/c9" || n.ID == 0 {
		t.Fatalf("after the trip = %+v", n)
	}
	none(t, other)
}

func TestMarkChatReadClearsTheNotificationsOfThatChat(t *testing.T) {
	svc := newService(t, nil, nil)
	ctx := context.Background()
	_, _ = svc.Add(ctx, notify.Notification{Principal: "u1", Title: "a", Link: notify.ChatLink("c1")})
	_, _ = svc.Add(ctx, notify.Notification{Principal: "u1", Title: "b", Link: notify.ChatLink("c2")})
	if err := svc.MarkChatRead(ctx, "u1", "c1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := svc.Count(ctx, "u1"); n != 1 {
		t.Fatalf("unread = %d", n)
	}
}

func TestAHookSeesEveryStoredNotification(t *testing.T) {
	svc := newService(t, nil, nil)
	var seen []string
	svc.OnAdd(func(n notify.Notification) { seen = append(seen, n.Title) })
	_, _ = svc.Add(context.Background(), notify.Notification{Principal: "u1", Title: "x"})
	if len(seen) != 1 || seen[0] != "x" {
		t.Fatalf("hook saw %v", seen)
	}
}

// The 90-day limit holds when a user reads, not only when a new notification
// arrives: a user who stops getting notifications must not keep old ones in view.
func TestOldNotificationsAreNotShownEvenWhenNothingNewArrives(t *testing.T) {
	st, err := notify.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	clock := now
	svc := notify.NewService(notify.Options{Store: st, Clock: func() time.Time { return clock }})
	ctx := context.Background()
	if _, err := svc.Add(ctx, notify.Notification{Principal: "u1", Kind: notify.KindRunEnded, Title: "old"}); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(notify.MaxAge / 2)
	if _, err := svc.Add(ctx, notify.Notification{Principal: "u1", Kind: notify.KindRunEnded, Title: "middle"}); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(notify.MaxAge + time.Hour) // "old" is now past the limit; "middle" is not
	got, _ := svc.List(ctx, "u1", false, 0)
	if len(got) != 1 || got[0].Title != "middle" {
		t.Fatalf("list = %+v, want only the notification inside the limit", got)
	}
	if unread, _ := svc.List(ctx, "u1", true, 0); len(unread) != 1 {
		t.Fatalf("unread list = %+v", unread)
	}
	if n, _ := svc.Count(ctx, "u1"); n != 1 {
		t.Fatalf("count = %d, want 1: an expired notification must not count", n)
	}
	clock = now.Add(2*notify.MaxAge + time.Hour)
	if n, _ := svc.Count(ctx, "u1"); n != 0 {
		t.Fatalf("count = %d after both expired", n)
	}
}
