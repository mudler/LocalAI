package studio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/onsi/gomega"
)

func sseContext(ctx context.Context) (echo.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	return echo.New().NewContext(req, rec), rec
}

func TestStreamSSEWritesEachValueAsAnEventAndStopsWhenTheChannelCloses(t *testing.T) {
	g := gomega.NewWithT(t)
	c, rec := sseContext(context.Background())
	events := make(chan int, 3)
	events <- 1
	events <- 2
	close(events)
	g.Expect(streamSSE(c, events, func(n int) (string, any) { return "number", map[string]int{"n": n} })).To(gomega.Succeed())
	g.Expect(rec.Header().Get("Content-Type")).To(gomega.Equal("text/event-stream"))
	g.Expect(rec.Body.String()).To(gomega.Equal("event: number\ndata: {\"n\":1}\n\nevent: number\ndata: {\"n\":2}\n\n"))
}

func TestStreamSSEReturnsWhenTheClientGoesAway(t *testing.T) {
	g := gomega.NewWithT(t)
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := sseContext(ctx)
	events := make(chan int) // never written and never closed
	done := make(chan error, 1)
	go func() { done <- streamSSE(c, events, func(n int) (string, any) { return "x", n }) }()
	g.Consistently(done, 50*time.Millisecond).ShouldNot(gomega.Receive())
	cancel()
	g.Eventually(done, time.Second).Should(gomega.Receive(gomega.BeNil()), "the stream kept running after the client left")
}

func TestStreamSSESkipsAValueThatCannotBeEncoded(t *testing.T) {
	g := gomega.NewWithT(t)
	c, rec := sseContext(context.Background())
	events := make(chan int, 2)
	events <- 1
	events <- 2
	close(events)
	err := streamSSE(c, events, func(n int) (string, any) {
		if n == 1 {
			return "bad", make(chan int) // json cannot encode a channel
		}
		return "ok", n
	})
	g.Expect(err).To(gomega.Succeed())
	g.Expect(rec.Body.String()).To(gomega.Equal("event: ok\ndata: 2\n\n"))
}
