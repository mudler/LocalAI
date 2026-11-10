package cluster_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	clusterapi "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/core/services/worker"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

func hashOf(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

var _ = Describe("Connect endpoint", func() {
	const (
		registrationToken = "shared-registration-token"
		nodeToken         = "own-tunnel-token-of-w1"
	)

	var (
		ctx      context.Context
		db       *gorm.DB
		clusterR *cluster.Registry
		nodeReg  *nodes.NodeRegistry
		tunnels  *tunnel.Registry
		srv      *httptest.Server
		nodeID   string
	)

	// register adds a node the way the registration endpoint does: the
	// registration token is stored as TokenHash, and the own credential of the
	// node as TunnelTokenHash.
	register := func(name string, autoApprove bool) *nodes.BackendNode {
		GinkgoHelper()
		node := &nodes.BackendNode{Name: name, NodeType: nodes.NodeTypeBackend, TokenHash: hashOf(registrationToken)}
		Expect(nodeReg.Register(ctx, node, autoApprove)).To(Succeed())
		return node
	}

	serve := func(registry *nodes.NodeRegistry, tunnels *tunnel.Registry) *httptest.Server {
		GinkgoHelper()
		e := echo.New()
		e.GET(tunnel.ConnectPath, clusterapi.ConnectHandler(registry, tunnels))
		s := httptest.NewServer(e)
		DeferCleanup(s.Close)
		return s
	}

	dial := func(query, token string, header ...http.Header) (*websocket.Conn, *http.Response, error) {
		h := http.Header{}
		if token != "" {
			h.Set("Authorization", "Bearer "+token)
		}
		for _, extra := range header {
			for k, v := range extra {
				h[k] = v
			}
		}
		url := "ws" + srv.URL[len("http"):] + tunnel.ConnectPath + query
		return tunnel.NewDialer(5*time.Second).Dial(url, h)
	}

	// plainGet is a request that is no websocket upgrade.
	plainGet := func(query, token string) *http.Response {
		GinkgoHelper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+tunnel.ConnectPath+query, nil)
		Expect(err).ToNot(HaveOccurred())
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(resp.Body.Close)
		return resp
	}

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR = cluster.NewRegistry(db)
		Expect(clusterR.Register(ctx, "replica-a", "test", 0, "")).To(Succeed())
		var err error
		nodeReg, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		tunnels = tunnel.NewRegistry(clusterR, "replica-a")

		node := register("w1", true)
		nodeID = node.ID
		Expect(nodeReg.SetTunnelTokenHash(ctx, nodeID, hashOf(nodeToken))).To(Succeed())

		srv = serve(nodeReg, tunnels)
	})

	Describe("the answers before the upgrade", func() {
		It("answers 401 to a dial with no credential, before anything else is checked", func() {
			// No query string at all: an anonymous dial must not get a 400.
			Expect(plainGet("", "").StatusCode).To(Equal(http.StatusUnauthorized))
			Expect(plainGet("?id="+nodeID, "").StatusCode).To(Equal(http.StatusUnauthorized))
			// Not even the 503 for a missing registry may come first.
			bare := serve(nil, nil)
			resp, err := http.Get(bare.URL + tunnel.ConnectPath)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(resp.Body.Close)
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("answers 503 to a credential when the frontend has no registry", func() {
			bare := serve(nil, nil)
			req, _ := http.NewRequest(http.MethodGet, bare.URL+tunnel.ConnectPath+"?id=x", nil)
			req.Header.Set("Authorization", "Bearer anything")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(resp.Body.Close)
			Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable))
		})

		It("answers 400 to a credential with no node id", func() {
			Expect(plainGet("", nodeToken).StatusCode).To(Equal(http.StatusBadRequest))
		})

		It("answers 401, not 404, for a node it does not know", func() {
			Expect(plainGet("?id=nobody", nodeToken).StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("answers 401 to a wrong token", func() {
			Expect(plainGet("?id="+nodeID, "not-the-token").StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("never accepts the shared registration token", func() {
			Expect(plainGet("?id="+nodeID, registrationToken).StatusCode).To(Equal(http.StatusUnauthorized))
			_, resp, err := dial("?id="+nodeID, registrationToken)
			Expect(err).To(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
			Expect(tunnels.Holds(nodeID)).To(BeFalse())
		})

		It("answers 401 to a node that has no tunnel credential, whatever it presents", func() {
			other := register("w-old", true)
			Expect(plainGet("?id="+other.ID, "").StatusCode).To(Equal(http.StatusUnauthorized))
			Expect(plainGet("?id="+other.ID, hashOf("")).StatusCode).To(Equal(http.StatusUnauthorized))
			Expect(plainGet("?id="+other.ID, registrationToken).StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("answers 403 to a node that waits for approval, and takes no tunnel from it", func() {
			pending := register("w-pending", false)
			Expect(pending.Status).To(Equal(nodes.StatusPending))
			Expect(nodeReg.SetTunnelTokenHash(ctx, pending.ID, hashOf("pending-token"))).To(Succeed())

			_, resp, err := dial("?id="+pending.ID, "pending-token")
			Expect(err).To(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
			Expect(tunnels.Holds(nodeID)).To(BeFalse())

			Expect(nodeReg.ApproveNode(ctx, pending.ID)).To(Succeed())
			ws, _, err := dial("?id="+pending.ID, "pending-token")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(ws.Close)
		})

		It("answers 500 and not 401 when the node cannot be read", func() {
			sqlDB, err := db.DB()
			Expect(err).ToNot(HaveOccurred())
			Expect(sqlDB.Close()).To(Succeed())
			Expect(plainGet("?id="+nodeID, nodeToken).StatusCode).To(Equal(http.StatusInternalServerError))
		})

		It("answers 400 to a lane it does not know", func() {
			Expect(plainGet("?id="+nodeID+"&lane=express", nodeToken).StatusCode).To(Equal(http.StatusBadRequest))
		})

		It("leaves the table alone when the request is no upgrade", func() {
			resp := plainGet("?id="+nodeID, nodeToken)
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
			_, _, err := clusterR.Owner(ctx, nodeID)
			Expect(err).To(MatchError(cluster.ErrNoConnection))
		})

		It("refuses a browser of another origin", func() {
			_, resp, err := dial("?id="+nodeID, nodeToken, http.Header{"Origin": []string{"https://evil.example"}})
			Expect(err).To(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
			Expect(tunnels.Holds(nodeID)).To(BeFalse())
		})
	})

	Describe("a dial with the own credential of the node", func() {
		It("claims the node for this replica and carries streams to the worker", func() {
			ws, _, err := dial("?id="+nodeID, nodeToken)
			Expect(err).ToNot(HaveOccurred())
			worker, err := tunnel.ClientSession(ws, tunnel.LaneInference)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(worker.Close)

			Eventually(func() bool { return tunnels.Holds(nodeID) }).Should(BeTrue())
			owner, _, err := clusterR.Owner(ctx, nodeID)
			Expect(err).ToNot(HaveOccurred())
			Expect(owner).To(Equal("replica-a"))

			go func() {
				for {
					st, err := worker.AcceptStream()
					if err != nil {
						return
					}
					go func() { defer func() { _ = st.Close() }(); _, _ = io.Copy(st, st) }()
				}
			}()
			st, err := tunnels.Open(ctx, nodeID, tunnel.LaneInference)
			Expect(err).ToNot(HaveOccurred())
			_, err = st.Write([]byte("ping"))
			Expect(err).ToNot(HaveOccurred())
			got := make([]byte, 4)
			_, err = io.ReadFull(st, got)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(got)).To(Equal("ping"))
		})

		It("releases the claim when the worker goes away", func() {
			ws, _, err := dial("?id="+nodeID, nodeToken)
			Expect(err).ToNot(HaveOccurred())
			worker, err := tunnel.ClientSession(ws, tunnel.LaneInference)
			Expect(err).ToNot(HaveOccurred())
			Eventually(func() bool { return tunnels.Holds(nodeID) }).Should(BeTrue())

			Expect(worker.Close()).To(Succeed())

			Eventually(func() bool { return tunnels.Holds(nodeID) }, 5*time.Second).Should(BeFalse())
			// The registry drops the entry first and releases the claim after it.
			Eventually(func() error {
				_, _, err := clusterR.Owner(ctx, nodeID)
				return err
			}, 5*time.Second).Should(MatchError(cluster.ErrNoConnection))
		})

		It("replaces the tunnel when the worker dials again", func() {
			ws1, _, err := dial("?id="+nodeID, nodeToken)
			Expect(err).ToNot(HaveOccurred())
			first, err := tunnel.ClientSession(ws1, tunnel.LaneInference)
			Expect(err).ToNot(HaveOccurred())
			Eventually(func() bool { return tunnels.Holds(nodeID) }).Should(BeTrue())

			ws2, _, err := dial("?id="+nodeID, nodeToken)
			Expect(err).ToNot(HaveOccurred())
			second, err := tunnel.ClientSession(ws2, tunnel.LaneInference)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(second.Close)

			Eventually(first.CloseChan(), 5*time.Second).Should(BeClosed())
			Expect(second.IsClosed()).To(BeFalse())
			// The end of the first session must not remove the second.
			Consistently(func() bool { return tunnels.Holds(nodeID) }, time.Second).Should(BeTrue())
			_, _, err = clusterR.Owner(ctx, nodeID)
			Expect(err).ToNot(HaveOccurred())
		})

		It("stops accepting the old credential after a new one is minted", func() {
			Expect(nodeReg.SetTunnelTokenHash(ctx, nodeID, hashOf("rotated"))).To(Succeed())
			_, resp, err := dial("?id="+nodeID, nodeToken)
			Expect(err).To(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
			ws, _, err := dial("?id="+nodeID, "rotated")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(ws.Close)
		})
	})

	Describe("the bulk lane", func() {
		It("is refused with 409 before the inference lane is held, and accepted after", func() {
			_, resp, err := dial("?id="+nodeID+"&lane=bulk", nodeToken)
			Expect(err).To(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusConflict))
			_, _, ownerErr := clusterR.Owner(ctx, nodeID)
			Expect(ownerErr).To(MatchError(cluster.ErrNoConnection), "a bulk dial must not claim the node")

			wsI, _, err := dial("?id="+nodeID, nodeToken)
			Expect(err).ToNot(HaveOccurred())
			inference, err := tunnel.ClientSession(wsI, tunnel.LaneInference)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(inference.Close)
			Eventually(func() bool { return tunnels.Holds(nodeID) }).Should(BeTrue())

			wsB, _, err := dial("?id="+nodeID+"&lane=bulk", nodeToken)
			Expect(err).ToNot(HaveOccurred())
			bulk, err := tunnel.ClientSession(wsB, tunnel.LaneBulk)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(bulk.Close)

			// A transfer stream reaches the worker on the bulk session.
			reached := make(chan string, 1)
			go func() {
				st, err := bulk.AcceptStream()
				if err != nil {
					return
				}
				buf := make([]byte, 4)
				_, _ = io.ReadFull(st, buf)
				reached <- string(buf)
			}()
			Eventually(func() error {
				st, err := tunnels.Open(ctx, nodeID, tunnel.LaneBulk)
				if err != nil {
					return err
				}
				_, err = st.Write([]byte("blob"))
				return err
			}, 5*time.Second).Should(Succeed())
			Eventually(reached).Should(Receive(Equal("blob")))
		})

		It("authenticates the bulk lane like the inference lane", func() {
			_, resp, err := dial("?id="+nodeID+"&lane=bulk", registrationToken)
			Expect(err).To(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		})
	})
})

var _ = Describe("A worker whose credential was rotated on the frontend", func() {
	It("registers again and gets its tunnel back without a restart", func() {
		ctx := context.Background()
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		clusterR := cluster.NewRegistry(db)
		Expect(clusterR.Register(ctx, "replica-a", "test", 0, "")).To(Succeed())
		nodeReg, err := nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		tunnels := tunnel.NewRegistry(clusterR, "replica-a")

		node := &nodes.BackendNode{Name: "w1", NodeType: nodes.NodeTypeBackend, TokenHash: hashOf("registration")}
		Expect(nodeReg.Register(ctx, node, true)).To(Succeed())
		var mu sync.Mutex
		current := "credential-1"
		Expect(nodeReg.SetTunnelTokenHash(ctx, node.ID, hashOf(current))).To(Succeed())
		held := func() string { mu.Lock(); defer mu.Unlock(); return current }

		e := echo.New()
		e.GET(tunnel.ConnectPath, clusterapi.ConnectHandler(nodeReg, tunnels))
		srv := httptest.NewServer(e)
		DeferCleanup(srv.Close)

		// Another process registers under the same name and replaces the
		// credential on the frontend. This worker still holds credential-1.
		mu.Lock()
		current = "credential-2-of-another-process"
		Expect(nodeReg.SetTunnelTokenHash(ctx, node.ID, hashOf(current))).To(Succeed())
		mu.Unlock()
		workerToken := "credential-1"
		var reregistered atomic.Int32
		w, err := worker.StartTunnel(ctx, worker.TunnelConfig{
			FrontendURL: srv.URL,
			NodeID:      node.ID,
			Token:       func() string { mu.Lock(); defer mu.Unlock(); return workerToken },
			Services:    map[string]worker.LocalService{},
			Reauthorize: func(ctx context.Context) error {
				// What registration does on the frontend: mint, store the hash,
				// return the value once.
				mu.Lock()
				defer mu.Unlock()
				reregistered.Add(1)
				current = "credential-3"
				workerToken = current
				return nodeReg.SetTunnelTokenHash(ctx, node.ID, hashOf(current))
			},
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(w.Close)
		Eventually(reregistered.Load, 20*time.Second).Should(BeNumerically(">=", 1))
		Eventually(w.Connected, 20*time.Second).Should(BeTrue())
		Expect(held()).To(Equal("credential-3"))
	})
})

var _ = Describe("The path of the connect route", func() {
	It("is the one that the global auth middleware lets through", func() {
		Expect(auth.ClusterConnectPath).To(Equal(tunnel.ConnectPath))
	})
})
