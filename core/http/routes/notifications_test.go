// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/notify"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// sseEvents opens the event stream of a path and sends the names of the events
// it reads to the returned channel.
func sseEvents(srv *httptest.Server, path string, mod func(*http.Request)) <-chan string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	DeferCleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
	Expect(err).NotTo(HaveOccurred())
	if mod != nil {
		mod(req)
	}
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusOK))
	DeferCleanup(func() { _ = resp.Body.Close() })
	events := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "event: ") {
				events <- strings.TrimPrefix(sc.Text(), "event: ")
			}
		}
	}()
	return events
}

var _ = Describe("The notification center routes", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	add := func(user, title string) {
		_, err := s.notify.Add(context.Background(), notify.Notification{Principal: user, Kind: notify.KindNeedsAttention, Status: notify.StatusOK, Source: notify.SourceSystem, Title: title, Link: "/app/updates"})
		Expect(err).NotTo(HaveOccurred())
	}

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/notifications"),
		Entry("count", http.MethodGet, "/api/notifications/count"),
		Entry("read", http.MethodPost, "/api/notifications/read"),
		Entry("delete", http.MethodDelete, "/api/notifications"),
		Entry("events", http.MethodGet, "/api/notifications/events"),
		Entry("push key", http.MethodGet, "/api/notifications/push/key"),
		Entry("push subscribe", http.MethodPost, "/api/notifications/push/subscribe"),
		Entry("push unsubscribe", http.MethodDelete, "/api/notifications/push/subscribe"),
	)

	It("lists, counts, reads and deletes only the notifications of the caller", func() {
		add("alice", "first")
		add("alice", "second")
		add("bob", "bobs")
		list := decodeJSON(s.do("alice", http.MethodGet, "/api/notifications", nil))
		Expect(list).To(HaveKeyWithValue("unread", BeNumerically("==", 2)))
		Expect(list).To(HaveKeyWithValue("total", BeNumerically("==", 2)))
		items := list["notifications"].([]any)
		Expect(items[0].(map[string]any)).To(HaveKeyWithValue("title", "second"))
		Expect(items[0].(map[string]any)).To(HaveKeyWithValue("source", "system"))

		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/notifications/count", nil))).To(HaveKeyWithValue("unread", BeNumerically("==", 2)))
		Expect(decodeJSON(s.do("alice", http.MethodPost, "/api/notifications/read", map[string]any{"all": true}))).To(HaveKeyWithValue("unread", BeNumerically("==", 0)))
		rec := s.do("alice", http.MethodDelete, "/api/notifications", map[string]any{"all": true})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("deleted", BeNumerically("==", 2)))
		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/notifications", nil))["notifications"]).To(HaveLen(1), "alice emptied bob's inbox")
	})

	It("answers the old chats paths with the same rows", func() {
		add("alice", "one")
		viaNew := decodeJSON(s.do("alice", http.MethodGet, "/api/notifications", nil))["notifications"]
		viaOld := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/notifications", nil))["notifications"]
		Expect(viaOld).To(Equal(viaNew))
		Expect(s.do("alice", http.MethodPost, "/api/notifications/read", map[string]any{}).Code).To(Equal(http.StatusBadRequest))
	})

	It("lets a person whose chats feature was revoked keep the inbox, and closes the old chats path to them", func() {
		add("carol", "an update")
		Expect(s.do("carol", http.MethodGet, "/api/notifications", nil).Code).To(Equal(http.StatusOK))
		Expect(s.do("carol", http.MethodGet, "/api/chats/notifications", nil).Code).To(Equal(http.StatusForbidden))
	})

	It("streams a notification, then a changed signal, to the stream of its owner", func() {
		srv := httptest.NewServer(s.e)
		DeferCleanup(srv.Close)
		events := sseEvents(srv, "/api/notifications/events", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "session", Value: s.sessions["alice"]})
		})
		add("alice", "x")
		Expect(s.do("alice", http.MethodDelete, "/api/notifications", map[string]any{"all": true}).Code).To(Equal(http.StatusOK))
		Eventually(events, 5*time.Second).Should(Receive(Equal("notification")))
		Eventually(events, 5*time.Second).Should(Receive(Equal("notifications_changed")))
	})

	It("keeps the browser push under the notifications path too", func() {
		rec := s.do("alice", http.MethodGet, "/api/notifications/push/key", nil)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)["public_key"]).NotTo(BeEmpty())
		sub := map[string]any{"endpoint": "https://push.example.test/abc", "keys": map[string]any{"p256dh": "BPk", "auth": "au"}}
		Expect(s.do("alice", http.MethodPost, "/api/notifications/push/subscribe", sub).Code).To(Equal(http.StatusNoContent))
		Expect(s.do("alice", http.MethodDelete, "/api/notifications/push/subscribe", map[string]any{"endpoint": sub["endpoint"]}).Code).To(Equal(http.StatusNoContent))
		Expect(s.do("alice", http.MethodPost, "/api/notifications/push/subscribe", map[string]any{}).Code).To(Equal(http.StatusBadRequest))
	})
})

