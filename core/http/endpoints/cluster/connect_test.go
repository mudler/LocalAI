package cluster_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	clustersvc "github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// workerToken is the secret one worker holds. It is stored on that worker's row
// as a hash, never in a config value, which is the whole point of the check the
// specs below pin: a second worker's token, and the deployment-wide
// registration token, are both wrong for this node.
const workerToken = "worker-1-secret"

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// bearer builds the header a worker dials with.
func bearer(token string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	return h
}

// wsConnectURL is the worker tunnel route on a test server, named as nodeID.
func wsConnectURL(s *httptest.Server, nodeID string) string {
	return "ws" + strings.TrimPrefix(s.URL, "http") + clustersvc.ConnectPath +
		"?id=" + url.QueryEscape(nodeID)
}

var _ = Describe("Worker tunnel handler", func() {
	var (
		srv    *httptest.Server
		db     *gorm.DB
		reg    *clustersvc.Registry
		tun    *clustersvc.TunnelRegistry
		nodeID string
		ctx    context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()

		nodeReg, err := nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		node := &nodes.BackendNode{
			Name:      "worker-1",
			Address:   "10.0.0.9:50051",
			TokenHash: tokenHash(workerToken),
		}
		Expect(nodeReg.Register(ctx, node, true)).To(Succeed())
		nodeID = node.ID
		Expect(nodeID).ToNot(BeEmpty())

		reg = clustersvc.NewRegistry(db)
		Expect(reg.Register(ctx, "me", "10.0.0.1:8080", "v1")).To(Succeed())
		tun = clustersvc.NewTunnelRegistry(reg, "me")

		e := echo.New()
		routes.RegisterWorkerTunnelRoute(e, nodeReg, tun)
		srv = httptest.NewServer(e)
		DeferCleanup(srv.Close)
	})

	It("refuses an anonymous dial before upgrading", func() {
		// A plain GET, not a WebSocket dial: this is exactly what the
		// route-coverage test issues, and a handler that upgrades first answers
		// it with gorilla's own 400 handshake failure instead of a 401.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+clustersvc.ConnectPath+"?id="+nodeID, nil)
		Expect(err).ToNot(HaveOccurred())
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		Expect(resp.Header.Get("Upgrade")).To(BeEmpty(),
			"the handler upgraded an unauthenticated dial")
		Expect(tun.Held()).To(BeEmpty())
	})

	It("refuses a dial that carries no credentials at all", func() {
		_, resp, err := websocket.DefaultDialer.Dial(wsConnectURL(srv, nodeID), nil)
		Expect(err).To(HaveOccurred())
		Expect(resp).ToNot(BeNil())
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		Expect(tun.Held()).To(BeEmpty())
	})

	It("refuses a token that is not the node's own", func() {
		// The registration token is the credential every worker in the
		// deployment holds. Accepting it here would mean one leaked shared
		// secret impersonates any worker whose ID an attacker can read.
		_, resp, err := websocket.DefaultDialer.Dial(wsConnectURL(srv, nodeID), bearer("deployment-registration-token"))
		Expect(err).To(HaveOccurred())
		Expect(resp).ToNot(BeNil())
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		Expect(tun.Held()).To(BeEmpty())
	})

	It("refuses a dial that names a node it has never seen", func() {
		_, resp, err := websocket.DefaultDialer.Dial(wsConnectURL(srv, "no-such-node"), bearer(workerToken))
		Expect(err).To(HaveOccurred())
		Expect(resp).ToNot(BeNil())
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		Expect(tun.Held()).To(BeEmpty())
	})

	It("refuses an authenticated dial that names no node", func() {
		_, resp, err := websocket.DefaultDialer.Dial(
			"ws"+strings.TrimPrefix(srv.URL, "http")+clustersvc.ConnectPath, bearer(workerToken))
		Expect(err).To(HaveOccurred())
		Expect(resp).ToNot(BeNil())
		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
	})

	It("attaches an authenticated worker and carries bytes to it", func() {
		conn, _, err := websocket.DefaultDialer.Dial(wsConnectURL(srv, nodeID), bearer(workerToken))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = conn.Close() })

		// The worker is the side that dials, so its half of the mux is the
		// yamux CLIENT and the frontend's is the server.
		workerSess, err := yamux.Client(clustersvc.WebsocketConn(conn), nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = workerSess.Close() })

		Eventually(tun.Held, "10s").Should(ConsistOf(nodeID))

		owner, _, err := reg.OwnerRow(ctx, nodeID)
		Expect(err).ToNot(HaveOccurred())
		Expect(owner).To(Equal("me"),
			"the tunnel was stored without the claim that tells other replicas where it is")

		go func() {
			defer GinkgoRecover()
			stream, aerr := workerSess.AcceptStream()
			if aerr != nil {
				return
			}
			defer func() { _ = stream.Close() }()
			buf := make([]byte, 4)
			if _, rerr := stream.Read(buf); rerr != nil {
				return
			}
			_, _ = stream.Write(buf)
		}()

		stream, err := tun.Open(ctx, nodeID)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = stream.Close() })
		_, err = stream.Write([]byte("ping"))
		Expect(err).ToNot(HaveOccurred())
		echoed := make([]byte, 4)
		_, err = stream.Read(echoed)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(echoed)).To(Equal("ping"))
	})

	It("detaches the tunnel and drops its claim when the worker goes away", func() {
		conn, _, err := websocket.DefaultDialer.Dial(wsConnectURL(srv, nodeID), bearer(workerToken))
		Expect(err).ToNot(HaveOccurred())
		Eventually(tun.Held, "10s").Should(ConsistOf(nodeID))

		Expect(conn.Close()).To(Succeed())

		Eventually(tun.Held, "10s").Should(BeEmpty(),
			"a dead tunnel is still held here, so every dialer routed to this replica gets a socket that carries nothing")
		Eventually(func() error {
			_, _, err := reg.OwnerRow(ctx, nodeID)
			return err
		}, "10s").Should(MatchError(clustersvc.ErrNoConnection),
			"the claim outlived the socket, so this replica keeps being named the owner of a worker it no longer holds")
	})

	It("reports a lookup failure as a failure, not as a refusal", func() {
		// ErrNotOwner, 401 and 404 are all ANSWERS. A database that cannot be
		// read is none of them: telling a worker its credentials are wrong when
		// the frontend simply could not look them up sends it re-registering
		// instead of retrying.
		Expect(db.Exec(`DROP TABLE backend_nodes CASCADE`).Error).To(Succeed())

		_, resp, err := websocket.DefaultDialer.Dial(wsConnectURL(srv, nodeID), bearer(workerToken))
		Expect(err).To(HaveOccurred())
		Expect(resp).ToNot(BeNil())
		Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
	})
})

