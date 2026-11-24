package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// wsHarness serves one realtime-like websocket: it watches a key the way a
// session does, and reads until the connection ends.
type wsHarness struct {
	srv   *httptest.Server
	ended chan struct{}
}

func newWSHarness(status func(context.Context) (bool, error), interval time.Duration) *wsHarness {
	h := &wsHarness{ended: make(chan struct{})}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		stop := watchClientKey(NewWebSocketTransport(ws), status, interval)
		defer stop()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				close(h.ended)
				return
			}
		}
	}))
	DeferCleanup(h.srv.Close)
	return h
}

// dial is the fake client of the session.
func (h *wsHarness) dial() *websocket.Conn {
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(h.srv.URL, "http")+"/v1/realtime?model=voice", nil)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = c.Close() })
	return c
}

// drain reads what the server sends until the connection closes, and returns
// the messages and the time it took.
func drain(c *websocket.Conn, within time.Duration) (msgs []string, closed bool) {
	_ = c.SetReadDeadline(time.Now().Add(within))
	for {
		_, m, err := c.ReadMessage()
		if err != nil {
			return msgs, !isTimeout(err)
		}
		msgs = append(msgs, string(m))
	}
}

func isTimeout(err error) bool {
	var ne interface{ Timeout() bool }
	return errors.As(err, &ne) && ne.Timeout()
}

var _ = Describe("watchClientKey", func() {
	It("keeps the session while the key stands", func() {
		var calls atomic.Int32
		h := newWSHarness(func(context.Context) (bool, error) { calls.Add(1); return true, nil }, 10*time.Millisecond)
		c := h.dial()
		msgs, closed := drain(c, 300*time.Millisecond)
		Expect(closed).To(BeFalse())
		Expect(msgs).To(BeEmpty())
		Expect(calls.Load()).To(BeNumerically(">=", 3), "the key is asked again and again")
	})

	It("tells the client and closes the session when the key stops standing", func() {
		var revoked atomic.Bool
		h := newWSHarness(func(context.Context) (bool, error) { return !revoked.Load(), nil }, 10*time.Millisecond)
		c := h.dial()
		time.Sleep(50 * time.Millisecond)
		revoked.Store(true)
		msgs, closed := drain(c, 2*time.Second)
		Expect(closed).To(BeTrue())
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0]).To(ContainSubstring("client_key_invalid"))
		Eventually(h.ended).Should(BeClosed(), "the read of the session ends, so it cleans up")
	})

	It("lives through a failed check or two, and closes when the store stays down", func() {
		var calls atomic.Int32
		h := newWSHarness(func(context.Context) (bool, error) {
			n := calls.Add(1)
			if n == 2 || n == 3 {
				return false, errors.New("store down")
			}
			return true, nil
		}, 10*time.Millisecond)
		c := h.dial()
		_, closed := drain(c, 300*time.Millisecond)
		Expect(closed).To(BeFalse(), "two failures in a row, then a good check")

		calls.Store(0)
		h2 := newWSHarness(func(context.Context) (bool, error) { return false, errors.New("store down") }, 10*time.Millisecond)
		_, closed = drain(h2.dial(), 2*time.Second)
		Expect(closed).To(BeTrue(), "%d failures in a row close it", clientKeyMaxStoreErrors)
	})

	It("closes when the store hangs, since a check that does not answer is a failed check", func() {
		var calls atomic.Int32
		// The check ignores its context and never returns.
		block := make(chan struct{})
		DeferCleanup(func() { close(block) })
		h := newWSHarness(func(context.Context) (bool, error) { calls.Add(1); <-block; return true, nil }, 20*time.Millisecond)
		msgs, closed := drain(h.dial(), 3*time.Second)
		Expect(closed).To(BeTrue(), "%d timeouts in a row close it", clientKeyMaxStoreErrors)
		Expect(msgs).To(HaveLen(1))
		Expect(calls.Load()).To(BeNumerically(">=", clientKeyMaxStoreErrors))
	})

	It("stops when the session ends first", func() {
		var calls atomic.Int32
		tr := &recordingTransport{}
		stop := watchClientKey(tr, func(context.Context) (bool, error) { calls.Add(1); return false, nil }, time.Hour)
		stop()
		stop() // twice is fine
		time.Sleep(20 * time.Millisecond)
		Expect(calls.Load()).To(BeZero())
		Expect(tr.closed.Load()).To(BeFalse())
	})
})

type recordingTransport struct {
	fakeTransport
	closed atomic.Bool
}

func (r *recordingTransport) Close() error { r.closed.Store(true); return nil }
