package cluster_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	clusterep "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	clustersvc "github.com/mudler/LocalAI/core/services/cluster"

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
		clientSess, err := yamux.Client(clustersvc.WebsocketConn(conn), nil, nil)
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
	It("rejects every dial when no cluster token is configured", func() {
		// The route is registered in every deployment, so an empty configured
		// token must authorize nobody. Failing open the way the worker
		// file-transfer server's checkBearerToken does would publish an
		// unauthenticated yamux multiplexer to anyone who can reach the port.
		e := echo.New()
		accepted := make(chan *yamux.Session, 1)
		clusterep.RegisterClusterRoutes(e, "", func(_ string, sess *yamux.Session) { accepted <- sess })
		s2 := httptest.NewServer(e)
		DeferCleanup(s2.Close)

		for _, header := range []http.Header{nil, {"Authorization": []string{"Bearer "}}, {"Authorization": []string{"Bearer anything"}}} {
			_, resp, err := websocket.DefaultDialer.Dial(wsURL(s2), header)
			Expect(err).To(HaveOccurred())
			Expect(resp).ToNot(BeNil())
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		}
		Expect(accepted).ToNot(Receive())
	})

	It("accepts the bearer scheme in any case", func() {
		// RFC 7235 makes the scheme case-insensitive. The token after it is not.
		h := http.Header{}
		h.Set("Authorization", "bearer peer-token")
		conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv), h)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })
		Eventually(sessions, "5s").Should(Receive())
	})

	It("closes the session when the callback panics", func() {
		// net/http recovers the panic but leaves the hijacked socket open, so
		// without the handler's own recover the peer would keep a link nobody
		// ever accepts streams on.
		e := echo.New()
		clusterep.RegisterClusterRoutes(e, "peer-token", func(_ string, _ *yamux.Session) {
			panic("callback exploded")
		})
		s2 := httptest.NewServer(e)
		DeferCleanup(func() {
			// A hijacked connection the handler never closed would park
			// httptest's Close forever, turning the assertion below into a
			// suite hang. Forcing the conns shut keeps the failure legible.
			s2.CloseClientConnections()
			s2.Close()
		})

		h := http.Header{}
		h.Set("Authorization", "Bearer peer-token")
		conn, _, err := websocket.DefaultDialer.Dial(wsURL(s2), h)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })

		clientSess, err := yamux.Client(clustersvc.WebsocketConn(conn), nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = clientSess.Close() })

		// Asserting on OpenStream would hang rather than fail: yamux only
		// acknowledges a stream once the peer accepts it, and the leak this
		// pins is precisely that nobody ever will. The session's own liveness
		// is the observable that answers in both directions.
		Eventually(clientSess.IsClosed, "10s").Should(BeTrue())
	})

	It("rejects an authenticated dial that names no peer", func() {
		// The session is keyed by peer id, so a nameless link could never be
		// looked up again; refusing it is cheaper than leaking it.
		h := http.Header{}
		h.Set("Authorization", "Bearer peer-token")
		_, resp, err := websocket.DefaultDialer.Dial(
			"ws"+strings.TrimPrefix(srv.URL, "http")+"/api/cluster/peer", h)
		Expect(err).To(HaveOccurred())
		Expect(resp).ToNot(BeNil())
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		Expect(sessions).ToNot(Receive())
	})
})

var _ = Describe("Peer link auth prefix", func() {
	It("keeps the peer route inside the alternative-authentication prefix", func() {
		// The peer route authenticates with the cluster token, not the global
		// session middleware, which only holds while the route sits under the
		// prefix auth exempts. Moving either one alone 401s every peer dial.
		//
		// This lives here because it is the only package that can see both:
		// core/services/cluster owns the route and must stay free of any
		// core/http dependency, and core/http/auth owns the exemption.
		Expect(strings.HasPrefix(clustersvc.PeerPath, auth.ClusterPathPrefix)).To(BeTrue(),
			"peer route %q is no longer under the auth-exempt prefix %q",
			clustersvc.PeerPath, auth.ClusterPathPrefix)
	})
})
