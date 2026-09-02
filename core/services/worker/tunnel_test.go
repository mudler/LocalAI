package worker

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
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

		It("routes through the worker's OWN table, ignoring the host the frontend names", func() {
			// Every other spec in this file installs dialLocalTCP, which dials
			// whatever it is handed. This one installs tunnelServices, the
			// table Run installs, so the wire path is exercised against the
			// real routing rules at least once.
			backend := echoListenerOn("127.0.0.1:0")
			DeferCleanup(func() { _ = backend.Close() })
			port := portOf(backend)

			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services = tunnelServices(&Config{
					ServeAddr:   fmt.Sprintf("0.0.0.0:%d", port),
					GRPCMaxPort: port,
				}, "0.0.0.0:1")
			})

			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			// A host that is not this machine, and a port that is.
			Expect(cluster.WriteStreamRequest(stream, cluster.StreamTagGRPC,
				fmt.Sprintf("attacker.invalid:%d", port))).To(Succeed())

			reply := awaitErr(func() error { return cluster.ReadStreamReply(stream) })
			Eventually(reply, "10s").Should(Receive(BeNil()))

			_, err = stream.Write([]byte("loopback"))
			Expect(err).ToNot(HaveOccurred())
			buf := make([]byte, len("loopback"))
			read := awaitErr(func() error {
				_, err := io.ReadFull(stream, buf)
				return err
			})
			Eventually(read, "10s").Should(Receive(BeNil()))
			Expect(string(buf)).To(Equal("loopback"))
		})

		It("refuses a port outside its range as a bad request, over the wire", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services = tunnelServices(&Config{
					ServeAddr:   "0.0.0.0:50051",
					GRPCMaxPort: 50051,
				}, "0.0.0.0:50050")
			})

			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.WriteStreamRequest(stream, cluster.StreamTagGRPC, "127.0.0.1:22")).To(Succeed())

			reply := awaitErr(func() error { return cluster.ReadStreamReply(stream) })
			var got error
			Eventually(reply, "10s").Should(Receive(&got))
			// Three refusals, three meanings. A frontend retries unavailable
			// and gives up on this one.
			Expect(got).To(MatchError(cluster.ErrStreamRequestInvalid))
			Expect(got).ToNot(MatchError(cluster.ErrStreamTargetUnavailable))
			Expect(got).ToNot(MatchError(cluster.ErrStreamTagUnknown))
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

		It("says it learned NOTHING when the request frame never arrived in time", func() {
			// The producer side of the phase's worst self-inflicted defect.
			//
			// This refusal used to be ErrStreamRequestInvalid, merged with a
			// genuinely malformed frame on the grounds that both are "this
			// stream never told me what it wanted". That was safe only while
			// the frontend treated every refusal as "no route". Once
			// nodes.unroutable started exempting worker answers so a crashed
			// backend could be reaped, this became reaping evidence for a frame
			// that had merely not ARRIVED yet.
			//
			// It is reachable: on the relay path the worker's header timer
			// starts when the OWNING replica opens the stream, while the frame
			// is written by the DIALLING replica only after the relay
			// acceptance travels back, so a peer-link round trip runs inside
			// this window on a link that also carries multi-gigabyte artifacts.
			// For a long-deadline caller the endpoint is
			// ConnectionEvictingClient, which stops the model across the fleet.
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[cluster.StreamTagGRPC] = dialLocalTCP
				c.headerTimeout = 50 * time.Millisecond
			})

			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			// Nothing is written, so only the header timer can end this.
			reply := awaitErr(func() error { return cluster.ReadStreamReply(stream) })
			var got error
			Eventually(reply, "10s").Should(Receive(&got))

			Expect(got).To(MatchError(cluster.ErrStreamNotServed))
			// The three assertions that make this bite. Each of the other
			// sentinels is evidence the frontend acts on, and the predicate is
			// the single place the two lists are kept identical.
			Expect(got).ToNot(MatchError(cluster.ErrStreamRequestInvalid),
				"a frame that arrived late is not a malformed frame, and this one reaps")
			Expect(got).ToNot(MatchError(cluster.ErrStreamTargetUnavailable))
			Expect(cluster.IsWorkerAnswer(got)).To(BeFalse(),
				"a timeout must reach a reap guard as no-route, never as the worker's verdict")
		})

		It("still calls a MALFORMED request frame malformed, which is a verdict", func() {
			// The other direction. Separating the timeout out must not turn the
			// verdict off: a frontend that writes a frame this worker cannot
			// parse has a bug that no retry fixes, and the refusal has to keep
			// saying so.
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[cluster.StreamTagGRPC] = dialLocalTCP
				c.headerTimeout = time.Minute
			})

			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			// A frame whose declared length exceeds what the reader will take,
			// so the failure is the frame's shape and not the clock.
			Expect(binary.Write(stream, binary.BigEndian, uint16(60000))).To(Succeed())

			reply := awaitErr(func() error { return cluster.ReadStreamReply(stream) })
			var got error
			Eventually(reply, "10s").Should(Receive(&got))

			Expect(got).To(MatchError(cluster.ErrStreamRequestInvalid))
			Expect(got).ToNot(MatchError(cluster.ErrStreamNotServed))
			Expect(cluster.IsWorkerAnswer(got)).To(BeTrue())
		})

		It("says it learned nothing when the local dial ended on the session going away", func() {
			// classifyServiceFailure's deny-list. The default there is
			// TargetUnavailable and stays that way, because a mis-classified
			// dial failure must fall towards "reapable" rather than towards a
			// row nothing can ever delete. What is exempted is the pair of
			// causes that are provably not the target answering: this worker's
			// own session context ending, and its own I/O deadline firing.
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[cluster.StreamTagGRPC] = func(ctx context.Context, _ string) (net.Conn, error) {
					return nil, fmt.Errorf("dialing the backend: %w", context.Canceled)
				}
			})

			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.WriteStreamRequest(stream, cluster.StreamTagGRPC, "127.0.0.1:41000")).To(Succeed())

			reply := awaitErr(func() error { return cluster.ReadStreamReply(stream) })
			var got error
			Eventually(reply, "10s").Should(Receive(&got))
			Expect(got).To(MatchError(cluster.ErrStreamNotServed))
			Expect(cluster.IsWorkerAnswer(got)).To(BeFalse())
		})

		DescribeTable("keeps a classification the local service already made",
			// The latent instance of the same shape, found by the gate rather
			// than by anything reaching it. This function preserved exactly ONE
			// of the four codes, which was faithful to its own comment for as
			// long as there was one worth keeping. Once ErrStreamNotServed
			// existed, a service returning the code whose whole job is to say
			// "I learned nothing" had it PROMOTED to ErrStreamTargetUnavailable,
			// which every reap guard acts on.
			//
			// No in-tree service produced it, which is the "unreachable,
			// therefore safe" argument that let the request-frame merge survive
			// a whole phase. LocalService and TunnelConfig.Services are both
			// exported, so out-of-tree is a real place, and the same standard
			// applies.
			func(classified error, wantEvidence bool) {
				frontend = newFakeFrontend(false)
				start(func(c *TunnelConfig) {
					c.Services[cluster.StreamTagGRPC] = func(context.Context, string) (net.Conn, error) {
						return nil, fmt.Errorf("the service decided for itself: %w", classified)
					}
				})

				stream, err := session().OpenStream(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(cluster.WriteStreamRequest(stream, cluster.StreamTagGRPC, "127.0.0.1:41000")).To(Succeed())

				reply := awaitErr(func() error { return cluster.ReadStreamReply(stream) })
				var got error
				Eventually(reply, "10s").Should(Receive(&got))
				Expect(got).To(MatchError(classified),
					"re-classifying overwrites a decision made closer to the failure")
				Expect(cluster.IsWorkerAnswer(got)).To(Equal(wantEvidence))
			},
			// The one the promotion broke: not evidence before, evidence after.
			Entry("I learned nothing", cluster.ErrStreamNotServed, false),
			// Promoted too. Both sides reap, so it cost nothing, which is
			// exactly why nothing caught it.
			Entry("I do not serve that tag", cluster.ErrStreamTagUnknown, true),
			Entry("that request was malformed", cluster.ErrStreamRequestInvalid, true),
			Entry("I could not reach the target", cluster.ErrStreamTargetUnavailable, true),
		)

		It("still reports a refused local dial as an unavailable target, which reaps", func() {
			// The other direction for the deny-list: the ordinary shape of a
			// crashed backend must keep producing the code the reap guards act
			// on, or the ghost rows come back.
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[cluster.StreamTagGRPC] = func(context.Context, string) (net.Conn, error) {
					return nil, fmt.Errorf("dial tcp 127.0.0.1:41000: connect: %w", syscall.ECONNREFUSED)
				}
			})

			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(cluster.WriteStreamRequest(stream, cluster.StreamTagGRPC, "127.0.0.1:41000")).To(Succeed())

			reply := awaitErr(func() error { return cluster.ReadStreamReply(stream) })
			var got error
			Eventually(reply, "10s").Should(Receive(&got))
			Expect(got).To(MatchError(cluster.ErrStreamTargetUnavailable))
			Expect(cluster.IsWorkerAnswer(got)).To(BeTrue())
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

// echoListenerOn is echoListener bound to a specific address, so a spec can put
// a listener somewhere the worker must NOT reach.
func echoListenerOn(addr string) net.Listener {
	ln, err := net.Listen("tcp", addr)
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

// portOf returns the port a listener bound to.
func portOf(ln net.Listener) int {
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	Expect(err).ToNot(HaveOccurred())
	port, err := strconv.Atoi(portStr)
	Expect(err).ToNot(HaveOccurred())
	return port
}

// listenOnSecondLoopback binds 127.0.0.2 on a port that 127.0.0.1 does not
// have anything on, and will not be given anything on.
//
// The port choice is the assertion's, not an incidental. The spec it serves
// proves a reachability fact: only 127.0.0.2 is listening, so a service that
// honoured the host the frontend named would connect and one that dials
// loopback cannot. A port taken from :0 lands in the kernel's ephemeral range,
// where some unrelated socket on 127.0.0.1 can be holding the same number, and
// then the dial to 127.0.0.1 succeeds and the spec reports an SSRF that did not
// happen. That is not hypothetical: it failed about one run in seven under
// `-race` while passing every time in isolation.
//
// Choosing from BELOW the ephemeral range (32768 on Linux by default) is what
// removes it, because the kernel does not hand those out for outbound
// connections. 127.0.0.1 is probed and released rather than held: holding it
// would make the dial the spec expects to fail succeed instead.
func listenOnSecondLoopback() (net.Listener, int) {
	GinkgoHelper()
	const (
		floor    = 20000
		ceiling  = 31000
		attempts = 200
	)
	for i := 0; i < attempts; i++ {
		port := floor + rand.IntN(ceiling-floor)
		free, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		if err := free.Close(); err != nil {
			continue
		}
		victim, err := net.Listen("tcp", fmt.Sprintf("127.0.0.2:%d", port))
		if err != nil {
			// A host with no second loopback address fails on every port, so
			// this is the skip the spec used to make inline.
			if i == 0 && strings.Contains(err.Error(), "assign requested address") {
				Skip("this host cannot bind a second loopback address: " + err.Error())
			}
			continue
		}
		return victim, port
	}
	Fail(fmt.Sprintf("no port in [%d, %d) was free on 127.0.0.1 and bindable on 127.0.0.2 after %d attempts", floor, ceiling, attempts))
	return nil, 0
}

// The routing table is the security boundary of the whole tunnel, and until now
// nothing exercised it: every spec above installs dialLocalTCP, which is exactly
// the permissive dialler loopbackService exists to prevent. A review turned
// loopbackService into an arbitrary-host dialler and all 131 specs passed.
var _ = Describe("Worker tunnel local services", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	Describe("loopbackService", func() {
		It("reaches a loopback listener whose port is in range", func() {
			ln := echoListenerOn("127.0.0.1:0")
			DeferCleanup(func() { _ = ln.Close() })
			port := portOf(ln)

			conn, err := loopbackService(port, port)(ctx, ln.Addr().String())
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })

			_, err = conn.Write([]byte("hi"))
			Expect(err).ToNot(HaveOccurred())
			buf := make([]byte, 2)
			_, err = io.ReadFull(conn, buf)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(buf)).To(Equal("hi"))
		})

		It("ignores the host the frontend names and dials loopback anyway", func() {
			ln := echoListenerOn("127.0.0.1:0")
			DeferCleanup(func() { _ = ln.Close() })
			port := portOf(ln)

			// A host that is emphatically not this machine. If it were honoured
			// the dial would fail or, far worse, succeed against something else.
			conn, err := loopbackService(port, port)(ctx, fmt.Sprintf("attacker.invalid:%d", port))
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })
			Expect(conn.RemoteAddr().String()).To(Equal(ln.Addr().String()))
		})

		It("does not reach a listener on another local address the frontend names", func() {
			// The SSRF proof, stated as a reachability fact rather than as a
			// property of the code. The only listener is on 127.0.0.2; nothing
			// is on 127.0.0.1 at that port. A service that honoured the named
			// host would connect; one that dials loopback cannot.
			victim, port := listenOnSecondLoopback()
			DeferCleanup(func() { _ = victim.Close() })

			conn, err := loopbackService(port, port)(ctx, victim.Addr().String())
			if err == nil {
				_ = conn.Close()
				Fail("the worker reached a host the frontend named, so a stream can steer it off loopback")
			}
			Expect(err).To(HaveOccurred())
		})

		DescribeTable("refuses a target it will not route",
			func(target string, minPort, maxPort int) {
				_, err := loopbackService(minPort, maxPort)(ctx, target)
				Expect(err).To(HaveOccurred())
				// Invalid, not unavailable. No retry brings a port outside this
				// worker's own allocator range into it, and telling a frontend
				// to retry forever is how a refusal becomes a hang.
				Expect(err).To(MatchError(cluster.ErrStreamRequestInvalid))
				Expect(err).ToNot(MatchError(cluster.ErrStreamTargetUnavailable))
			},
			Entry("a port below the range", "127.0.0.1:50050", 50051, 50060),
			Entry("a port above the range", "127.0.0.1:50061", 50051, 50060),
			Entry("a non-numeric port", "127.0.0.1:http", 50051, 50060),
			Entry("no port at all", "127.0.0.1", 50051, 50060),
			Entry("an empty target", "", 50051, 50060),
		)

		It("reports a backend that is not listening as unavailable, which a frontend may retry", func() {
			// The other half of the taxonomy: a port IN range with nothing on
			// it is a backend that has not started yet, not a bad request.
			ln := echoListenerOn("127.0.0.1:0")
			port := portOf(ln)
			Expect(ln.Close()).To(Succeed())

			_, err := loopbackService(port, port)(ctx, ln.Addr().String())
			Expect(err).To(HaveOccurred())
			Expect(classifyServiceFailure(err)).To(MatchError(cluster.ErrStreamTargetUnavailable))
			Expect(classifyServiceFailure(err)).ToNot(MatchError(cluster.ErrStreamRequestInvalid))
		})
	})

	Describe("fixedService", func() {
		It("reaches its own address whatever the frontend names", func() {
			ln := echoListenerOn("127.0.0.1:0")
			DeferCleanup(func() { _ = ln.Close() })

			conn, err := fixedService(ln.Addr().String())(ctx, "attacker.invalid:9")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })
			Expect(conn.RemoteAddr().String()).To(Equal(ln.Addr().String()))
		})
	})

	DescribeTable("loopbackAddr rewrites a bind address into a dialable one",
		func(bind, want string) {
			Expect(loopbackAddr(bind)).To(Equal(want))
		},
		// Dialling 0.0.0.0 only accidentally reaches localhost, and not on
		// every platform, so the wildcard is replaced rather than dialled.
		Entry("IPv4 wildcard", "0.0.0.0:8080", "127.0.0.1:8080"),
		Entry("IPv6 wildcard", "[::]:8080", "127.0.0.1:8080"),
		Entry("no host", ":8080", "127.0.0.1:8080"),
		Entry("an explicit host is left alone", "10.0.0.9:8080", "10.0.0.9:8080"),
		Entry("an explicit loopback is left alone", "127.0.0.1:8080", "127.0.0.1:8080"),
		Entry("something that is not host:port passes through", "not-an-address", "not-an-address"),
	)

	Describe("tunnelServices", func() {
		// The table Run installs. Built by its own function precisely so this
		// can be asserted without starting a worker.
		It("serves exactly the two tags the frontend may name", func() {
			cfg := &Config{ServeAddr: "0.0.0.0:50051"}
			Expect(tunnelServices(cfg, "0.0.0.0:50050")).To(HaveLen(2))
			Expect(tunnelServices(cfg, "0.0.0.0:50050")).To(HaveKey(cluster.StreamTagGRPC))
			Expect(tunnelServices(cfg, "0.0.0.0:50050")).To(HaveKey(cluster.StreamTagHTTP))
		})

		It("bounds the gRPC service by THIS worker's configured port range", func() {
			cfg := &Config{ServeAddr: "0.0.0.0:50051", GRPCMaxPort: 50052}
			svc := tunnelServices(cfg, "0.0.0.0:50050")[cluster.StreamTagGRPC]

			// The HTTP server's own port sits one below the base port, so a
			// gRPC-tagged stream cannot be steered onto it.
			_, err := svc(ctx, "127.0.0.1:50050")
			Expect(err).To(MatchError(cluster.ErrStreamRequestInvalid))
			_, err = svc(ctx, "127.0.0.1:50053")
			Expect(err).To(MatchError(cluster.ErrStreamRequestInvalid))
		})

		// Pins that the HTTP service reaches the address Run configures. It
		// does NOT pin the wildcard rewrite: on Linux dialling 0.0.0.0 reaches
		// loopback anyway, so this spec stays green with loopbackAddr disabled.
		// The loopbackAddr table above is what holds that, and it exists
		// because the accident is not portable.
		It("points the HTTP service at the worker's own server", func() {
			ln := echoListenerOn("127.0.0.1:0")
			DeferCleanup(func() { _ = ln.Close() })

			cfg := &Config{ServeAddr: "0.0.0.0:50051"}
			svc := tunnelServices(cfg, fmt.Sprintf("0.0.0.0:%d", portOf(ln)))[cluster.StreamTagHTTP]

			conn, err := svc(ctx, "ignored:1")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = conn.Close() })
			Expect(conn.RemoteAddr().String()).To(Equal(ln.Addr().String()))
		})
	})

	DescribeTable("tunnelEndpoint builds the URL the worker dials",
		func(frontendURL, nodeID, want string) {
			got, err := tunnelEndpoint(frontendURL, nodeID)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("http becomes ws", "http://frontend:8080", "n1", "ws://frontend:8080/api/cluster/connect?id=n1"),
		Entry("https becomes wss", "https://frontend", "n1", "wss://frontend/api/cluster/connect?id=n1"),
		Entry("ws passes through", "ws://frontend:8080", "n1", "ws://frontend:8080/api/cluster/connect?id=n1"),
		Entry("wss passes through", "wss://frontend", "n1", "wss://frontend/api/cluster/connect?id=n1"),
		// A frontend behind a path prefix keeps it: the path is appended, not
		// assigned, exactly as the registration client builds its URLs.
		Entry("a path prefix is kept", "https://host/localai", "n1", "wss://host/localai/api/cluster/connect?id=n1"),
		Entry("a trailing slash is not doubled", "https://host/localai/", "n1", "wss://host/localai/api/cluster/connect?id=n1"),
		Entry("the node id is escaped", "http://h", "a b&c", "ws://h/api/cluster/connect?id=a+b%26c"),
	)

	DescribeTable("tunnelEndpoint refuses a frontend URL it cannot dial",
		func(frontendURL string) {
			_, err := tunnelEndpoint(frontendURL, "n1")
			Expect(err).To(HaveOccurred())
		},
		Entry("empty", ""),
		// Refused rather than coerced: a worker silently dialling a scheme
		// nobody configured is worse than one that says it cannot start.
		Entry("a scheme that is not HTTP", "ftp://frontend"),
		Entry("a bare host with no scheme", "frontend:8080/x"),
		Entry("no host", "http://"),
	)
})
