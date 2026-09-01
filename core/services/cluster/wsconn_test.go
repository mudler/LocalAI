package cluster_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// wsPair returns the two ends of one live WebSocket connection.
func wsPair() (clientSide, serverSide *websocket.Conn) {
	GinkgoHelper()

	upgrader := websocket.Upgrader{}
	accepted := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		accepted <- ws
	}))
	DeferCleanup(srv.Close)

	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = c.Close() })

	var s *websocket.Conn
	Eventually(accepted, "5s").Should(Receive(&s))
	DeferCleanup(func() { _ = s.Close() })

	return c, s
}

var _ = Describe("WebsocketConn framing", func() {
	// The specs below exist because the brief's end-to-end yamux spec cannot
	// catch a lost message tail: yamux reads through a 4 KiB bufio.Reader, so
	// every small message arrives whole no matter how the adapter behaves.
	// These drive the adapter directly with buffers smaller than the message.

	It("returns the rest of a message on the following Read", func() {
		clientWS, serverWS := wsPair()
		writer := cluster.WebsocketConn(clientWS)
		reader := cluster.WebsocketConn(serverWS)

		// A lost tail would otherwise park the reassembly below forever; with a
		// deadline it fails as a timeout on the read that has nothing left.
		Expect(reader.SetReadDeadline(time.Now().Add(10 * time.Second))).To(Succeed())

		payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
		n, err := writer.Write(payload)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(Equal(len(payload)))

		// Deliberately smaller than the message: a naive adapter that starts a
		// fresh NextReader on every call drops everything past the first 7
		// bytes, and this reassembly fails.
		got := make([]byte, 0, len(payload))
		buf := make([]byte, 7)
		for len(got) < len(payload) {
			read, err := reader.Read(buf)
			Expect(err).ToNot(HaveOccurred())
			Expect(read).To(BeNumerically(">", 0))
			Expect(read).To(BeNumerically("<=", len(buf)))
			got = append(got, buf[:read]...)
		}
		Expect(got).To(Equal(payload))
	})

	It("streams a message larger than the yamux read buffer without loss or reordering", func() {
		clientWS, serverWS := wsPair()
		writer := cluster.WebsocketConn(clientWS)
		reader := cluster.WebsocketConn(serverWS)

		Expect(reader.SetReadDeadline(time.Now().Add(20 * time.Second))).To(Succeed())

		payload := make([]byte, 256*1024)
		_, err := rand.Read(payload)
		Expect(err).ToNot(HaveOccurred())

		go func() {
			defer GinkgoRecover()
			_, _ = writer.Write(payload)
		}()

		// 4096 is the buffer yamux's bufio.Reader actually hands down.
		got := make([]byte, len(payload))
		_, err = io.ReadFull(reader, got)
		Expect(err).ToNot(HaveOccurred())
		Expect(bytes.Equal(got, payload)).To(BeTrue())
	})

	It("presents consecutive messages as one continuous byte stream", func() {
		clientWS, serverWS := wsPair()
		writer := cluster.WebsocketConn(clientWS)
		reader := cluster.WebsocketConn(serverWS)

		Expect(reader.SetReadDeadline(time.Now().Add(10 * time.Second))).To(Succeed())

		for _, chunk := range []string{"abc", "", "de", "fghij"} {
			_, err := writer.Write([]byte(chunk))
			Expect(err).ToNot(HaveOccurred())
		}

		// A read spanning several messages must be satisfied: a message
		// boundary is not the end of the stream.
		got := make([]byte, 10)
		_, err := io.ReadFull(reader, got)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("abcdefghij"))
	})

	It("never hands back a zero-length read for a zero-length message", func() {
		clientWS, serverWS := wsPair()
		writer := cluster.WebsocketConn(clientWS)
		reader := cluster.WebsocketConn(serverWS)

		Expect(reader.SetReadDeadline(time.Now().Add(10 * time.Second))).To(Succeed())

		// An empty message carries nothing to return. Handing back (0, nil)
		// would be legal for io.Reader but reads as a stalled stream to callers
		// that loop on n, so the adapter waits for the next message instead.
		_, err := writer.Write(nil)
		Expect(err).ToNot(HaveOccurred())
		_, err = writer.Write([]byte("xy"))
		Expect(err).ToNot(HaveOccurred())

		n, err := reader.Read(make([]byte, 8))
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(Equal(2))
	})

	It("reports a clean peer close as io.EOF", func() {
		clientWS, serverWS := wsPair()
		reader := cluster.WebsocketConn(serverWS)

		Expect(clientWS.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))).To(Succeed())

		_, err := reader.Read(make([]byte, 8))
		Expect(err).To(MatchError(io.EOF))
	})

	It("refuses a text message rather than desynchronising the stream", func() {
		clientWS, serverWS := wsPair()
		reader := cluster.WebsocketConn(serverWS)

		Expect(clientWS.WriteMessage(websocket.TextMessage, []byte("not a frame"))).To(Succeed())

		_, err := reader.Read(make([]byte, 32))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("want binary"))
	})

	It("satisfies net.Conn", func() {
		clientWS, _ := wsPair()
		var conn net.Conn = cluster.WebsocketConn(clientWS)

		Expect(conn.LocalAddr()).ToNot(BeNil())
		Expect(conn.RemoteAddr()).ToNot(BeNil())
	})

	It("enforces a write deadline, which yamux arms before every flush", func() {
		clientWS, _ := wsPair()
		conn := cluster.WebsocketConn(clientWS)

		// Asserting that the setter returns nil would prove nothing: gorilla
		// only records the deadline and applies it at the next flush. The write
		// below is what shows the deadline reached the socket, and an adapter
		// that swallowed the call would let a stalled peer block yamux's send
		// loop forever instead of failing it.
		Expect(conn.SetWriteDeadline(time.Now().Add(-time.Second))).To(Succeed())
		_, err := conn.Write([]byte("x"))
		Expect(err).To(HaveOccurred())
		Expect(os.IsTimeout(err)).To(BeTrue(), "want a timeout, got %v", err)
	})

	It("enforces a read deadline, which is how a parked reader is unblocked", func() {
		clientWS, _ := wsPair()
		conn := cluster.WebsocketConn(clientWS)

		Expect(conn.SetReadDeadline(time.Now().Add(-time.Second))).To(Succeed())
		_, err := conn.Read(make([]byte, 8))
		Expect(err).To(HaveOccurred())
		Expect(os.IsTimeout(err)).To(BeTrue(), "want a timeout, got %v", err)
	})

	It("arms both directions from SetDeadline", func() {
		clientWS, _ := wsPair()
		conn := cluster.WebsocketConn(clientWS)

		Expect(conn.SetDeadline(time.Now().Add(-time.Second))).To(Succeed())

		_, err := conn.Read(make([]byte, 8))
		Expect(os.IsTimeout(err)).To(BeTrue(), "read: want a timeout, got %v", err)
		_, err = conn.Write([]byte("x"))
		Expect(os.IsTimeout(err)).To(BeTrue(), "write: want a timeout, got %v", err)
	})

	It("lets a deadline be armed while another goroutine writes", func() {
		// Task 5's relay arms an idle deadline from a supervisor goroutine while
		// yamux's send loop writes. gorilla keeps the write deadline in a plain
		// struct field, so this is a data race unless the adapter serialises it;
		// the spec is here to be run under -race, where it would report one.
		clientWS, _ := wsPair()
		conn := cluster.WebsocketConn(clientWS)

		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)
			for i := 0; i < 200; i++ {
				_, _ = conn.Write([]byte("ping"))
			}
		}()
		for i := 0; i < 200; i++ {
			_ = conn.SetWriteDeadline(time.Now().Add(time.Minute))
		}
		Eventually(done, "20s").Should(BeClosed())
	})
})

