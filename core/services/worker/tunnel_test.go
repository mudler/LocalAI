package worker

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
)

// awaitErr runs fn on its own goroutine and reports its result on a channel.
//
// Every blocking read in this file goes through it, and that is the single most
// load-bearing decision in the whole suite. The obvious way to assert "the
// worker refused this stream promptly" is to arm a read deadline and expect an
// error, and phase 1 shipped exactly that in three places: it held in none,
// because a stream the worker never answers AT ALL satisfies a deadline
// assertion just as well as one it refused. Reading with NO deadline, on
// another goroutine, and asserting the channel delivers, inverts that: a parked
// stream delivers nothing and the Eventually fails.
func awaitErr(fn func() error) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- fn() }()
	return ch
}

// tunnelDial is what the fake frontend saw on one incoming dial.
type tunnelDial struct {
	token  string
	nodeID string
}

// fakeFrontend is the far side of the tunnel: it speaks the real WebSocket
// upgrade and the real yamux server handshake, so these specs exercise the
// wire, not a mock of it. It is deliberately NOT core/http's handler; that one
// needs a database, and what is under test here is the client.
type fakeFrontend struct {
	srv      *httptest.Server
	sessions chan *yamux.Session
	dials    chan tunnelDial

	// closeAtOnce makes every accepted session die immediately, which is what a
	// frontend replica going down during a rolling restart looks like from the
	// worker.
	closeAtOnce bool
}

func newFakeFrontend(closeAtOnce bool) *fakeFrontend {
	f := &fakeFrontend{
		sessions:    make(chan *yamux.Session, 64),
		dials:       make(chan tunnelDial, 256),
		closeAtOnce: closeAtOnce,
	}
	upgrader := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != cluster.ConnectPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		select {
		case f.dials <- tunnelDial{
			token:  strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
			nodeID: r.URL.Query().Get("id"),
		}:
		default:
		}

		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		sess, err := yamux.Server(cluster.WebsocketConn(ws), nil, nil)
		if err != nil {
			_ = ws.Close()
			return
		}
		if f.closeAtOnce {
			_ = sess.Close()
			return
		}
		select {
		case f.sessions <- sess:
		default:
			_ = sess.Close()
		}
	}))
	return f
}

func (f *fakeFrontend) close() {
	for {
		select {
		case sess := <-f.sessions:
			_ = sess.Close()
		default:
			f.srv.Close()
			return
		}
	}
}

// echoListener is a stand-in for a backend gRPC process on the worker: a local
// TCP listener that reads and writes back.
func echoListener() net.Listener {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return ln
}

// dialLocalTCP is the simplest possible LocalService: connect to whatever the
// frontend named.
func dialLocalTCP(ctx context.Context, target string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", target)
}

