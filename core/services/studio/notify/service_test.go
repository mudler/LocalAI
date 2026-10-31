package notify_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/testutil"
)

func recv(ch <-chan notify.Notification) notify.Notification {
	var n notify.Notification
	Eventually(ch, 5*time.Second).Should(Receive(&n), "no notification arrived or the subscription closed")
	return n
}

func none(ch <-chan notify.Notification) {
	Consistently(ch, 50*time.Millisecond).ShouldNot(Receive(), "a notification arrived that should not have")
}

func newService(fan notify.Fanout, st notify.Store) *notify.Service {
	if st == nil {
		var err error
		st, err = notify.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
	}
	return notify.NewService(notify.Options{Store: st, Fanout: fan, Clock: func() time.Time { return time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC) }})
}

var _ = Describe("the notification service", func() {
	It("cuts the text and stamps the notification", func() {
		svc := newService(nil, nil)
		n, err := svc.Add(context.Background(), notify.Notification{Principal: "u1", Kind: notify.KindRunEnded, Status: notify.StatusOK,
			Title: strings.Repeat("é", 300), Body: strings.Repeat("b", 1000), Link: notify.ChatLink("c1"), Read: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(len([]rune(n.Title))).To(Equal(notify.MaxTitleRunes))
		Expect(len([]rune(n.Body))).To(Equal(notify.MaxBodyRunes))
		Expect(n.Read).To(BeFalse())
		Expect(n.ID).NotTo(BeZero())
		Expect(n.Created.IsZero()).To(BeFalse())
		got, _ := svc.List(context.Background(), "u1", false, 0)
		Expect(got).To(HaveLen(1))
		Expect(got[0].ID).To(Equal(n.ID))
	})

	It("tells a local subscriber only about its owner and stops after cancel", func() {
		svc := newService(nil, nil)
		mine, cancel := svc.Subscribe("u1")
		other, cancelOther := svc.Subscribe("u2")
		defer cancelOther()
		_, _ = svc.Add(context.Background(), notify.Notification{Principal: "u1", Title: "hello"})
		Expect(recv(mine).Title).To(Equal("hello"), "wrong notification")
		none(other)
		cancel()
		_, ok := <-mine
		Expect(ok).To(BeFalse(), "the channel should be closed after cancel")
		_, _ = svc.Add(context.Background(), notify.Notification{Principal: "u1", Title: "again"})
	})

	It("carries a notification added on one frontend to the clients of another", func() {
		net := testutil.NewFakeBus()
		one := newService(notify.NewBroadcastFanout(net, nil), nil)
		two := newService(notify.NewBroadcastFanout(net, nil), nil)
		ch, cancel := two.Subscribe("u/1")
		defer cancel()
		other, cancelOther := two.Subscribe("u/2")
		defer cancelOther()
		_, err := one.Add(context.Background(), notify.Notification{Principal: "u/1", Kind: notify.KindRunFailed, Status: notify.StatusFailed, Title: "Nightly failed", Link: "/app/chats/c9"})
		Expect(err).NotTo(HaveOccurred())
		n := recv(ch)
		Expect(n.Title).To(Equal("Nightly failed"))
		Expect(n.Kind).To(Equal(notify.KindRunFailed))
		Expect(n.Link).To(Equal("/app/chats/c9"))
		Expect(n.ID).NotTo(BeZero())
		none(other)
	})

	It("clears the notifications of a chat when it is marked read", func() {
		svc := newService(nil, nil)
		ctx := context.Background()
		_, _ = svc.Add(ctx, notify.Notification{Principal: "u1", Title: "a", Link: notify.ChatLink("c1")})
		_, _ = svc.Add(ctx, notify.Notification{Principal: "u1", Title: "b", Link: notify.ChatLink("c2")})
		Expect(svc.MarkChatRead(ctx, "u1", "c1")).To(Succeed())
		n, _ := svc.Count(ctx, "u1")
		Expect(n).To(BeEquivalentTo(1), "unread")
	})

	It("clears an inbox and tells the clients of that user, on every frontend, to read again", func() {
		net := testutil.NewFakeBus()
		st, err := notify.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		one := newService(notify.NewBroadcastFanout(net, nil), st)
		two := newService(notify.NewBroadcastFanout(net, nil), st)
		ctx := context.Background()
		_, _ = one.Add(ctx, notify.Notification{Principal: "u1", Title: "a"})
		_, _ = one.Add(ctx, notify.Notification{Principal: "u1", Title: "b"})
		_, _ = one.Add(ctx, notify.Notification{Principal: "u2", Title: "c"})
		mine, cancel := two.Subscribe("u1")
		defer cancel()
		other, cancelOther := two.Subscribe("u2")
		defer cancelOther()
		n, err := one.DeleteAll(ctx, "u1", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(2))
		Expect(recv(mine).Op).To(Equal(notify.OpChanged))
		none(other)
		left, _ := two.List(ctx, "u1", false, 0)
		Expect(left).To(BeEmpty(), "the store is shared by both frontends")
		kept, _ := two.List(ctx, "u2", false, 0)
		Expect(kept).To(HaveLen(1))
	})

	It("signals a change after a read or a delete, and calls no hook for it", func() {
		svc := newService(nil, nil)
		hooked := 0
		svc.OnAdd(func(notify.Notification) { hooked++ })
		ctx := context.Background()
		a, _ := svc.Add(ctx, notify.Notification{Principal: "u1", Title: "a"})
		ch, cancel := svc.Subscribe("u1")
		defer cancel()
		Expect(svc.MarkRead(ctx, "u1", []int64{a.ID})).To(Succeed())
		Expect(recv(ch).Op).To(Equal(notify.OpChanged))
		Expect(svc.Delete(ctx, "u1", []int64{a.ID})).To(Succeed())
		Expect(recv(ch).Op).To(Equal(notify.OpChanged))
		Expect(hooked).To(Equal(1), "only the Add calls the hook")
	})

	It("calls a hook for every stored notification", func() {
		svc := newService(nil, nil)
		var seen []string
		svc.OnAdd(func(n notify.Notification) { seen = append(seen, n.Title) })
		_, _ = svc.Add(context.Background(), notify.Notification{Principal: "u1", Title: "x"})
		Expect(seen).To(Equal([]string{"x"}))
	})

	// The 90-day limit holds when a user reads, not only when a new notification
	// arrives: a user who stops getting notifications must not keep old ones in view.
	It("does not show old notifications even when nothing new arrives", func() {
		st, err := notify.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
		clock := now
		svc := notify.NewService(notify.Options{Store: st, Clock: func() time.Time { return clock }})
		ctx := context.Background()
		_, err = svc.Add(ctx, notify.Notification{Principal: "u1", Kind: notify.KindRunEnded, Title: "old"})
		Expect(err).NotTo(HaveOccurred())
		clock = now.Add(notify.MaxAge / 2)
		_, err = svc.Add(ctx, notify.Notification{Principal: "u1", Kind: notify.KindRunEnded, Title: "middle"})
		Expect(err).NotTo(HaveOccurred())
		clock = now.Add(notify.MaxAge + time.Hour) // "old" is now past the limit; "middle" is not
		got, _ := svc.List(ctx, "u1", false, 0)
		Expect(got).To(HaveLen(1), "want only the notification inside the limit")
		Expect(got[0].Title).To(Equal("middle"))
		unread, _ := svc.List(ctx, "u1", true, 0)
		Expect(unread).To(HaveLen(1), "unread list")
		n, _ := svc.Count(ctx, "u1")
		Expect(n).To(BeEquivalentTo(1), "an expired notification must not count")
		clock = now.Add(2*notify.MaxAge + time.Hour)
		n, _ = svc.Count(ctx, "u1")
		Expect(n).To(BeEquivalentTo(0), "count after both expired")
	})
})
