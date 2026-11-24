package sse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func sseContext(ctx context.Context) (echo.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	return echo.New().NewContext(req, rec), rec
}

var _ = Describe("Events", func() {
	It("writes each value as an event and stops when the channel closes", func() {
		c, rec := sseContext(context.Background())
		events := make(chan int, 3)
		events <- 1
		events <- 2
		close(events)
		Expect(Events(c, events, time.Minute, func(n int) (string, any) { return "number", map[string]int{"n": n} })).To(Succeed())
		Expect(rec.Header().Get("Content-Type")).To(Equal("text/event-stream"))
		Expect(rec.Body.String()).To(Equal("event: number\ndata: {\"n\":1}\n\nevent: number\ndata: {\"n\":2}\n\n"))
	})

	It("returns when the client goes away", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		c, _ := sseContext(ctx)
		events := make(chan int) // never written and never closed
		done := make(chan error, 1)
		go func() { done <- Events(c, events, time.Minute, func(n int) (string, any) { return "x", n }) }()
		Consistently(done, 50*time.Millisecond).ShouldNot(Receive())
		cancel()
		Eventually(done, time.Second).Should(Receive(BeNil()), "the stream kept running after the client left")
	})

	It("skips a value that cannot be encoded", func() {
		c, rec := sseContext(context.Background())
		events := make(chan int, 2)
		events <- 1
		events <- 2
		close(events)
		err := Events(c, events, time.Minute, func(n int) (string, any) {
			if n == 1 {
				return "bad", make(chan int) // json cannot encode a channel
			}
			return "ok", n
		})
		Expect(err).To(Succeed())
		Expect(rec.Body.String()).To(Equal("event: ok\ndata: 2\n\n"))
	})
})

var _ = Describe("Stream", func() {
	It("writes the id of an event when it has one", func() {
		c, rec := sseContext(context.Background())
		events := make(chan int, 2)
		events <- 1
		events <- 2
		close(events)
		Expect(Stream(c, events, time.Minute, func(n int) (string, string, any) {
			if n == 1 {
				return "7", "number", n
			}
			return "", "number", n
		})).To(Succeed())
		Expect(rec.Body.String()).To(Equal("id: 7\nevent: number\ndata: 1\n\nevent: number\ndata: 2\n\n"))
	})

	It("sends a heartbeat comment while idle", func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		c, rec := sseContext(ctx)
		done := make(chan error, 1)
		go func() {
			done <- Stream(c, make(chan int), 10*time.Millisecond, func(n int) (string, string, any) { return "", "x", n })
		}()
		// The recorder is not safe to read while the stream writes: let a few
		// beats pass, stop the stream, then read.
		time.Sleep(100 * time.Millisecond)
		cancel()
		Eventually(done, time.Second).Should(Receive(BeNil()))
		Expect(rec.Body.String()).To(ContainSubstring(": ping\n\n"))
	})
})