var _ = Describe("Worker tunnel handler without distributed mode", func() {
	// The route is registered in every deployment so that the route-coverage
	// test sees it, which is what pins the reject-before-upgrade rule. With no
	// node registry there is nothing to authenticate against, so it must refuse
	// every dial rather than publish an unauthenticated multiplexer.
	var srv *httptest.Server

	BeforeEach(func() {
		e := echo.New()
		routes.RegisterWorkerTunnelRoute(e, nil, nil)
		srv = httptest.NewServer(e)
		DeferCleanup(srv.Close)
	})

	It("refuses an anonymous dial with 401", func() {
		req, err := http.NewRequestWithContext(GinkgoT().Context(), http.MethodGet, srv.URL+clustersvc.ConnectPath, nil)
		Expect(err).ToNot(HaveOccurred())
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
	})

	It("tells a credentialed worker the frontend has no cluster, rather than rejecting it", func() {
		// A single-binary frontend cannot authenticate anybody, and saying
		// "unauthorized" would send an operator hunting a token problem that
		// does not exist.
		_, resp, err := websocket.DefaultDialer.Dial(wsConnectURL(srv, "w1"), bearer(workerToken))
		Expect(err).To(HaveOccurred())
		Expect(resp).ToNot(BeNil())
		Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable))
	})
})

var _ = Describe("Worker tunnel auth coverage", func() {
	// The same argument the peer link's coverage specs make: the tunnel route
	// authenticates a worker against its own stored token, not a session, so it
	// only works while its path sits under the prefix the global auth
	// middleware exempts.
	var srv *httptest.Server

	BeforeEach(func() {
		e := echo.New()
		// A nil DB with one legacy API key is the cheapest configuration that
		// turns the middleware ON without a database.
		e.Use(auth.Middleware(nil, &config.ApplicationConfig{ApiKeys: []string{"an-api-key"}}))
		routes.RegisterWorkerTunnelRoute(e, nil, nil)
		e.GET("/api/nodes", func(c echo.Context) error { return c.NoContent(http.StatusOK) })
		srv = httptest.NewServer(e)
		DeferCleanup(srv.Close)
	})

	It("refuses an uncredentialed request to a route outside the cluster prefix", func() {
		resp, err := http.Get(srv.URL + "/api/nodes")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized),
			"the global auth middleware is not actually guarding this server, so the assertion below would prove nothing")
	})

	It("lets a worker dial reach the handler, which is the only thing that can authenticate it", func() {
		// The worker's token is not one of the API keys the middleware knows,
		// so a 503 from the handler's own no-cluster check can only mean the
		// request was let through by the middleware.
		req, err := http.NewRequestWithContext(GinkgoT().Context(), http.MethodGet, srv.URL+clustersvc.ConnectPath+"?id=w1", nil)
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Authorization", "Bearer "+workerToken)

		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable),
			"a worker dial must reach the handler; 401 here means the tunnel route left the auth-exempt prefix %q", auth.ClusterPathPrefix)
	})
})
