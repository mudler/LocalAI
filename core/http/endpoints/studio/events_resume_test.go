package studio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
)

var _ = Describe("the events endpoint of a chat", func() {
	var (
		svc     *studiosvc.Service
		h       *Handlers
		harness *studiotest.GateHarness
		chat    string
		firstID string
	)

	// get serves the endpoint until the turn ends, or until the context is
	// cancelled, and returns the body.
	get := func(user, target, header string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		req := httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx)
		if header != "" {
			req.Header.Set("Last-Event-ID", header)
		}
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.Set("auth_user", &auth.User{ID: user, Role: auth.RoleUser})
		c.SetParamNames("id")
		c.SetParamValues(chat)
		Expect(h.Events(c)).To(Succeed())
		return rec.Body.String()
	}

	BeforeEach(func() {
		ctx := context.Background()
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		harness = studiotest.NewGateHarness()
		harness.End = make(chan struct{}, 1)
		harness.Gate <- struct{}{}
		svc, err = studiosvc.New(studiosvc.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: harness})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(svc.Close(ctx)).To(Succeed()) })
		h = New(svc, nil)
		rec, err := svc.Create(ctx, "alice", studiosvc.CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		chat = rec.ID
		live, cancel, err := svc.Events(ctx, "alice", chat)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		_, err = svc.Send(ctx, "alice", chat, "hello")
		Expect(err).NotTo(HaveOccurred())
		for ev := range live {
			if ev.Kind == session.KindText {
				firstID = ev.ID
				break
			}
		}
		Expect(firstID).NotTo(BeEmpty())
		harness.End <- struct{}{}
		Eventually(func() bool {
			info, err := svc.Turn(ctx, "alice", chat)
			return err == nil && !info.Running && info.Last != ""
		}, 20*time.Second, 5*time.Millisecond).Should(BeTrue())
	})

	It("sends an id on each event of a turn", func() {
		body := get("alice", "/api/chats/x/events?after="+firstID, "")
		Expect(body).To(MatchRegexp(`(?m)^id: [0-9a-f]+\.[0-9]+$`))
		Expect(body).To(ContainSubstring("event: turn_end"))
	})

	It("resumes after the id in the query", func() {
		body := get("alice", "/api/chats/x/events?after="+firstID, "")
		Expect(body).To(ContainSubstring("event: text\n"))
		Expect(body).To(ContainSubstring(`"text":"done"`))
		Expect(body).NotTo(ContainSubstring(`working on`))
		Expect(body).NotTo(ContainSubstring("event: resync"))
	})

	It("resumes after the Last-Event-ID header, which wins over the query", func() {
		body := get("alice", "/api/chats/x/events?after=junk.1", firstID)
		Expect(body).To(ContainSubstring(`"text":"done"`))
		Expect(body).NotTo(ContainSubstring("event: resync"))
	})

	It("sends a resync for an id it does not know", func() {
		for _, id := range []string{"junk", "other.4", firstID + "0"} {
			body := get("alice", "/api/chats/x/events?after="+id, "")
			Expect(body).To(HavePrefix("id: "), id)
			Expect(body).To(ContainSubstring("event: resync"), id)
			Expect(body).NotTo(ContainSubstring("event: text"), id)
		}
	})

	It("starts from now when no id is sent", func() {
		body := get("alice", "/api/chats/x/events", "")
		Expect(body).NotTo(ContainSubstring("event: text"))
		Expect(strings.Count(body, "event:")).To(Equal(0))
	})

	It("keeps another user's chat private", func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		req := httptest.NewRequest(http.MethodGet, "/api/chats/x/events?after="+firstID, nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.Set("auth_user", &auth.User{ID: "bob", Role: auth.RoleUser})
		c.SetParamNames("id")
		c.SetParamValues(chat)
		_ = h.Events(c)
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	It("answers 429 past the cap of open streams on one chat", func() {
		ctx := context.Background()
		for range 8 {
			_, cancel, err := svc.EventsAfter(ctx, "alice", chat, "")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cancel)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/chats/x/events", nil)
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.Set("auth_user", &auth.User{ID: "alice", Role: auth.RoleUser})
		c.SetParamNames("id")
		c.SetParamValues(chat)
		_ = h.Events(c)
		Expect(rec.Code).To(Equal(http.StatusTooManyRequests))
	})
})
