package mcpconns_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

// within runs f and reports whether it returned before the limit.
func within(limit time.Duration, f func()) bool {
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
		return true
	case <-time.After(limit):
		return false
	}
}

func TestAStoppedTurnDoesNotWaitForAServerThatNeverAnswers(t *testing.T) {
	g := gomega.NewWithT(t)
	e := newEnv(t, func(o *mcpconns.Options) { o.ConnectTimeout = 10 * time.Second })
	over := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-over:
		}
	}))
	t.Cleanup(func() { close(over); hung.Close() })
	c, err := e.svc.Create(t.Context(), "u1", httpInput("Hung", hung.URL))
	g.Expect(err).NotTo(gomega.HaveOccurred())

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(150*time.Millisecond, cancel)
	var missing []string
	returned := within(3*time.Second, func() { _, missing = e.svc.Resolve(ctx, "u1", []string{c.ID}, false) })
	g.Expect(returned).To(gomega.BeTrue(), "Resolve waited for the connect timeout after its context ended")
	g.Expect(missing).To(gomega.HaveLen(1))
	g.Expect(e.svc.Sessions()).To(gomega.BeZero())
}

func TestASessionOutlivesTheTurnThatOpenedIt(t *testing.T) {
	g := gomega.NewWithT(t)
	e := newEnv(t)
	f := newFakeServer(t, 0)
	c, err := e.svc.Create(t.Context(), "u1", httpInput("Docs", f.URL))
	g.Expect(err).NotTo(gomega.HaveOccurred())

	turn, end := context.WithCancel(t.Context())
	tools, missing := e.svc.Resolve(turn, "u1", []string{c.ID}, false)
	g.Expect(missing).To(gomega.BeEmpty())
	end() // the turn is over; the session stays for the next one

	g.Expect(e.svc.Sessions()).To(gomega.Equal(1))
	for _, tl := range tools {
		if tl.Name != "echo" {
			continue
		}
		out, err := tl.Run(t.Context(), json.RawMessage(`{"text":"again"}`))
		g.Expect(err).NotTo(gomega.HaveOccurred())
		g.Expect(out).To(gomega.Equal("echo: again"))
		return
	}
	g.Expect(tools).NotTo(gomega.BeEmpty(), "no echo tool")
}

func TestNoSessionIsOpenedAfterClose(t *testing.T) {
	g := gomega.NewWithT(t)
	e := newEnv(t)
	f := newFakeServer(t, 0)
	c, err := e.svc.Create(t.Context(), "u1", httpInput("Docs", f.URL))
	g.Expect(err).NotTo(gomega.HaveOccurred())
	e.svc.Close()

	tools, missing := e.svc.Resolve(t.Context(), "u1", []string{c.ID}, false)
	g.Expect(tools).To(gomega.BeEmpty())
	g.Expect(missing).To(gomega.HaveLen(1))
	g.Expect(e.svc.Sessions()).To(gomega.BeZero(), "a session was cached after Close")
}

func TestASlowKeyReadDoesNotBlockTheSessionCache(t *testing.T) {
	g := gomega.NewWithT(t)
	key, err := connectors.NewKey()
	g.Expect(err).NotTo(gomega.HaveOccurred())
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	e := newEnv(t, func(o *mcpconns.Options) {
		o.Key = func(context.Context) ([]byte, error) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			return key, nil
		}
	})
	in := httpInput("Docs", "https://example.com/mcp")
	in.Token = str("tok")
	created := make(chan struct{})
	go func() { defer close(created); _, _ = e.svc.Create(t.Context(), "u1", in) }()
	<-entered
	t.Cleanup(func() { close(release); <-created })

	g.Expect(within(time.Second, func() { e.svc.ReapIdle(); _ = e.svc.Sessions() })).To(gomega.BeTrue(),
		"the session cache waited for the key to be read")
}
