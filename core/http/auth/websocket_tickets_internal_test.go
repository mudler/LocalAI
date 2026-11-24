package auth

import (
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WebSocket ticket store", func() {
	var store *WebSocketTickets
	var c echo.Context
	const path = "/api/motion/sessions/test/poses"
	const origin = "https://consumer.example"
	BeforeEach(func() {
		store = NewWebSocketTickets()
		c = echo.New().NewContext(httptest.NewRequest("POST", "/tickets", nil), httptest.NewRecorder())
	})
	It("expires after 30 seconds", func() {
		now := time.Now()
		store.now = func() time.Time { return now }
		ticket, err := store.Issue(c, path, origin)
		Expect(err).NotTo(HaveOccurred())
		Expect(ticket.ExpiresAt).To(Equal(now.Add(30 * time.Second)))
		now = now.Add(30 * time.Second)
		_, ok := store.consume(ticket.Ticket, path, origin)
		Expect(ok).To(BeFalse())
	})
	It("binds the exact origin and path and consumes once", func() {
		ticket, err := store.Issue(c, path, origin)
		Expect(err).NotTo(HaveOccurred())
		_, ok := store.consume(ticket.Ticket, path, "https://other.example")
		Expect(ok).To(BeFalse())
		_, ok = store.consume(ticket.Ticket, "/api/motion/sessions/other/poses", origin)
		Expect(ok).To(BeFalse())
		_, ok = store.consume(ticket.Ticket, path, origin)
		Expect(ok).To(BeTrue())
		_, ok = store.consume(ticket.Ticket, path, origin)
		Expect(ok).To(BeFalse())
	})
	It("has one winner under concurrent redemption", func() {
		ticket, err := store.Issue(c, path, origin)
		Expect(err).NotTo(HaveOccurred())
		var wg sync.WaitGroup
		var accepted atomic.Int64
		for range 16 {
			wg.Go(func() {
				if _, ok := store.consume(ticket.Ticket, path, origin); ok {
					accepted.Add(1)
				}
			})
		}
		wg.Wait()
		Expect(accepted.Load()).To(Equal(int64(1)))
	})
	It("limits outstanding tickets and frees expired capacity", func() {
		now := time.Now()
		store.now = func() time.Time { return now }
		for range 32 {
			_, err := store.Issue(c, path, origin)
			Expect(err).NotTo(HaveOccurred())
		}
		_, err := store.Issue(c, path, origin)
		Expect(err).To(MatchError(echo.NewHTTPError(429, "too many outstanding WebSocket tickets")))
		now = now.Add(WebSocketTicketTTL)
		_, err = store.Issue(c, path, origin)
		Expect(err).NotTo(HaveOccurred())
	})
	It("revokes tickets when a target closes", func() {
		ticket, err := store.Issue(c, path, origin)
		Expect(err).NotTo(HaveOccurred())
		store.RevokePath(path)
		_, ok := store.consume(ticket.Ticket, path, origin)
		Expect(ok).To(BeFalse())
	})
	It("rejects mismatched request origins and opaque origins", func() {
		c.Request().Header.Set("Origin", origin)
		_, err := store.Issue(c, path, "https://other.example")
		Expect(err).To(HaveOccurred())
		for _, invalid := range []string{"null", "*", "https://user:pass@example.com", "https://example.com/path", "https://example.com?x=y"} {
			Expect(ValidWebSocketOrigin(invalid)).To(BeFalse())
		}
	})
})
