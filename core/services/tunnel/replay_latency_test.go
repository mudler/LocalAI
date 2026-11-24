package tunnel_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The replay of chat events has a budget of 2 s and answers with up to 512 KiB.
// On the tunnel carrier it crosses the peer link, which also carries the bulk
// traffic of the workers that the far replica holds. These specs measure the
// time of one such request while a transfer fills the link in the direction of
// the reply.
var _ = Describe("Delay of a request between replicas during a bulk transfer", Label("benchmark"), func() {
	const (
		replyBytes = 512 << 10
		requests   = 15
	)

	BeforeEach(func() {
		if !strings.Contains(GinkgoLabelFilter(), "benchmark") {
			Skip("a latency benchmark; run it with --label-filter=benchmark")
		}
	})

	// peerLink starts the owning replica and returns two openers of the asking
	// one: a request, and a transfer from the owner that runs until its stream
	// is closed. Both ends are one session with the windows of the peer link,
	// through a link of the given rate and delay.
	peerLink := func(rate float64, oneWay time.Duration) (request, transfer func(context.Context, string) (net.Conn, error)) {
		GinkgoHelper()
		f := startLanedFrontend()
		link := newSlowLink(f.addr, rate, oneWay)
		owner := dialThrough(link.Addr(), tunnel.LaneBulk)
		var asker *tunnel.Session
		Eventually(f.bulk).Should(Receive(&asker))
		DeferCleanup(func() { _ = asker.Close() })

		replies := tunnel.NewReplies("owner", 8<<20)
		page := bytes.Repeat([]byte("e"), replyBytes)
		replies.Handle("chats.replay.owner", func(context.Context, []byte) ([]byte, error) { return page, nil })
		relay := tunnel.NewRelay(nil, tunnel.WithReplies(replies))
		serveStreams(owner, func(c net.Conn) {
			kind := make([]byte, 1)
			if _, err := io.ReadFull(c, kind); err != nil {
				_ = c.Close()
				return
			}
			if kind[0] == 's' {
				// A relayed file: io.Copy writes 32 KiB at a time.
				defer func() { _ = c.Close() }()
				chunk := make([]byte, 32<<10)
				for {
					if _, err := c.Write(chunk); err != nil {
						return
					}
				}
			}
			relay.Stream("asker", c)
		})
		opener := func(kind byte) func(context.Context, string) (net.Conn, error) {
			return func(ctx context.Context, _ string) (net.Conn, error) {
				st, err := asker.OpenStream(ctx)
				if err != nil {
					return nil, err
				}
				if _, err := st.Write([]byte{kind}); err != nil {
					_ = st.Close()
					return nil, err
				}
				return st, nil
			}
		}
		return opener('r'), opener('s')
	}

	measure := func(name string, rate float64, transfers int) {
		GinkgoHelper()
		request, transfer := peerLink(rate, 10*time.Millisecond)
		var bulk []net.Conn
		for range transfers {
			c, err := transfer(context.Background(), "owner")
			Expect(err).ToNot(HaveOccurred())
			bulk = append(bulk, c)
			go func() { _, _ = io.Copy(io.Discard, c) }()
		}
		if transfers > 0 {
			time.Sleep(time.Second) // the transfers fill the link
		}
		var samples []time.Duration
		for range requests {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			start := time.Now()
			out, err := tunnel.RequestThrough(ctx, request, "owner", "chats.replay.owner", []byte(`{"key":"k"}`), 8<<20, 0)
			cancel()
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(HaveLen(replyBytes))
			samples = append(samples, time.Since(start))
		}
		for _, c := range bulk {
			_ = c.Close()
		}
		stats := summarize(samples)
		over := 0
		for _, d := range samples {
			if d > 2*time.Second {
				over++
			}
		}
		AddReportEntry(name, map[string]any{"requests": stats.count, "p50 ms": stats.p50, "p99 ms": stats.p99, "max ms": stats.max, "over 2 s": over})
		GinkgoWriter.Printf("%s: p50 %.0f ms, p99 %.0f ms, max %.0f ms, %d of %d over 2 s\n", name, stats.p50, stats.p99, stats.max, over, stats.count)
	}

	It("measures a 512 KiB reply on an idle link at 1 Gbit/s", func() { measure("idle, 1 Gbit/s", 125e6, 0) })
	It("measures a 512 KiB reply behind one transfer at 1 Gbit/s", func() { measure("one transfer, 1 Gbit/s", 125e6, 1) })
	It("measures a 512 KiB reply behind four transfers at 1 Gbit/s", func() { measure("four transfers, 1 Gbit/s", 125e6, 4) })
	It("measures a 512 KiB reply on an idle link at 100 Mbit/s", func() { measure("idle, 100 Mbit/s", 12.5e6, 0) })
	It("measures a 512 KiB reply behind one transfer at 100 Mbit/s", func() { measure("one transfer, 100 Mbit/s", 12.5e6, 1) })
	It("measures a 512 KiB reply behind four transfers at 100 Mbit/s", func() { measure("four transfers, 100 Mbit/s", 12.5e6, 4) })
	It("measures a 512 KiB reply behind eight transfers at 100 Mbit/s", func() { measure("eight transfers, 100 Mbit/s", 12.5e6, 8) })
	It("measures a 512 KiB reply on an idle link at 10 Mbit/s", func() { measure("idle, 10 Mbit/s", 1.25e6, 0) })
	It("measures a 512 KiB reply behind one transfer at 10 Mbit/s", func() { measure("one transfer, 10 Mbit/s", 1.25e6, 1) })
})
