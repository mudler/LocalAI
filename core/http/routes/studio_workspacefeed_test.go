// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
)

// feedEvent is one server-sent event of the change feed.
type feedEvent struct {
	id, name string
	data     map[string]any
}

var _ = Describe("The workspace change feed", func() {
	var s *studioServer
	var srv *httptest.Server
	BeforeEach(func() {
		s = newStudioServer()
		srv = httptest.NewServer(s.e)
		DeferCleanup(srv.Close)
	})

	// open starts a stream and returns its events; the stream ends with the spec.
	open := func(user string, header map[string]string) <-chan feedEvent {
		GinkgoHelper()
		ctx, cancel := context.WithCancel(context.Background())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/workspace/events", nil)
		Expect(err).NotTo(HaveOccurred())
		req.AddCookie(&http.Cookie{Name: "session", Value: s.sessions[user]})
		for k, v := range header {
			req.Header.Set(k, v)
		}
		resp, err := srv.Client().Do(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/event-stream"))
		DeferCleanup(func() { cancel(); _ = resp.Body.Close() })
		out := make(chan feedEvent, 16)
		go func() {
			defer GinkgoRecover()
			sc := bufio.NewScanner(resp.Body)
			var ev feedEvent
			for sc.Scan() {
				line := sc.Text()
				switch {
				case strings.HasPrefix(line, "id: "):
					ev.id = strings.TrimPrefix(line, "id: ")
				case strings.HasPrefix(line, "event: "):
					ev.name = strings.TrimPrefix(line, "event: ")
				case strings.HasPrefix(line, "data: "):
					Expect(json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.data)).To(Succeed())
				case line == "" && ev.name != "":
					out <- ev
					ev = feedEvent{}
				}
			}
		}()
		return out
	}

	It("needs a login", func() {
		Expect(s.do("", http.MethodGet, "/api/workspace/events", nil).Code).To(Equal(http.StatusUnauthorized))
	})

	It("sends the owner the paths of a change inside the home, and nothing to another user", func() {
		alice := open("alice", nil)
		bob := open("bob", nil)
		time.Sleep(50 * time.Millisecond) // both streams are open before the write
		Expect(s.putFile("alice", "/home.json", "{}").Code).To(Equal(http.StatusOK))
		var ev feedEvent
		Eventually(alice, 10*time.Second).Should(Receive(&ev))
		Expect(ev.name).To(Equal("change"))
		Expect(ev.data["paths"]).To(Equal([]any{"home.json"}))
		Expect(ev.data["revision"]).To(BeNumerically(">", 0))
		Expect(ev.id).To(Equal(jsonNumber(ev.data["revision"])), "the event id is the revision")
		Consistently(bob, 1500*time.Millisecond).ShouldNot(Receive())
	})

	It("resumes after the revision a reconnecting client sends", func() {
		Expect(s.putFile("alice", "/one.txt", "1").Code).To(Equal(http.StatusOK))
		first := open("alice", nil)
		Expect(s.putFile("alice", "/two.txt", "2").Code).To(Equal(http.StatusOK))
		var ev feedEvent
		Eventually(first, 10*time.Second).Should(Receive(&ev))
		Expect(s.putFile("alice", "/three.txt", "3").Code).To(Equal(http.StatusOK))
		again := open("alice", map[string]string{"Last-Event-ID": ev.id})
		var next feedEvent
		Eventually(again, 10*time.Second).Should(Receive(&next))
		Expect(next.data["paths"]).To(Equal([]any{"three.txt"}))
	})

	It("refuses a resume point that is not a revision", func() {
		Expect(s.do("alice", http.MethodGet, "/api/workspace/events?after=x", nil).Code).To(Equal(http.StatusBadRequest))
	})

	It("says when files are off", func() {
		off := newStudioServerFiles(files.Disabled("There is no data path, so files are off."))
		rec := off.do("alice", http.MethodGet, "/api/workspace/events", nil)
		Expect(rec.Code).To(Equal(http.StatusConflict))
		Expect(rec.Body.String()).To(ContainSubstring("files_off"))
	})
})

// jsonNumber prints a JSON number the way the event id spells it.
func jsonNumber(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
