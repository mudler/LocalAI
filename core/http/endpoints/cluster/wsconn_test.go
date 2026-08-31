package cluster_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	clusterep "github.com/mudler/LocalAI/core/http/endpoints/cluster"

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
		writer := clusterep.WebsocketConn(clientWS)
		reader := clusterep.WebsocketConn(serverWS)

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
		writer := clusterep.WebsocketConn(clientWS)
		reader := clusterep.WebsocketConn(serverWS)

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
		writer := clusterep.WebsocketConn(clientWS)
		reader := clusterep.WebsocketConn(serverWS)

		Expect(reader.SetReadDeadline(time.Now().Add(10 * time.Second))).To(Succeed())

		for _, chunk := range []string{"abc", "", "de", "fghij"} {
			_, err := writer.Write([]byte(chunk))
			Expect(err).ToNot(HaveOccurred())
		}

		// A read spanning three messages must be satisfied, and the empty
		// message must not surface as a premature (0, nil) or an EOF.
		got := make([]byte, 10)
		_, err := io.ReadFull(reader, got)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(got)).To(Equal("abcdefghij"))
	})

	It("reports a clean peer close as io.EOF", func() {
		clientWS, serverWS := wsPair()
		reader := clusterep.WebsocketConn(serverWS)

		Expect(clientWS.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))).To(Succeed())

		_, err := reader.Read(make([]byte, 8))
		Expect(err).To(MatchError(io.EOF))
	})

	It("refuses a text message rather than desynchronising the stream", func() {
		clientWS, serverWS := wsPair()
		reader := clusterep.WebsocketConn(serverWS)

		Expect(clientWS.WriteMessage(websocket.TextMessage, []byte("not a frame"))).To(Succeed())

		_, err := reader.Read(make([]byte, 32))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("want binary"))
	})

	It("satisfies net.Conn, including the deadlines yamux sets on every write", func() {
		clientWS, _ := wsPair()
		var conn net.Conn = clusterep.WebsocketConn(clientWS)

		Expect(conn.LocalAddr()).ToNot(BeNil())
		Expect(conn.RemoteAddr()).ToNot(BeNil())
		// yamux's sendLoop calls SetWriteDeadline before every flush, so an
		// adapter that dropped the call would let a stalled peer block the
		// session forever instead of failing it.
		Expect(conn.SetWriteDeadline(time.Now().Add(time.Minute))).To(Succeed())
		Expect(conn.SetReadDeadline(time.Now().Add(time.Minute))).To(Succeed())
		Expect(conn.SetDeadline(time.Time{})).To(Succeed())
	})
})

var _ = Describe("Peer link payloads", func() {
	It("carries a payload far larger than one yamux frame end to end", func() {
		sessions := make(chan *yamux.Session, 1)
		e := echo.New()
		clusterep.RegisterClusterRoutes(e, "peer-token", func(_ string, s *yamux.Session) { sessions <- s })
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

		clientSess, err := yamux.Client(clusterep.WebsocketConn(conn), nil, nil)
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