var _ = Describe("Worker tunnel client", func() {
	var (
		ctx      context.Context
		cancel   context.CancelFunc
		frontend *fakeFrontend
		tunnel   *Tunnel
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
	})

	AfterEach(func() {
		if tunnel != nil {
			Expect(tunnel.Close()).To(Succeed())
			tunnel = nil
		}
		cancel()
		if frontend != nil {
			frontend.close()
			frontend = nil
		}
	})

	// start brings up the client against the fake frontend already created.
	start := func(mutate func(*TunnelConfig)) {
		cfg := TunnelConfig{
			FrontendURL: frontend.srv.URL,
			NodeID:      "node-1",
			Token:       func() string { return "tunnel-secret" },
			Services:    map[string]LocalService{},
		}
		if mutate != nil {
			mutate(&cfg)
		}
		var err error
		tunnel, err = StartTunnel(ctx, cfg)
		Expect(err).ToNot(HaveOccurred())
	}

	// session waits for the frontend to have accepted the worker's dial.
	session := func() *yamux.Session {
		var sess *yamux.Session
		EventuallyWithOffset(1, frontend.sessions, "10s").Should(Receive(&sess))
		return sess
	}

	Describe("carrying a tagged stream to a local service", func() {
		It("routes a stream tagged for gRPC to the local address it names", func() {
			ln := echoListener()
			DeferCleanup(func() { _ = ln.Close() })

			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[cluster.StreamTagGRPC] = dialLocalTCP
			})

			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.WriteStreamRequest(stream, cluster.StreamTagGRPC, ln.Addr().String())).To(Succeed())

			reply := awaitErr(func() error { return cluster.ReadStreamReply(stream) })
			Eventually(reply, "10s").Should(Receive(BeNil()))

			_, err = stream.Write([]byte("ping"))
			Expect(err).ToNot(HaveOccurred())

			buf := make([]byte, 4)
			read := awaitErr(func() error {
				_, err := io.ReadFull(stream, buf)
				return err
			})
			Eventually(read, "10s").Should(Receive(BeNil()))
			Expect(string(buf)).To(Equal("ping"))
		})
	})

	Describe("refusing a stream it cannot serve", func() {
		// The refusal specs all read with NO deadline, on another goroutine.
		// See awaitErr: a deadline would be satisfied by a stream that was
		// merely parked, which is the exact defect this phase inherited.

		It("refuses an unknown tag promptly, and the stream ENDS rather than parking", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[cluster.StreamTagGRPC] = dialLocalTCP
			})

			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.WriteStreamRequest(stream, "no-such-tag", "")).To(Succeed())

			// Two facts, in order, on one goroutine: the worker SAID why, and
			// then the stream ended. A worker that only says why and leaves the
			// stream open never sends on this channel, so the Eventually below
			// fails rather than passing on a deadline.
			type outcome struct{ reply, end error }
			done := make(chan outcome, 1)
			go func() {
				var got outcome
				got.reply = cluster.ReadStreamReply(stream)
				_, got.end = stream.Read(make([]byte, 1))
				done <- got
			}()

			var got outcome
			Eventually(done, "10s").Should(Receive(&got))
			Expect(got.reply).To(MatchError(cluster.ErrStreamTagUnknown))
			Expect(got.end).To(MatchError(io.EOF))
		})

		It("reports a local service it could not reach as unavailable, not as an unknown tag", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[cluster.StreamTagGRPC] = func(context.Context, string) (net.Conn, error) {
					return nil, fmt.Errorf("connection refused")
				}
			})

			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.WriteStreamRequest(stream, cluster.StreamTagGRPC, "127.0.0.1:1")).To(Succeed())

			reply := awaitErr(func() error { return cluster.ReadStreamReply(stream) })
			var got error
			Eventually(reply, "10s").Should(Receive(&got))
			// Distinct conditions must not be reported as each other: a caller
			// gives up on an unknown tag and retries an unavailable target.
			Expect(got).To(MatchError(cluster.ErrStreamTargetUnavailable))
			Expect(got).ToNot(MatchError(cluster.ErrStreamTagUnknown))
		})

		It("ends a stream whose request never arrives instead of holding it open", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[cluster.StreamTagGRPC] = dialLocalTCP
				c.headerTimeout = 50 * time.Millisecond
			})

			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			// Nothing is written. A worker that waits forever for a request it
			// will never get holds a goroutine and a stream slot per dial.
			ended := awaitErr(func() error {
				_, err := io.Copy(io.Discard, stream)
				return err
			})
			Eventually(ended, "10s").Should(Receive(BeNil()))
		})
	})

	Describe("surviving a bad stream", func() {
		It("keeps serving the session after one stream it could not read", func() {
			ln := echoListener()
			DeferCleanup(func() { _ = ln.Close() })

			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[cluster.StreamTagGRPC] = dialLocalTCP
			})
			sess := session()

			// A frame that declares far more than it sends, then hangs up. The
			// worker cannot parse it and must not take the session down with it.
			bad, err := sess.OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			var hdr [2]byte
			binary.BigEndian.PutUint16(hdr[:], 900)
			_, err = bad.Write(append(hdr[:], []byte("gr")...))
			Expect(err).ToNot(HaveOccurred())
			Expect(bad.CloseWrite()).To(Succeed())
			badEnded := awaitErr(func() error {
				_, err := io.Copy(io.Discard, bad)
				return err
			})
			Eventually(badEnded, "10s").Should(Receive(BeNil()))

			// Same session, a stream the worker can serve.
			good, err := sess.OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.WriteStreamRequest(good, cluster.StreamTagGRPC, ln.Addr().String())).To(Succeed())
			reply := awaitErr(func() error { return cluster.ReadStreamReply(good) })
			Eventually(reply, "10s").Should(Receive(BeNil()))

			_, err = good.Write([]byte("still here"))
			Expect(err).ToNot(HaveOccurred())
			buf := make([]byte, len("still here"))
			read := awaitErr(func() error {
				_, err := io.ReadFull(good, buf)
				return err
			})
			Eventually(read, "10s").Should(Receive(BeNil()))
			Expect(string(buf)).To(Equal("still here"))
		})
		It("serves streams concurrently, so one live stream does not block the next", func() {
			ln := echoListener()
			DeferCleanup(func() { _ = ln.Close() })

			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[cluster.StreamTagGRPC] = dialLocalTCP
			})
			sess := session()

			// The first stream is accepted and then left open with nothing
			// flowing, which is what an idle inference stream or a paused file
			// transfer looks like. Serving streams from the accept loop rather
			// than a goroutine each would park every later request behind it.
			first, err := sess.OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.WriteStreamRequest(first, cluster.StreamTagGRPC, ln.Addr().String())).To(Succeed())
			firstReply := awaitErr(func() error { return cluster.ReadStreamReply(first) })
			Eventually(firstReply, "10s").Should(Receive(BeNil()))

			second, err := sess.OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.WriteStreamRequest(second, cluster.StreamTagGRPC, ln.Addr().String())).To(Succeed())
			secondReply := awaitErr(func() error { return cluster.ReadStreamReply(second) })
			Eventually(secondReply, "10s").Should(Receive(BeNil()))
		})

		It("keeps the session after a local service panics", func() {
			ln := echoListener()
			DeferCleanup(func() { _ = ln.Close() })

			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services["explodes"] = func(context.Context, string) (net.Conn, error) {
					panic("a local service blew up")
				}
				c.Services[cluster.StreamTagGRPC] = dialLocalTCP
			})
			sess := session()

			// Nothing supervises a stream goroutine, so an unrecovered panic
			// here ends the process, which is the loudest possible way to kill
			// the session.
			boom, err := sess.OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.WriteStreamRequest(boom, "explodes", "")).To(Succeed())
			boomEnded := awaitErr(func() error {
				_, err := io.Copy(io.Discard, boom)
				return err
			})
			Eventually(boomEnded, "10s").Should(Receive(BeNil()))

			good, err := sess.OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.WriteStreamRequest(good, cluster.StreamTagGRPC, ln.Addr().String())).To(Succeed())
			reply := awaitErr(func() error { return cluster.ReadStreamReply(good) })
			Eventually(reply, "10s").Should(Receive(BeNil()))
		})
	})

	Describe("reconnecting", func() {
		It("backs off exponentially between reconnects, bounded and never tight", func() {
			frontend = newFakeFrontend(true) // every session dies at once

			delays := make(chan time.Duration, 64)
			start(func(c *TunnelConfig) {
				c.sleep = func(ctx context.Context, d time.Duration) error {
					select {
					case delays <- d:
					default:
					}
					return ctx.Err()
				}
			})

			observed := make([]time.Duration, 0, 10)
			for i := 0; i < 10; i++ {
				var d time.Duration
				Eventually(delays, "20s").Should(Receive(&d), "expected reconnect attempt %d", i+1)
				observed = append(observed, d)
			}

			for i, d := range observed {
				// Never a tight loop: a worker that reconnect-storms a frontend
				// during a rolling restart is a denial of service against the
				// control plane.
				Expect(d).To(BeNumerically(">", 0), "delay %d was not positive", i+1)
				// Bounded: without a ceiling a worker that misses a rolling
				// restart backs off into hours and never comes back.
				Expect(d).To(BeNumerically("<=", tunnelBackoffMax), "delay %d exceeded the ceiling", i+1)
			}
			// And it actually grows. The jitter has a floor of half the
			// unjittered delay, so the fourth attempt is at least 4x the base
			// however the dice fall.
			Expect(observed[3]).To(BeNumerically(">=", 4*tunnelBackoffBase))
		})

		It("keeps backing off after a session that died at once", func() {
			frontend = newFakeFrontend(true)

			delays := make(chan time.Duration, 64)
			start(func(c *TunnelConfig) {
				c.sleep = func(ctx context.Context, d time.Duration) error {
					select {
					case delays <- d:
					default:
					}
					return ctx.Err()
				}
			})

			var last time.Duration
			for i := 0; i < 6; i++ {
				Eventually(delays, "20s").Should(Receive(&last))
			}
			// A session that came up and died immediately is not evidence the
			// frontend is healthy, so the delay must NOT be back at the floor.
			Expect(last).To(BeNumerically(">", tunnelBackoffBase))
		})

		It("returns to its shortest delay after a session that lasted", func() {
			frontend = newFakeFrontend(true)

			// A clock that jumps a minute on every reading. The loop reads it
			// once when a session comes up and once when it ends, so every
			// session looks like it lasted a minute, which is longer than the
			// threshold below which a session is not counted as healthy.
			var ticks atomic.Int64
			base := time.Now()

			delays := make(chan time.Duration, 64)
			start(func(c *TunnelConfig) {
				c.now = func() time.Time {
					return base.Add(time.Duration(ticks.Add(1)) * time.Minute)
				}
				c.sleep = func(ctx context.Context, d time.Duration) error {
					select {
					case delays <- d:
					default:
					}
					return ctx.Err()
				}
			})

			for i := 0; i < 6; i++ {
				var d time.Duration
				Eventually(delays, "20s").Should(Receive(&d), "expected reconnect attempt %d", i+1)
				Expect(d).To(BeNumerically("<=", tunnelBackoffBase),
					"delay %d did not return to the floor after a session that lasted", i+1)
			}
		})

		It("presents the credential current at DIAL time, not the one it started with", func() {
			frontend = newFakeFrontend(true)

			var issued atomic.Int64
			start(func(c *TunnelConfig) {
				c.Token = func() string { return fmt.Sprintf("token-%d", issued.Add(1)) }
				c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
			})

			// Nothing survives a reconnect: the new owner replica has no record
			// of the old session, and the worker's own credential may have been
			// rotated by a re-registration in between. A client that captured
			// its token once locks itself out on the first rotation.
			var first, second tunnelDial
			Eventually(frontend.dials, "20s").Should(Receive(&first))
			Eventually(frontend.dials, "20s").Should(Receive(&second))
			Expect(first.token).To(Equal("token-1"))
			Expect(second.token).To(Equal("token-2"))
			Expect(first.nodeID).To(Equal("node-1"))
			Expect(second.nodeID).To(Equal("node-1"))
		})
	})
})
