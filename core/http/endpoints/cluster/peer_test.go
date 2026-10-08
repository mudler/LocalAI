package cluster_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	clusterapi "github.com/mudler/LocalAI/core/http/endpoints/cluster"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// These specs drive the production handler over a real websocket and a real
// yamux session, because what they pin is a property of the place where the
// connection is hijacked: the id of a peer is a query parameter, and whether it
// is checked is decided there.
var _ = Describe("Peer link endpoint", func() {
	const (
		ownerID  = "replica-owner"
		callerID = "replica-caller"
		// The registration token is held by every worker. Holding it must open
		// no peer link.
		registrationToken = "shared-registration-token"
	)

	var (
		ctx        context.Context
		db         *gorm.DB
		reg        *cluster.Registry
		sessions   *tunnel.PeerSessions
		srv        *httptest.Server
		ownerCred  cluster.PeerCredential
		callerCred cluster.PeerCredential
	)

	BeforeEach(func() {
		ctx = context.Background()
		db = testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg = cluster.NewRegistry(db)

		// The owner holds a worker, and accepts peer links.
		ownerTunnels := tunnel.NewRegistry(reg, ownerID)
		sessions = tunnel.NewPeerSessions(tunnel.NewRelay(ownerTunnels).Stream)
		DeferCleanup(sessions.Close)

		e := echo.New()
		e.GET(tunnel.PeerPath, clusterapi.PeerHandler(reg, sessions.Accept))
		srv = httptest.NewServer(e)
		DeferCleanup(srv.Close)

		ownerCred, callerCred = cluster.NewPeerCredential(), cluster.NewPeerCredential()
		addr := strings.TrimPrefix(srv.URL, "http://")
		Expect(reg.RegisterPeer(ctx, ownerID, "test", 0, "", addr, ownerCred.Hash())).To(Succeed())
		Expect(reg.RegisterPeer(ctx, callerID, "test", 0, "", "127.0.0.1:9", callerCred.Hash())).To(Succeed())
	})

	// raw dials the route with the headers a spec chooses.
	raw := func(query string, header http.Header) (*http.Response, error) {
		GinkgoHelper()
		url := "ws" + strings.TrimPrefix(srv.URL, "http") + tunnel.PeerPath + query
		ws, resp, err := tunnel.NewDialer(5*time.Second).Dial(url, header)
		if ws != nil {
			DeferCleanup(func() { _ = ws.Close() })
		}
		return resp, err
	}

	Describe("the answers before the upgrade", func() {
		It("answers 401 to a dial with no credential, whatever else is missing", func() {
			resp, err := raw("", nil)
			Expect(err).To(MatchError(websocket.ErrBadHandshake))
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("answers 401 to a dial that holds only the registration token", func() {
			header := http.Header{"Authorization": []string{"Bearer " + registrationToken}}
			resp, err := raw("?id="+callerID, header)
			Expect(err).To(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("answers 503 to a credential when the frontend has no table of replicas", func() {
			e := echo.New()
			e.GET(tunnel.PeerPath, clusterapi.PeerHandler(nil, nil))
			bare := httptest.NewServer(e)
			DeferCleanup(bare.Close)
			req, _ := http.NewRequest(http.MethodGet, bare.URL+tunnel.PeerPath+"?id=x", nil)
			req.Header.Set(cluster.PeerIdentityHeader, "anything")
			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(resp.Body.Close)
			Expect(resp.StatusCode).To(Equal(http.StatusServiceUnavailable))
		})

		It("answers 400 to a credential with no replica id", func() {
			resp, err := raw("", http.Header{cluster.PeerIdentityHeader: []string{callerCred.Token()}})
			Expect(err).To(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		})

		It("answers 401, not 404, for a replica it has no row for", func() {
			resp, err := raw("?id=ghost", http.Header{cluster.PeerIdentityHeader: []string{callerCred.Token()}})
			Expect(err).To(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("answers 401 for a replica whose row publishes no credential", func() {
			Expect(db.Model(&cluster.Instance{}).Where("id = ?", callerID).Update("peer_token_hash", "").Error).To(Succeed())
			resp, err := raw("?id="+callerID, http.Header{cluster.PeerIdentityHeader: []string{callerCred.Token()}})
			Expect(err).To(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized), "an empty hash authorises nobody")
		})

		It("answers 401 for the id of a replica with the credential of another", func() {
			resp, err := raw("?id="+callerID, http.Header{cluster.PeerIdentityHeader: []string{cluster.NewPeerCredential().Token()}})
			Expect(err).To(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
			Expect(sessions.Holds(callerID)).To(BeFalse())
		})
	})

	Describe("a replica that proves its id", func() {
		It("gets a link, and the owner holds exactly one session for it", func() {
			pool := tunnel.NewPeerPool(callerID, callerCred, reg)
			DeferCleanup(pool.Close)
			st, err := pool.Open(ctx, ownerID)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = st.Close() })
			Eventually(func() bool { return sessions.Holds(callerID) }, "10s").Should(BeTrue())
		})

		It("is refused when it declares another replica's id, and cannot evict the link of the real one", func() {
			good := tunnel.NewPeerPool(callerID, callerCred, reg)
			DeferCleanup(good.Close)
			st, err := good.Open(ctx, ownerID)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = st.Close() })
			Eventually(func() bool { return sessions.Holds(callerID) }, "10s").Should(BeTrue())

			impostor := tunnel.NewPeerPool(callerID, cluster.NewPeerCredential(), reg)
			DeferCleanup(impostor.Close)
			_, err = impostor.Open(ctx, ownerID)
			Expect(err).To(MatchError(tunnel.ErrPeerRejected))
			Expect(err).To(MatchError(tunnel.ErrPeerUnreachable), "every caller that handles an unreachable peer handles a refusal")
			Expect(errors.Is(err, cluster.ErrInstanceNotFound)).To(BeFalse(), "a refusal is not absence")
			Expect(errors.Is(err, tunnel.ErrNoRoute)).To(BeFalse())
			Expect(sessions.Holds(callerID)).To(BeTrue(), "the link of the real replica stays")
		})

		It("reaches a worker that the owner holds, through the link, with the bytes intact", func() {
			// A worker behind the owner.
			ownerTunnels := tunnel.NewRegistry(reg, ownerID)
			worker := &workerEcho{}
			sessions.Close()
			sessions = tunnel.NewPeerSessions(tunnel.NewRelay(ownerTunnels).Stream)
			DeferCleanup(sessions.Close)
			e := echo.New()
			e.GET(tunnel.PeerPath, clusterapi.PeerHandler(reg, sessions.Accept))
			srv2 := httptest.NewServer(e)
			DeferCleanup(srv2.Close)
			Expect(reg.RegisterPeer(ctx, ownerID, "test", 0, "", strings.TrimPrefix(srv2.URL, "http://"), ownerCred.Hash())).To(Succeed())

			front, back := workerSessions()
			worker.serve(back)
			_, err := ownerTunnels.Attach(ctx, "w1", tunnel.LaneInference, front)
			Expect(err).ToNot(HaveOccurred())

			callerTunnels := tunnel.NewRegistry(reg, callerID)
			pool := tunnel.NewPeerPool(callerID, callerCred, reg)
			DeferCleanup(pool.Close)
			dialer := tunnel.NewWorkerDialer(callerTunnels, pool)

			conn, err := dialer.Dial(ctx, "w1", tunnel.StreamTagGRPC, "10.0.0.5:50051")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })
			big := strings.Repeat("0123456789abcdef", 1<<14) // 256 KiB
			go func() { _, _ = conn.Write([]byte(big)) }()
			got := make([]byte, len(big))
			Expect(conn.SetReadDeadline(time.Now().Add(10 * time.Second))).To(Succeed())
			_, err = io.ReadFull(conn, got)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(got)).To(Equal(big))
		})

		It("reports a peer with no listener as unreachable, and a peer without an address as unreachable too", func() {
			Expect(reg.RegisterPeer(ctx, "silent", "test", 0, "", "127.0.0.1:1", cluster.NewPeerCredential().Hash())).To(Succeed())
			Expect(reg.Register(ctx, "addressless", "test", 0, "")).To(Succeed())
			pool := tunnel.NewPeerPool(callerID, callerCred, reg)
			DeferCleanup(pool.Close)
			for _, peer := range []string{"silent", "addressless"} {
				_, err := pool.Open(ctx, peer)
				Expect(err).To(MatchError(tunnel.ErrPeerUnreachable), peer)
				Expect(errors.Is(err, cluster.ErrInstanceNotFound)).To(BeFalse(), peer)
			}
			_, err := pool.Open(ctx, "no-such-replica")
			Expect(err).To(MatchError(cluster.ErrInstanceNotFound))
			Expect(errors.Is(err, tunnel.ErrPeerUnreachable)).To(BeFalse(), "a replica that is not registered is not an unreachable one")
		})

		It("reports a closed pool as itself", func() {
			pool := tunnel.NewPeerPool(callerID, callerCred, reg)
			pool.Close()
			pool.Close()
			_, err := pool.Open(ctx, ownerID)
			Expect(err).To(MatchError(tunnel.ErrPoolClosed))
		})

		It("does not blame the peer when the budget of the caller runs out", func() {
			// A listener that accepts the TCP connection and never answers the upgrade.
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = ln.Close() })
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					DeferCleanup(func() { _ = c.Close() })
				}
			}()
			Expect(reg.RegisterPeer(ctx, "slow", "test", 0, "", ln.Addr().String(), cluster.NewPeerCredential().Hash())).To(Succeed())
			pool := tunnel.NewPeerPool(callerID, callerCred, reg)
			DeferCleanup(pool.Close)
			short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			_, err = pool.Open(short, "slow")
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(), "%v", err)
			Expect(errors.Is(err, tunnel.ErrPeerUnreachable)).To(BeFalse())
		})
	})
})

