package cluster_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	clusterep "github.com/mudler/LocalAI/core/http/endpoints/cluster"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Peer link handler", func() {
	var (
		srv      *httptest.Server
		sessions chan *yamux.Session
	)

	BeforeEach(func() {
		sessions = make(chan *yamux.Session, 1)
		e := echo.New()
		clusterep.RegisterClusterRoutes(e, "peer-token", func(_ string, s *yamux.Session) {
			sessions <- s
		})
		srv = httptest.NewServer(e)
		DeferCleanup(srv.Close)
	})

	wsURL := func(s *httptest.Server) string {
		return "ws" + strings.TrimPrefix(s.URL, "http") + "/api/cluster/peer?id=peer-1"
	}

	It("rejects a connection with no token", func() {
		_, resp, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
		Expect(err).To(HaveOccurred())
		Expect(resp).ToNot(BeNil())
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("rejects a connection with the wrong token", func() {
		h := http.Header{}
		h.Set("Authorization", "Bearer wrong")
		_, resp, err := websocket.DefaultDialer.Dial(wsURL(srv), h)
		Expect(err).To(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("accepts an authenticated peer and yields a usable yamux session", func() {
		h := http.Header{}
		h.Set("Authorization", "Bearer peer-token")
		conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv), h)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })

		var serverSess *yamux.Session
		Eventually(sessions, "5s").Should(Receive(&serverSess))
		Expect(serverSess).ToNot(BeNil())

		// The client wraps its side as a yamux CLIENT and opens a stream; the
		// server must accept it. This proves the WebSocket was adapted into a
		// stream-oriented conn correctly, which is the part most likely to be
		// subtly wrong.
		clientSess, err := yamux.Client(clusterep.WebsocketConn(conn), nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = clientSess.Close() })

		go func() {
			defer GinkgoRecover()
			st, e := clientSess.OpenStream(GinkgoT().Context())
			if e == nil {
				_, _ = st.Write([]byte("hello"))
			}
		}()

		accepted := make(chan []byte, 1)
		go func() {
			defer GinkgoRecover()
			st, e := serverSess.AcceptStream()
			if e != nil {
				return
			}
			buf := make([]byte, 5)
			if _, e := st.Read(buf); e == nil {
				accepted <- buf
			}
		}()
		Eventually(accepted, "10s").Should(Receive(Equal([]byte("hello"))))
	})

	It("reports the peer id it was given", func() {
		h := http.Header{}
		h.Set("Authorization", "Bearer peer-token")
		ids := make(chan string, 1)
		e := echo.New()
		clusterep.RegisterClusterRoutes(e, "peer-token", func(id string, _ *yamux.Session) { ids <- id })
		s2 := httptest.NewServer(e)
		DeferCleanup(s2.Close)

		conn, _, err := websocket.DefaultDialer.Dial(wsURL(s2), h)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })

		Eventually(ids, "5s").Should(Receive(Equal("peer-1")))
	})
})

var _ = Describe("Peer link auth prefix", func() {
	It("is covered by the alternative-authentication prefix list", func() {
		// /api/cluster/ authenticates with the cluster token, not the global
		// session middleware, so it must be listed or every peer dial 401s.
		Expect(clusterep.AlternativeAuthPrefix).To(Equal("/api/cluster/"))
	})
})
