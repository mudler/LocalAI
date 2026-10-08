package tunnel_test

import (
	"context"
	"errors"
	"net"
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
})
