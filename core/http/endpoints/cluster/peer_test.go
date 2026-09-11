package cluster_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	clustersvc "github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"

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

// peerHeaders builds what a legitimate replica dials with: the deployment's
// shared cluster token AND its own peer credential. Both are always sent,
// because the handler checks both and a spec that sent one would be pinning
// half the door.
func peerHeaders(token string, cred clustersvc.PeerCredential) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set(clustersvc.PeerIdentityHeader, cred.Token())
	return h
}

var _ = Describe("Peer link handler", func() {
	var (
		srv      *httptest.Server
		sessions chan *yamux.Session
		reg      *clustersvc.Registry
		cred     clustersvc.PeerCredential
		ctx      context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		sessions = make(chan *yamux.Session, 1)

		db := testutil.SetupTestDB()
		Expect(clustersvc.Migrate(ctx, db)).To(Succeed())
		reg = clustersvc.NewRegistry(db)
		// peer-1 is the replica every dial below claims to be, and this is the
		// credential that claim is checked against.
		cred = clustersvc.NewPeerCredential()
		Expect(reg.Register(ctx, "peer-1", "10.0.0.1:8080", "v1", cred.Hash())).To(Succeed())

		e := echo.New()
		routes.RegisterClusterRoutes(e, "peer-token", reg, func(_ string, s *yamux.Session) {
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
		conn, _, err := websocket.DefaultDialer.Dial(wsPeerURL(srv), peerHeaders("peer-token", cred))
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

	It("reports the peer id it proved", func() {
		ids := make(chan string, 1)
		e := echo.New()
		routes.RegisterClusterRoutes(e, "peer-token", reg, func(id string, _ *yamux.Session) { ids <- id })
		s2 := httptest.NewServer(e)
		DeferCleanup(s2.Close)

		conn, _, err := websocket.DefaultDialer.Dial(wsPeerURL(s2), peerHeaders("peer-token", cred))
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
		routes.RegisterClusterRoutes(e, "", reg, func(_ string, sess *yamux.Session) { accepted <- sess })
		s2 := httptest.NewServer(e)
		DeferCleanup(s2.Close)

		for _, header := range []http.Header{nil, {"Authorization": []string{"Bearer "}}, {"Authorization": []string{"Bearer anything"}}, peerHeaders("", cred), peerHeaders("anything", cred)} {
			_, resp, err := websocket.DefaultDialer.Dial(wsPeerURL(s2), header)
			Expect(err).To(HaveOccurred())
			Expect(resp).ToNot(BeNil())
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		}
		Expect(accepted).ToNot(Receive())
	})

	It("accepts the bearer scheme in any case", func() {
		// RFC 7235 makes the scheme case-insensitive. The token after it is not.
		h := peerHeaders("peer-token", cred)
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
		routes.RegisterClusterRoutes(e, "peer-token", reg, func(_ string, _ *yamux.Session) {
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

		conn, _, err := websocket.DefaultDialer.Dial(wsPeerURL(s2), peerHeaders("peer-token", cred))
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
		_, resp, err := websocket.DefaultDialer.Dial(
			"ws"+strings.TrimPrefix(srv.URL, "http")+"/api/cluster/peer", peerHeaders("peer-token", cred))
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
		cred     clustersvc.PeerCredential
	)

	BeforeEach(func() {
		ctx := context.Background()
		sessions = make(chan *yamux.Session, 1)
		db := testutil.SetupTestDB()
		Expect(clustersvc.Migrate(ctx, db)).To(Succeed())
		reg := clustersvc.NewRegistry(db)
		cred = clustersvc.NewPeerCredential()
		Expect(reg.Register(ctx, "peer-1", "10.0.0.1:8080", "v1", cred.Hash())).To(Succeed())

		e := echo.New()
		// A nil DB with one legacy API key is the cheapest configuration that
		// turns the middleware ON without a database. With neither, Middleware
		// short-circuits to next() and every assertion below would pass against
		// a server that has no auth at all.
		e.Use(auth.Middleware(nil, &config.ApplicationConfig{ApiKeys: []string{"an-api-key"}}))
		routes.RegisterClusterRoutes(e, "peer-token", reg, func(_ string, s *yamux.Session) { sessions <- s })
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
		conn, _, err := websocket.DefaultDialer.Dial(wsPeerURL(srv), peerHeaders("peer-token", cred))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })
		Eventually(sessions, "5s").Should(Receive())
	})
})

var _ = Describe("Peer link identity", func() {
	// The status codes a peer's own retry loop reads, and the order they are
	// decided in. Every refusal here happens BEFORE the WebSocket upgrade: a
	// handler that upgraded first would answer with a WebSocket error instead
	// of a status, and neither an operator nor a dialler could tell an
	// authorization failure from a transport one.
	var (
		srv      *httptest.Server
		sessions chan *yamux.Session
		reg      *clustersvc.Registry
		cred     clustersvc.PeerCredential
		ctx      context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		sessions = make(chan *yamux.Session, 1)
		db := testutil.SetupTestDB()
		Expect(clustersvc.Migrate(ctx, db)).To(Succeed())
		reg = clustersvc.NewRegistry(db)
		cred = clustersvc.NewPeerCredential()
		Expect(reg.Register(ctx, "peer-1", "10.0.0.1:8080", "v1", cred.Hash())).To(Succeed())

		e := echo.New()
		routes.RegisterClusterRoutes(e, "peer-token", reg, func(_ string, s *yamux.Session) { sessions <- s })
		srv = httptest.NewServer(e)
		DeferCleanup(srv.Close)
	})

	// dialPeer returns the handshake status a dial was answered with.
	dialPeer := func(target *httptest.Server, h http.Header) int {
		GinkgoHelper()
		conn, resp, err := websocket.DefaultDialer.Dial(wsPeerURL(target), h)
		if err == nil {
			DeferCleanup(func() { _ = conn.Close() })
			Expect(resp).ToNot(BeNil())
			return resp.StatusCode
		}
		Expect(resp).ToNot(BeNil())
		Expect(resp.Header.Get("Upgrade")).To(BeEmpty(),
			"the handler upgraded a dial it then refused; a peer reads the status, not a WebSocket error")
		return resp.StatusCode
	}

	It("refuses a dial that carries the shared token and no peer credential", func() {
		// This is the mixed-version case, and the answer is deliberately a
		// refusal rather than a fallback. A replica running a release from
		// before peer credentials sends no such header, and so does an attacker
		// holding the shared token: there is no way to accept the first without
		// accepting the second, so both are refused and the handler logs which
		// replica it refused and why.
		h := http.Header{}
		h.Set("Authorization", "Bearer peer-token")
		Expect(dialPeer(srv, h)).To(Equal(http.StatusUnauthorized))
		Expect(sessions).ToNot(Receive())
	})

	It("refuses a dial that presents an empty peer credential", func() {
		h := peerHeaders("peer-token", clustersvc.PeerCredential{})
		Expect(dialPeer(srv, h)).To(Equal(http.StatusUnauthorized))
		Expect(sessions).ToNot(Receive())
	})

	It("refuses a dial that declares a real replica with the wrong credential", func() {
		Expect(dialPeer(srv, peerHeaders("peer-token", clustersvc.NewPeerCredential()))).
			To(Equal(http.StatusUnauthorized))
		Expect(sessions).ToNot(Receive())
	})

	It("refuses a dial whose declared replica publishes no credential", func() {
		Expect(reg.Register(ctx, "peer-1", "10.0.0.1:8080", "v1", "")).To(Succeed())
		Expect(dialPeer(srv, peerHeaders("peer-token", cred))).To(Equal(http.StatusUnauthorized))
		Expect(sessions).ToNot(Receive())
	})

	It("accepts a dial that proves the replica it declares", func() {
		Expect(dialPeer(srv, peerHeaders("peer-token", cred))).To(Equal(http.StatusSwitchingProtocols))
		Eventually(sessions, "5s").Should(Receive())
	})

	It("answers 503, not 401, when there is no cluster registry to check against", func() {
		// A frontend with nothing to resolve replica ids against cannot
		// authenticate anybody. Answering "unauthorized" would send an operator
		// hunting a token problem that does not exist, and accepting would
		// publish an unauthenticated multiplexer, so it does neither.
		e := echo.New()
		accepted := make(chan *yamux.Session, 1)
		routes.RegisterClusterRoutes(e, "peer-token", nil, func(_ string, s *yamux.Session) { accepted <- s })
		s2 := httptest.NewServer(e)
		DeferCleanup(s2.Close)

		Expect(dialPeer(s2, peerHeaders("peer-token", cred))).To(Equal(http.StatusServiceUnavailable))
		Expect(accepted).ToNot(Receive())
	})

	It("answers 500, not 401, when the replica lookup itself fails", func() {
		// A query that FAILED is neither a rejection nor an absence. Telling a
		// healthy replica its credentials are wrong because the database was
		// unreadable sends it re-registering; nothing above may read it as a
		// worker having gone away either.
		db := testutil.SetupTestDB()
		Expect(clustersvc.Migrate(ctx, db)).To(Succeed())
		broken := clustersvc.NewRegistry(db)
		Expect(broken.Register(ctx, "peer-1", "10.0.0.1:8080", "v1", cred.Hash())).To(Succeed())
		sqlDB, err := db.DB()
		Expect(err).ToNot(HaveOccurred())
		Expect(sqlDB.Close()).To(Succeed())

		e := echo.New()
		accepted := make(chan *yamux.Session, 1)
		routes.RegisterClusterRoutes(e, "peer-token", broken, func(_ string, s *yamux.Session) { accepted <- s })
		s2 := httptest.NewServer(e)
		DeferCleanup(s2.Close)

		Expect(dialPeer(s2, peerHeaders("peer-token", cred))).To(Equal(http.StatusInternalServerError))
		Expect(accepted).ToNot(Receive())
	})

	It("refuses an anonymous dial before it looks anything up", func() {
		// Ordering: the shared token is still the first gate, unchanged, so a
		// dial with no Authorization at all is a 401 and not a 400 about an id
		// or a 401 about a credential. The route-coverage test issues exactly
		// this shape.
		Expect(dialPeer(srv, nil)).To(Equal(http.StatusUnauthorized))
	})
})
