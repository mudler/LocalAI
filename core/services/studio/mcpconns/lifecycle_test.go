package mcpconns_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

var _ = Describe("Session lifecycle", func() {
	It("does not make a stopped turn wait for a server that never answers", func() {
		e := newEnv(func(o *mcpconns.Options) { o.ConnectTimeout = 10 * time.Second })
		over := make(chan struct{})
		hung := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-over:
			}
		}))
		DeferCleanup(func() { close(over); hung.Close() })
		c, err := e.svc.Create(specCtx(), "u1", httpInput("Hung", hung.URL))
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithCancel(specCtx())
		time.AfterFunc(150*time.Millisecond, cancel)
		var missing []string
		returned := within(3*time.Second, func() { _, missing = e.svc.Resolve(ctx, "u1", []string{c.ID}, false) })
		Expect(returned).To(BeTrue(), "Resolve waited for the connect timeout after its context ended")
		Expect(missing).To(HaveLen(1))
		Expect(e.svc.Sessions()).To(BeZero())
	})

	It("keeps a session after the turn that opened it", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		c, err := e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
		Expect(err).NotTo(HaveOccurred())

		turn, end := context.WithCancel(ctx)
		tools, missing := e.svc.Resolve(turn, "u1", []string{c.ID}, false)
		Expect(missing).To(BeEmpty())
		end() // the turn is over; the session stays for the next one

		Expect(e.svc.Sessions()).To(Equal(1))
		for _, tl := range tools {
			if tl.Name != "echo" {
				continue
			}
			out, err := tl.Run(ctx, json.RawMessage(`{"text":"again"}`))
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal("echo: again"))
			return
		}
		Expect(tools).NotTo(BeEmpty(), "no echo tool")
	})

	It("opens no session after Close", func() {
		e := newEnv()
		ctx := specCtx()
		f := newFakeServer(0)
		c, err := e.svc.Create(ctx, "u1", httpInput("Docs", f.URL))
		Expect(err).NotTo(HaveOccurred())
		e.svc.Close()

		tools, missing := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		Expect(tools).To(BeEmpty())
		Expect(missing).To(HaveLen(1))
		Expect(e.svc.Sessions()).To(BeZero(), "a session was cached after Close")
	})

	It("does not let a slow key read block the session cache", func() {
		key, err := connectors.NewKey()
		Expect(err).NotTo(HaveOccurred())
		release := make(chan struct{})
		entered := make(chan struct{}, 1)
		e := newEnv(func(o *mcpconns.Options) {
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
		ctx := specCtx()
		created := make(chan struct{})
		go func() { defer GinkgoRecover(); defer close(created); _, _ = e.svc.Create(ctx, "u1", in) }()
		<-entered
		DeferCleanup(func() { close(release); <-created })

		Expect(within(time.Second, func() { e.svc.ReapIdle(); _ = e.svc.Sessions() })).To(BeTrue(),
			"the session cache waited for the key to be read")
	})
})
