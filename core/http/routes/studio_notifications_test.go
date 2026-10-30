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

	"github.com/mudler/LocalAI/core/services/studio/notify"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Chats notifications endpoints", func() {
	var s *studioServer
	BeforeEach(func() { s = newStudioServer() })

	add := func(user, title, link string) int64 {
		n, err := s.notify.Add(context.Background(), notify.Notification{Principal: user, Kind: notify.KindRunEnded, Status: notify.StatusOK, Title: title, Link: link})
		Expect(err).NotTo(HaveOccurred())
		return n.ID
	}

	DescribeTable("require authentication on every route",
		func(method, path string) {
			Expect(s.do("", method, path, nil).Code).To(Equal(http.StatusUnauthorized))
		},
		Entry("list", http.MethodGet, "/api/chats/notifications"),
		Entry("count", http.MethodGet, "/api/chats/notifications/count"),
		Entry("read", http.MethodPost, "/api/chats/notifications/read"),
		Entry("delete", http.MethodDelete, "/api/chats/notifications"),
		Entry("events", http.MethodGet, "/api/chats/notifications/events"),
	)

	It("lists, counts, reads and deletes the user's own notifications", func() {
		a := add("alice", "first", "/app/chats/c1")
		add("alice", "second", "/app/chats/c2")
		add("bob", "bobs", "/app/chats/c3")

		list := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/notifications", nil))
		Expect(list).To(HaveKeyWithValue("unread", BeNumerically("==", 2)))
		items := list["notifications"].([]any)
		Expect(items).To(HaveLen(2))
		Expect(items[0].(map[string]any)).To(HaveKeyWithValue("title", "second"))

		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/notifications/count", nil))).To(HaveKeyWithValue("unread", BeNumerically("==", 2)))

		rec := s.do("alice", http.MethodPost, "/api/chats/notifications/read", map[string]any{"ids": []int64{a}})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(decodeJSON(rec)).To(HaveKeyWithValue("unread", BeNumerically("==", 1)))
		unread := decodeJSON(s.do("alice", http.MethodGet, "/api/chats/notifications?unread=1", nil))["notifications"].([]any)
		Expect(unread).To(HaveLen(1))

		Expect(decodeJSON(s.do("alice", http.MethodPost, "/api/chats/notifications/read", map[string]any{"all": true}))).To(HaveKeyWithValue("unread", BeNumerically("==", 0)))
		Expect(s.do("alice", http.MethodDelete, "/api/chats/notifications", map[string]any{"ids": []int64{a}}).Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/notifications", nil))["notifications"]).To(HaveLen(1))

		Expect(decodeJSON(s.do("bob", http.MethodGet, "/api/chats/notifications", nil))["notifications"]).To(HaveLen(1))
	})

	It("refuses an empty or oversized request", func() {
		Expect(s.do("alice", http.MethodPost, "/api/chats/notifications/read", map[string]any{}).Code).To(Equal(http.StatusBadRequest))
		Expect(s.do("alice", http.MethodDelete, "/api/chats/notifications", map[string]any{"ids": []int64{}}).Code).To(Equal(http.StatusBadRequest))
	})

	It("clears the notification of a chat when the chat is opened", func() {
		id := s.create("alice")
		add("alice", "run done", "/app/chats/"+id)
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/notifications/count", nil))).To(HaveKeyWithValue("unread", BeNumerically("==", 1)))
		Expect(s.do("alice", http.MethodGet, "/api/chats/"+id, nil).Code).To(Equal(http.StatusOK))
		Expect(decodeJSON(s.do("alice", http.MethodGet, "/api/chats/notifications/count", nil))).To(HaveKeyWithValue("unread", BeNumerically("==", 0)))
	})

	It("streams a new notification to its owner and to nobody else", func() {
		srv := httptest.NewServer(s.e)
		DeferCleanup(srv.Close)
		open := func(user string) (*bufio.Scanner, func()) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/chats/notifications/events", nil)
			Expect(err).NotTo(HaveOccurred())
			req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions[user]})
			resp, err := http.DefaultClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/event-stream"))
			return bufio.NewScanner(resp.Body), func() { cancel(); _ = resp.Body.Close() }
		}
		alice, closeAlice := open("alice")
		defer closeAlice()
		bob, closeBob := open("bob")
		defer closeBob()

		add("alice", "Nightly done", "/app/chats/c1")
		var event, data string
		for alice.Scan() {
			line := alice.Text()
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimPrefix(line, "data: ")
				break
			}
		}
		Expect(event).To(Equal("notification"))
		Expect(data).To(ContainSubstring("Nightly done"))

		got := make(chan string, 1)
		go func() {
			for bob.Scan() {
				if strings.HasPrefix(bob.Text(), "data: ") {
					got <- bob.Text()
					return
				}
			}
		}()
		Consistently(got, 300*time.Millisecond).ShouldNot(Receive())
	})
})