var _ = Describe("The notification center without the chats", func() {
	get := func(e *echo.Echo, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	It("serves the inbox on the new path while the old one answers that the chats are off", func() {
		st, err := notify.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		n := notify.NewService(notify.Options{Store: st})
		_, err = n.Add(context.Background(), notify.Notification{Principal: notify.AnonymousPrincipal, Title: "a backend update", Source: notify.SourceSystem})
		Expect(err).NotTo(HaveOccurred())

		e := echo.New()
		routes.RegisterStudioRoutes(e, nil, nil, nil)
		routes.RegisterNotificationRoutes(e, n, nil, false)

		rec := get(e, "/api/notifications")
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(rec)["notifications"]).To(HaveLen(1))
		Expect(get(e, "/api/notifications/count").Code).To(Equal(http.StatusOK))
		old := get(e, "/api/chats/notifications")
		Expect(old.Code).To(Equal(http.StatusNotFound))
		Expect(old.Body.String()).To(ContainSubstring("chats_off"))
		// Push needs somewhere to keep subscriptions: without it, 409.
		Expect(get(e, "/api/notifications/push/key").Code).To(Equal(http.StatusConflict))
	})

	It("answers 404 on every path when there is no notifier", func() {
		e := echo.New()
		routes.RegisterNotificationRoutes(e, nil, nil, true)
		for _, p := range []string{"/api/notifications", "/api/notifications/events", "/api/chats/notifications/count"} {
			rec := get(e, p)
			Expect(rec.Code).To(Equal(http.StatusNotFound), p)
			Expect(rec.Body.String()).To(ContainSubstring("notifications_off"))
		}
	})

	It("carries a notification written on one frontend to the stream of another", func() {
		bus := testutil.NewFakeBus()
		st, err := notify.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		a := notify.NewService(notify.Options{Store: st, Fanout: notify.NewBroadcastFanout(bus, nil)})
		b := notify.NewService(notify.Options{Store: st, Fanout: notify.NewBroadcastFanout(bus, nil)})
		eB := echo.New()
		routes.RegisterNotificationRoutes(eB, b, nil, false)
		srv := httptest.NewServer(eB)
		DeferCleanup(srv.Close)
		events := sseEvents(srv, "/api/notifications/events", nil)

		// The writer is the other frontend. It does not hold a lock the apply
		// path takes: the fake bus delivers before Add returns.
		_, err = a.Add(context.Background(), notify.Notification{Principal: notify.AnonymousPrincipal, Title: "from the leader", Source: notify.SourceSystem})
		Expect(err).NotTo(HaveOccurred())
		Eventually(events, 5*time.Second).Should(Receive(Equal("notification")))
		_, err = a.DeleteAll(context.Background(), notify.AnonymousPrincipal, false)
		Expect(err).NotTo(HaveOccurred())
		Eventually(events, 5*time.Second).Should(Receive(Equal("notifications_changed")))
	})
})
