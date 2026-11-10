package tunnel_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The peer pool", func() {
	// silentListener accepts connections and never answers them, which is what a
	// peer looks like while its websocket upgrade hangs.
	silentListener := func() string {
		GinkgoHelper()
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		var (
			mu    sync.Mutex
			conns []net.Conn
		)
		go func() {
			for {
				c, err := lis.Accept()
				if err != nil {
					return
				}
				mu.Lock()
				conns = append(conns, c)
				mu.Unlock()
			}
		}()
		DeferCleanup(func() {
			_ = lis.Close()
			mu.Lock()
			defer mu.Unlock()
			for _, c := range conns {
				_ = c.Close()
			}
		})
		return lis.Addr().String()
	}

	It("does not hold a caller with a short budget behind a caller that has none", func() {
		ctx := context.Background()
		db := testutil.SetupTestDB()
		Expect(cluster.Migrate(ctx, db)).To(Succeed())
		reg := cluster.NewRegistry(db)
		Expect(reg.RegisterPeer(ctx, "slow", "test", 0, "", silentListener(), cluster.NewPeerCredential().Hash())).To(Succeed())
		pool := tunnel.NewPeerPool("replica-a", cluster.NewPeerCredential(), reg)
		DeferCleanup(pool.Close)

		// The first caller states no deadline and sits in the dial.
		first := make(chan error, 1)
		go func() {
			_, err := pool.Open(ctx, "slow")
			first <- err
		}()
		time.Sleep(300 * time.Millisecond)

		short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := pool.Open(short, "slow")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(), "%v", err)
		Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second), "the caller waited on a lock and not on its own budget")
		Expect(errors.Is(err, tunnel.ErrPeerUnreachable)).To(BeFalse(), "an expired budget says nothing about the peer")

		pool.Close()
		Eventually(first, 15*time.Second).Should(Receive())
	})

	Describe("over TLS", func() {
		// peerServer is a replica that accepts a peer link at PeerPath, behind TLS.
		peerServer := func() *httptest.Server {
			GinkgoHelper()
			upgrader := tunnel.NewUpgrader()
			sessions := tunnel.NewPeerSessions(func(_ string, stream net.Conn) { _ = stream.Close() })
			DeferCleanup(sessions.Close)
			mux := http.NewServeMux()
			mux.HandleFunc(tunnel.PeerPath, func(w http.ResponseWriter, r *http.Request) {
				ws, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				sess, err := tunnel.PeerServerSession(ws)
				if err != nil {
					_ = ws.Close()
					return
				}
				sessions.Accept(r.URL.Query().Get("id"), sess)
			})
			srv := httptest.NewTLSServer(mux)
			DeferCleanup(srv.Close)
			return srv
		}

		poolFor := func(srv *httptest.Server, opts ...tunnel.PeerPoolOption) *tunnel.PeerPool {
			GinkgoHelper()
			ctx := context.Background()
			db := testutil.SetupTestDB()
			Expect(cluster.Migrate(ctx, db)).To(Succeed())
			reg := cluster.NewRegistry(db)
			Expect(reg.RegisterPeer(ctx, "tls-peer", "test", 0, "", srv.Listener.Addr().String(), cluster.NewPeerCredential().Hash())).To(Succeed())
			pool := tunnel.NewPeerPool("replica-a", cluster.NewPeerCredential(), reg, opts...)
			DeferCleanup(pool.Close)
			return pool
		}

		It("opens a stream to a peer that sits behind TLS, when it trusts the certificate", func() {
			srv := peerServer()
			roots := x509.NewCertPool()
			roots.AddCert(srv.Certificate())
			pool := poolFor(srv, tunnel.WithPeerTLS(&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}))

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			st, err := pool.Open(ctx, "tls-peer")
			Expect(err).ToNot(HaveOccurred())
			_ = st.Close()
		})

		It("does not reach a peer behind TLS with a plain link, and says that the peer is unreachable", func() {
			srv := peerServer()
			pool := poolFor(srv)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := pool.Open(ctx, "tls-peer")
			Expect(errors.Is(err, tunnel.ErrPeerUnreachable)).To(BeTrue(), "%v", err)
		})

		It("refuses a peer whose certificate it does not trust", func() {
			srv := peerServer()
			pool := poolFor(srv, tunnel.WithPeerTLS(&tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12}))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := pool.Open(ctx, "tls-peer")
			Expect(errors.Is(err, tunnel.ErrPeerUnreachable)).To(BeTrue(), "%v", err)
		})
	})
})
