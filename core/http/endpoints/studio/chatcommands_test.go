package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// commandRig is a chats API over a real service, with a scripted harness that
// compacts on request, and an echo router with the command routes.
type commandRig struct {
	svc *studiosvc.Service
	e   *echo.Echo
}

func newCommandRig() *commandRig {
	h := &studiotest.ToolHarness{Script: func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Say: "ok"}}
	}}
	h.Compact = func(string) (int, int, error) { return 800, 200, nil }
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	svc, err := studiosvc.New(studiosvc.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = svc.Close(context.Background()) })
	return &commandRig{svc: svc, e: commandRouter(svc)}
}

func commandRouter(svc *studiosvc.Service) *echo.Echo {
	hs := New(svc, nil)
	e := echo.New()
	e.POST("/api/chats/:id/compact", hs.Compact)
	e.POST("/api/chats/:id/clear", hs.Clear)
	e.POST("/api/chats/:id/messages", hs.Send)
	e.GET("/api/chats/:id/messages", hs.Messages)
	return e
}

// call runs one request and returns the status and the body.
func (r *commandRig) call(method, path, body string) (int, string) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.e.ServeHTTP(rec, req.WithContext(context.Background()))
	return rec.Code, rec.Body.String()
}

// asUser makes every request of the router run as user.
func (r *commandRig) asUser(user string) {
	r.e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("auth_user", &auth.User{ID: user, Role: auth.RoleUser})
			return next(c)
		}
	})
}

func (r *commandRig) chatWithTurn(user string) string {
	rec, err := r.svc.Create(context.Background(), user, studiosvc.CreateInput{Model: "m"})
	Expect(err).NotTo(HaveOccurred())
	_, err = r.svc.Send(context.Background(), user, rec.ID, "hello")
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() bool {
		info, err := r.svc.Turn(context.Background(), user, rec.ID)
		return err == nil && !info.Running && info.Last != ""
	}, 20*time.Second, 5*time.Millisecond).Should(BeTrue())
	return rec.ID
}

var _ = Describe("chat command routes", func() {
	It("compact answers the sizes and the transcript has the line", func() {
		r := newCommandRig()
		r.asUser("alice")
		id := r.chatWithTurn("alice")
		code, body := r.call(http.MethodPost, "/api/chats/"+id+"/compact", "")
		Expect(code).To(Equal(http.StatusOK), body)
		var res schema.ChatsContextResult
		Expect(json.Unmarshal([]byte(body), &res)).To(Succeed())
		Expect(res).To(Equal(schema.ChatsContextResult{Before: 800, After: 200}))

		code, body = r.call(http.MethodGet, "/api/chats/"+id+"/messages", "")
		Expect(code).To(Equal(http.StatusOK))
		var list schema.ChatsMessageList
		Expect(json.Unmarshal([]byte(body), &list)).To(Succeed())
		last := list.Messages[len(list.Messages)-1]
		Expect(last.Role).To(Equal(session.RoleCompacted))
		Expect([]int{last.Before, last.After}).To(Equal([]int{800, 200}))
	})

	It("clear answers 200 then 400 with nothing left, and another user gets 404", func() {
		r := newCommandRig()
		r.asUser("alice")
		id := r.chatWithTurn("alice")
		code, body := r.call(http.MethodPost, "/api/chats/"+id+"/clear", "")
		Expect(code).To(Equal(http.StatusOK), body)
		code, body = r.call(http.MethodPost, "/api/chats/"+id+"/clear", "")
		Expect(code).To(Equal(http.StatusBadRequest), body)
		Expect(body).To(ContainSubstring("nothing to clear"))
		code, _ = r.call(http.MethodPost, "/api/chats/"+id+"/compact", "")
		Expect(code).To(Equal(http.StatusBadRequest), "nothing to compact after a clear")

		bob := &commandRig{svc: r.svc, e: commandRouter(r.svc)}
		bob.asUser("bob")
		for _, p := range []string{"/compact", "/clear"} {
			code, _ = bob.call(http.MethodPost, "/api/chats/"+id+p, "")
			Expect(code).To(Equal(http.StatusNotFound), "bob %s", p)
		}
	})

	It("a message with an unknown skill or agent is a 400 with a reason", func() {
		r := newCommandRig()
		r.asUser("alice")
		id := r.chatWithTurn("alice")
		code, body := r.call(http.MethodPost, "/api/chats/"+id+"/messages", `{"text":"x","skill":"nope"}`)
		Expect(code).To(Equal(http.StatusBadRequest), body)
		Expect(body).To(ContainSubstring(`no skill \"nope\"`))
		code, body = r.call(http.MethodPost, "/api/chats/"+id+"/messages", `{"text":"x","agent":"nobody"}`)
		Expect(code).To(Equal(http.StatusBadRequest), body)
		Expect(body).To(ContainSubstring("saved agents"))
		// A skill message may come without words; the check still runs.
		code, _ = r.call(http.MethodPost, "/api/chats/"+id+"/messages", `{"skill":"nope"}`)
		Expect(code).To(Equal(http.StatusBadRequest))
	})

	It("lists the command of a user line and the sizes of a compacted line", func() {
		list := messageList("alice", []session.Entry{
			{Seq: 1, Role: session.RoleUser, Text: "check", Invoked: &session.Invocation{Kind: session.InvokeSkill, Name: "review"}},
			{Seq: 2, Role: session.RoleCompacted, Text: session.CompactedText(9000, 3000)},
			{Seq: 3, Role: session.RoleCleared},
		})
		Expect(list.Messages[0].Invoked).To(Equal(&schema.ChatsInvocation{Kind: "skill", Name: "review"}))
		Expect([]int{list.Messages[1].Before, list.Messages[1].After}).To(Equal([]int{9000, 3000}))
		raw, err := json.Marshal(list.Messages[2])
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"role":"cleared"`))
		Expect(string(raw)).NotTo(ContainSubstring("invoked"))
	})
})
