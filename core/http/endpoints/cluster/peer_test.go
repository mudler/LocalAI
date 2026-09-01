package cluster_test

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	clustersvc "github.com/mudler/LocalAI/core/services/cluster"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// wsPeerURL is the peer route on a test server, named as peer-1.
func wsPeerURL(s *httptest.Server) string {
	return "ws" + strings.TrimPrefix(s.URL, "http") + clustersvc.PeerPath + "?id=peer-1"
}

var _ = Describe("Peer link handler", func() {
	var (
		srv      *httptest.Server
		sessions chan *yamux.Session
	)

	BeforeEach(func() {
		sessions = make(chan *yamux.Session, 1)
		e := echo.New()
		routes.RegisterClusterRoutes(e, "peer-token", func(_ string, s *yamux.Session) {
			sessions <- s
		})
		srv = httptest.NewServer(e)
		DeferCleanup(srv.Close)
	})

	It("rejects a connection with no token", func() {
		_, resp, err := websocket.DefaultDialer.Dial(wsPeerURL(srv), nil)
		Expect(err).To(HaveOccurred())
		Expect(resp).ToNot(BeNil())
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("rejects a connection with the wrong token", func() {
		h := http.Header{}
		h.Set("Authorization", "Bearer wrong")
		_, resp, err := websocket.DefaultDialer.Dial(wsPeerURL(srv), h)
		Expect(err).To(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("accepts an authenticated peer and yields a usable yamux session", func() {
		h := http.Header{}
		h.Set("Authorization", "Bearer peer-token")
		conn, _, err := websocket.DefaultDialer.Dial(wsPeerURL(srv), h)
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
		routes.RegisterClusterRoutes(e, "peer-token", func(id string, _ *yamux.Session) { ids <- id })
		s2 := httptest.NewServer(e)
		DeferCleanup(s2.Close)

		conn, _, err := websocket.DefaultDialer.Dial(wsPeerURL(s2), h)
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
		routes.RegisterClusterRoutes(e, "", func(_ string, sess *yamux.Session) { accepted <- sess })
		s2 := httptest.NewServer(e)
		DeferCleanup(s2.Close)

		for _, header := range []http.Header{nil, {"Authorization": []string{"Bearer "}}, {"Authorization": []string{"Bearer anything"}}} {
			_, resp, err := websocket.DefaultDialer.Dial(wsPeerURL(s2), header)
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
		conn, _, err := websocket.DefaultDialer.Dial(wsPeerURL(srv), h)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })
		Eventually(sessions, "5s").Should(Receive())
	})

	It("closes the session when the callback panics", func() {
		// net/http recovers the panic but leaves the hijacked socket open, so
		// without the handler's own recover the peer would keep a link nobody
		// ever accepts streams on.
		e := echo.New()
		routes.RegisterClusterRoutes(e, "peer-token", func(_ string, _ *yamux.Session) {
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
		conn, _, err := websocket.DefaultDialer.Dial(wsPeerURL(s2), h)
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

var _ = Describe("Peer link auth coverage", func() {
	// These specs put the REAL global auth middleware in front of the REAL
	// registrar and prove a peer dial reaches the handler anyway. The peer link
	// authenticates with the cluster token, not a session, so it only works
	// while its path sits under the prefix auth exempts; moving either one
	// alone 401s every peer dial, and the two live in packages that must not
	// import each other.
	//
	// The predicate that grants the exemption is unexported, so this asserts on
	// its effect rather than on it: what a caller can observe is whether the
	// request reaches the handler.
	var (
		srv      *httptest.Server
		sessions chan *yamux.Session
	)

	BeforeEach(func() {
		sessions = make(chan *yamux.Session, 1)
		e := echo.New()
		// A nil DB with one legacy API key is the cheapest configuration that
		// turns the middleware ON without a database. With neither, Middleware
		// short-circuits to next() and every assertion below would pass against
		// a server that has no auth at all.
		e.Use(auth.Middleware(nil, &config.ApplicationConfig{ApiKeys: []string{"an-api-key"}}))
		routes.RegisterClusterRoutes(e, "peer-token", func(_ string, s *yamux.Session) { sessions <- s })
		// A route outside the cluster prefix, registered on the same server, is
		// the control: it proves the middleware in front of both is live.
		e.GET("/api/nodes", func(c echo.Context) error { return c.NoContent(http.StatusOK) })
		srv = httptest.NewServer(e)
		DeferCleanup(srv.Close)
	})

	It("refuses an uncredentialed request to a route outside the cluster prefix", func() {
		resp, err := http.Get(srv.URL + "/api/nodes")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized),
			"the global auth middleware is not actually guarding this server, so the peer-route assertions below would prove nothing")
	})

	It("lets a peer dial reach the handler, which is the only thing that can authenticate it", func() {
		// The cluster token is not one of the API keys the middleware knows, so
		// a 400 from the handler's own missing-id check can only mean the
		// request was let through unauthenticated by the middleware.
		req, err := http.NewRequestWithContext(GinkgoT().Context(), http.MethodGet, srv.URL+clustersvc.PeerPath, nil)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Authorization", "Bearer peer-token")

		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest),
			"a peer dial must reach the handler; 401 here means the peer route left the auth-exempt prefix %q", auth.ClusterPathPrefix)
	})

	It("completes a full peer handshake through the guarded server", func() {
		// The status-code assertion above cannot see the upgrade, and the
		// upgrade is what a peer actually does.
		h := http.Header{}
		h.Set("Authorization", "Bearer peer-token")
		conn, _, err := websocket.DefaultDialer.Dial(wsPeerURL(srv), h)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })
		Eventually(sessions, "5s").Should(Receive())
	})
})