var _ = Describe("Peer link payloads", func() {
	It("carries a payload far larger than one yamux frame end to end", func() {
		sessions := make(chan *yamux.Session, 1)
		e := echo.New()
		servePeerRoute(e, "peer-token", func(_ string, s *yamux.Session) { sessions <- s })
		srv := httptest.NewServer(e)
		DeferCleanup(srv.Close)

		h := http.Header{}
		h.Set("Authorization", "Bearer peer-token")
		conn, _, err := websocket.DefaultDialer.Dial(
			"ws"+strings.TrimPrefix(srv.URL, "http")+"/api/cluster/peer?id=peer-1", h)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })

		var serverSess *yamux.Session
		Eventually(sessions, "5s").Should(Receive(&serverSess))

		clientSess, err := yamux.Client(cluster.WebsocketConn(conn), nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = clientSess.Close() })

		payload := make([]byte, 1<<20)
		_, err = rand.Read(payload)
		Expect(err).ToNot(HaveOccurred())

		go func() {
			defer GinkgoRecover()
			st, e := clientSess.OpenStream(GinkgoT().Context())
			if e != nil {
				return
			}
			defer func() { _ = st.Close() }()
			_, _ = io.Copy(st, bytes.NewReader(payload))
		}()

		received := make(chan []byte, 1)
		go func() {
			defer GinkgoRecover()
			st, e := serverSess.AcceptStream()
			if e != nil {
				return
			}
			buf := make([]byte, len(payload))
			if _, e := io.ReadFull(st, buf); e == nil {
				received <- buf
			}
		}()

		var got []byte
		Eventually(received, "30s").Should(Receive(&got))
		Expect(bytes.Equal(got, payload)).To(BeTrue())
	})
})