var _ = Describe("The path of the peer route", func() {
	It("is the one that the global auth middleware lets through", func() {
		Expect(auth.ClusterPeerPath).To(Equal(tunnel.PeerPath))
	})
})

// workerEcho is a worker that accepts any stream and echoes.
type workerEcho struct{}

func (workerEcho) serve(sess *tunnel.Session) {
	go func() {
		for {
			st, err := sess.AcceptStream()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = st.Close() }()
				if _, _, err := tunnel.ReadStreamRequest(st); err != nil {
					return
				}
				if err := tunnel.WriteStreamAccepted(st); err != nil {
					return
				}
				_, _ = io.Copy(st, st)
			}()
		}
	}()
}

// workerSessions opens a real websocket on a loopback socket and returns the
// frontend side and the worker side of an inference session on it.
func workerSessions() (front, back *tunnel.Session) {
	GinkgoHelper()
	up := tunnel.NewUpgrader()
	got := make(chan *tunnel.Session, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		sess, err := tunnel.ServerSession(ws, tunnel.LaneInference)
		if err != nil {
			_ = ws.Close()
			return
		}
		got <- sess
	}))
	DeferCleanup(s.Close)
	ws, _, err := tunnel.NewDialer(5*time.Second).Dial("ws"+strings.TrimPrefix(s.URL, "http"), nil)
	Expect(err).ToNot(HaveOccurred())
	back, err = tunnel.ClientSession(ws, tunnel.LaneInference)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = back.Close() })
	Eventually(got).Should(Receive(&front))
	DeferCleanup(func() { _ = front.Close() })
	return front, back
}
