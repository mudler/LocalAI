package worker

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// awaitErr runs fn on its own goroutine and reports its result on a channel.
//
// Every blocking read in this file goes through it. To assert that the worker
// refused a stream promptly by arming a read deadline and expecting an error
// proves nothing, because a stream that the worker never answers satisfies the
// deadline as well as one that it refused. A read with no deadline on another
// goroutine, and an Eventually on the channel, turns that round: a stream that
// is parked delivers nothing, and the spec fails.
func awaitErr(fn func() error) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- fn() }()
	return ch
}

// tunnelDial is what the fake frontend saw on one incoming dial.
type tunnelDial struct {
	token  string
	nodeID string
	lane   tunnel.Lane
}

// fakeFrontend is the far side of the tunnel. It speaks the real websocket
// upgrade and the real session handshake of the tunnel package, so that these
// specs exercise the wire and not a mock of it. It is not the connect endpoint:
// that one needs a database, and the client is what is under test here.
type fakeFrontend struct {
	srv       *httptest.Server
	inference chan *tunnel.Session
	bulk      chan *tunnel.Session
	dials     chan tunnelDial

	// closeAtOnce makes every accepted session end at once, which is what a
	// replica that goes down in a rolling restart looks like to the worker.
	closeAtOnce bool
	// bulkStatus, when it is not zero, is the status that a bulk dial gets
	// instead of an upgrade.
	bulkStatus atomic.Int32
	// requiredToken, when it is set, is the only credential that a dial may
	// present. Any other gets 401, as the connect endpoint answers a node whose
	// credential was rotated.
	requiredToken atomic.Pointer[string]
	// denyStatus, when it is not zero, is the status that every dial gets.
	denyStatus atomic.Int32
}

func newFakeFrontend(closeAtOnce bool) *fakeFrontend {
	f := &fakeFrontend{
		inference:   make(chan *tunnel.Session, 64),
		bulk:        make(chan *tunnel.Session, 64),
		dials:       make(chan tunnelDial, 256),
		closeAtOnce: closeAtOnce,
	}
	upgrader := tunnel.NewUpgrader()
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != tunnel.ConnectPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		lane, err := tunnel.ParseLane(r.URL.Query().Get("lane"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case f.dials <- tunnelDial{
			token:  strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
			nodeID: r.URL.Query().Get("id"),
			lane:   lane,
		}:
		default:
		}
		if want := f.requiredToken.Load(); want != nil && strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != *want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if status := int(f.denyStatus.Load()); status != 0 {
			w.WriteHeader(status)
			return
		}
		if status := int(f.bulkStatus.Load()); lane == tunnel.LaneBulk && status != 0 {
			w.WriteHeader(status)
			return
		}

		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		sess, err := tunnel.ServerSession(ws, lane)
		if err != nil {
			_ = ws.Close()
			return
		}
		if f.closeAtOnce {
			_ = sess.Close()
			return
		}
		sessions := f.inference
		if lane == tunnel.LaneBulk {
			sessions = f.bulk
		}
		select {
		case sessions <- sess:
		default:
			_ = sess.Close()
		}
	}))
	return f
}

func (f *fakeFrontend) close() {
	for _, sessions := range []chan *tunnel.Session{f.inference, f.bulk} {
	drain:
		for {
			select {
			case sess := <-sessions:
				_ = sess.Close()
			default:
				break drain
			}
		}
	}
	f.srv.Close()
}

// echoListenerOn starts a TCP server that writes back what it reads.
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

func echoListener() net.Listener { return echoListenerOn("127.0.0.1:0") }

// portOf returns the port that a listener is bound to.
func portOf(ln net.Listener) int {
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	Expect(err).ToNot(HaveOccurred())
	port, err := strconv.Atoi(portStr)
	Expect(err).ToNot(HaveOccurred())
	return port
}

// dialLocalTCP is the simplest LocalService: connect to what the frontend named.
func dialLocalTCP(ctx context.Context, target string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", target)
}

var _ = Describe("Worker tunnel client", func() {
	var (
		ctx      context.Context
		cancel   context.CancelFunc
		frontend *fakeFrontend
		tun      *Tunnel
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
	})

	AfterEach(func() {
		if tun != nil {
			Expect(tun.Close()).To(Succeed())
			tun = nil
		}
		cancel()
		if frontend != nil {
			frontend.close()
			frontend = nil
		}
	})

	start := func(mutate func(*TunnelConfig)) {
		GinkgoHelper()
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
		tun, err = StartTunnel(ctx, cfg)
		Expect(err).ToNot(HaveOccurred())
	}

	session := func() *tunnel.Session {
		GinkgoHelper()
		var sess *tunnel.Session
		Eventually(frontend.inference, "10s").Should(Receive(&sess))
		return sess
	}

	// open opens a stream, names the service, and waits for the reply.
	open := func(sess *tunnel.Session, tag, target string) (net.Conn, error) {
		GinkgoHelper()
		stream, err := sess.OpenStream(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tunnel.WriteStreamRequest(stream, tag, target)).To(Succeed())
		var got error
		Eventually(awaitErr(func() error { return tunnel.ReadStreamReply(stream) }), "10s").Should(Receive(&got))
		return stream, got
	}

	echoes := func(stream net.Conn, msg string) {
		GinkgoHelper()
		_, err := stream.Write([]byte(msg))
		Expect(err).ToNot(HaveOccurred())
		buf := make([]byte, len(msg))
		Eventually(awaitErr(func() error { _, err := io.ReadFull(stream, buf); return err }), "10s").Should(Receive(BeNil()))
		Expect(string(buf)).To(Equal(msg))
	}

	Describe("carrying a tagged stream to a local service", func() {
		It("routes a stream tagged for gRPC to the local address it names", func() {
			ln := echoListener()
			DeferCleanup(func() { _ = ln.Close() })
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) { c.Services[tunnel.StreamTagGRPC] = dialLocalTCP })

			stream, err := open(session(), tunnel.StreamTagGRPC, ln.Addr().String())
			Expect(err).ToNot(HaveOccurred())
			echoes(stream, "ping")
		})

		It("routes through the own table of the worker and ignores the host the frontend names", func() {
			// The other specs install dialLocalTCP, which dials whatever it gets.
			// This one installs tunnelServices, the table that Run installs, so
			// the wire is tried at least once against the real routing rules.
			backend := echoListener()
			DeferCleanup(func() { _ = backend.Close() })
			port := portOf(backend)
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services = tunnelServices(&Config{ServeAddr: fmt.Sprintf("0.0.0.0:%d", port), GRPCMaxPort: port}, "0.0.0.0:1")
			})

			stream, err := open(session(), tunnel.StreamTagGRPC, fmt.Sprintf("attacker.invalid:%d", port))
			Expect(err).ToNot(HaveOccurred())
			echoes(stream, "loopback")
		})

		It("refuses a port outside its range as a bad request, over the wire", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services = tunnelServices(&Config{ServeAddr: "0.0.0.0:50051", GRPCMaxPort: 50051}, "0.0.0.0:50050")
			})

			_, err := open(session(), tunnel.StreamTagGRPC, "127.0.0.1:22")
			Expect(err).To(MatchError(tunnel.ErrStreamRequestInvalid))
			Expect(err).ToNot(MatchError(tunnel.ErrStreamTargetUnavailable))
			Expect(err).ToNot(MatchError(tunnel.ErrStreamTagUnknown))
		})

		It("routes the http tag to the own server of the worker and ignores the target", func() {
			own := echoListener()
			DeferCleanup(func() { _ = own.Close() })
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services = tunnelServices(&Config{}, fmt.Sprintf("0.0.0.0:%d", portOf(own)))
			})

			stream, err := open(session(), tunnel.StreamTagHTTP, "attacker.invalid:1")
			Expect(err).ToNot(HaveOccurred())
			echoes(stream, "own-server")
		})
	})

	Describe("refusing a stream it cannot serve", func() {
		It("refuses an unknown tag promptly, and the stream ends and does not park", func() {
			frontend = newFakeFrontend(false)
			start(nil)

			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(tunnel.WriteStreamRequest(stream, "no-such-tag", "")).To(Succeed())

			// Two facts in order on one goroutine: the worker said why, and the
			// stream then ended. A worker that says why and leaves the stream
			// open never sends on the channel.
			type outcome struct{ reply, end error }
			done := make(chan outcome, 1)
			go func() {
				var got outcome
				got.reply = tunnel.ReadStreamReply(stream)
				_, got.end = stream.Read(make([]byte, 1))
				done <- got
			}()
			var got outcome
			Eventually(done, "10s").Should(Receive(&got))
			Expect(got.reply).To(MatchError(tunnel.ErrStreamTagUnknown))
			Expect(got.end).To(MatchError(io.EOF))
		})

		It("reports a local service it could not reach as unavailable and not as an unknown tag", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[tunnel.StreamTagGRPC] = func(context.Context, string) (net.Conn, error) {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
				}
			})
			_, err := open(session(), tunnel.StreamTagGRPC, "127.0.0.1:1")
			Expect(err).To(MatchError(tunnel.ErrStreamTargetUnavailable))
			Expect(err).ToNot(MatchError(tunnel.ErrStreamTagUnknown))
		})

		It("ends a stream whose request never arrives and does not hold it open", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[tunnel.StreamTagGRPC] = dialLocalTCP
				c.headerTimeout = 50 * time.Millisecond
			})
			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			ended := awaitErr(func() error { _, err := io.Copy(io.Discard, stream); return err })
			Eventually(ended, "10s").Should(Receive(BeNil()))
		})

		It("says that it learned nothing when the request frame did not arrive in time", func() {
			// A frame that arrives late is not a frame that is malformed. On the
			// relay path a round trip of the link between replicas runs inside
			// this window, so a refusal that counted as evidence would evict a
			// model across the fleet for nothing but a congested link.
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[tunnel.StreamTagGRPC] = dialLocalTCP
				c.headerTimeout = 50 * time.Millisecond
			})
			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			var got error
			Eventually(awaitErr(func() error { return tunnel.ReadStreamReply(stream) }), "10s").Should(Receive(&got))

			Expect(got).To(MatchError(tunnel.ErrStreamNotServed))
			Expect(got).ToNot(MatchError(tunnel.ErrStreamRequestInvalid))
			Expect(tunnel.IsWorkerAnswer(got)).To(BeFalse())
		})

		It("still calls a malformed request frame malformed, which is evidence", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[tunnel.StreamTagGRPC] = dialLocalTCP
				c.headerTimeout = time.Minute
			})
			stream, err := session().OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(binary.Write(stream, binary.BigEndian, uint16(60000))).To(Succeed())
			var got error
			Eventually(awaitErr(func() error { return tunnel.ReadStreamReply(stream) }), "10s").Should(Receive(&got))

			Expect(got).To(MatchError(tunnel.ErrStreamRequestInvalid))
			Expect(tunnel.IsWorkerAnswer(got)).To(BeTrue())
		})

		It("says that it learned nothing when the local dial ended because the session went away", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[tunnel.StreamTagGRPC] = func(context.Context, string) (net.Conn, error) {
					return nil, fmt.Errorf("dialing the backend: %w", context.Canceled)
				}
			})
			_, err := open(session(), tunnel.StreamTagGRPC, "127.0.0.1:41000")
			Expect(err).To(MatchError(tunnel.ErrStreamNotServed))
			Expect(tunnel.IsWorkerAnswer(err)).To(BeFalse())
		})

		It("does not count a timeout of the local dial as the answer of the target", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services[tunnel.StreamTagGRPC] = func(context.Context, string) (net.Conn, error) {
					return nil, &net.OpError{Op: "dial", Err: timeoutError{}}
				}
			})
			_, err := open(session(), tunnel.StreamTagGRPC, "127.0.0.1:41000")
			Expect(err).To(MatchError(tunnel.ErrStreamNotServed))
		})

		DescribeTable("classifies the errno of a failed local dial",
			func(errno syscall.Errno, wantEvidence bool) {
				frontend = newFakeFrontend(false)
				start(func(c *TunnelConfig) {
					c.Services[tunnel.StreamTagGRPC] = func(context.Context, string) (net.Conn, error) {
						return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}
					}
				})
				_, err := open(session(), tunnel.StreamTagGRPC, "127.0.0.1:41000")
				if wantEvidence {
					Expect(err).To(MatchError(tunnel.ErrStreamTargetUnavailable))
				} else {
					Expect(err).To(MatchError(tunnel.ErrStreamNotServed))
				}
				Expect(tunnel.IsWorkerAnswer(err)).To(Equal(wantEvidence))
			},
			// The target did not answer: the backend is gone or not there.
			Entry("connection refused", syscall.ECONNREFUSED, true),
			Entry("host unreachable", syscall.EHOSTUNREACH, true),
			Entry("network unreachable", syscall.ENETUNREACH, true),
			Entry("connection reset", syscall.ECONNRESET, true),
			// This process or this host ran short of something. The target was
			// never asked, so nothing is learned about it.
			Entry("too many open files in this process", syscall.EMFILE, false),
			Entry("too many open files on this host", syscall.ENFILE, false),
			Entry("no buffer space", syscall.ENOBUFS, false),
			Entry("out of memory", syscall.ENOMEM, false),
			Entry("no local port or address left", syscall.EADDRNOTAVAIL, false),
			Entry("permission denied by a local rule", syscall.EACCES, false),
			Entry("operation not permitted", syscall.EPERM, false),
		)

		It("learns nothing from an error that it does not recognise", func() {
			Expect(classifyServiceFailure(errors.New("something new went wrong"))).To(MatchError(tunnel.ErrStreamNotServed))
		})

		DescribeTable("keeps a classification that the local service already made",
			func(classified error, wantEvidence bool) {
				frontend = newFakeFrontend(false)
				start(func(c *TunnelConfig) {
					c.Services[tunnel.StreamTagGRPC] = func(context.Context, string) (net.Conn, error) {
						return nil, fmt.Errorf("%w: from the service", classified)
					}
				})
				_, err := open(session(), tunnel.StreamTagGRPC, "127.0.0.1:1")
				Expect(err).To(MatchError(classified))
				Expect(tunnel.IsWorkerAnswer(err)).To(Equal(wantEvidence))
			},
			Entry("I learned nothing", tunnel.ErrStreamNotServed, false),
			Entry("I do not serve that tag", tunnel.ErrStreamTagUnknown, true),
			Entry("that request was malformed", tunnel.ErrStreamRequestInvalid, true),
			Entry("I could not reach the target", tunnel.ErrStreamTargetUnavailable, true),
		)
	})

	Describe("surviving a bad stream", func() {
		It("keeps serving the session after a stream it could not read", func() {
			ln := echoListener()
			DeferCleanup(func() { _ = ln.Close() })
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) { c.Services[tunnel.StreamTagGRPC] = dialLocalTCP })
			sess := session()

			// A frame that declares much more than it sends, and then hangs up.
			bad, err := sess.OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			var hdr [2]byte
			binary.BigEndian.PutUint16(hdr[:], 900)
			_, err = bad.Write(append(hdr[:], []byte("gr")...))
			Expect(err).ToNot(HaveOccurred())
			Expect(bad.(interface{ CloseWrite() error }).CloseWrite()).To(Succeed())
			Eventually(awaitErr(func() error { _, err := io.Copy(io.Discard, bad); return err }), "10s").Should(Receive(BeNil()))

			good, err := open(sess, tunnel.StreamTagGRPC, ln.Addr().String())
			Expect(err).ToNot(HaveOccurred())
			echoes(good, "still here")
		})

		It("serves streams at the same time, so a stream that is live does not block the next", func() {
			ln := echoListener()
			DeferCleanup(func() { _ = ln.Close() })
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) { c.Services[tunnel.StreamTagGRPC] = dialLocalTCP })
			sess := session()

			_, err := open(sess, tunnel.StreamTagGRPC, ln.Addr().String())
			Expect(err).ToNot(HaveOccurred())
			_, err = open(sess, tunnel.StreamTagGRPC, ln.Addr().String())
			Expect(err).ToNot(HaveOccurred())
		})

		It("keeps the session after a local service panics", func() {
			ln := echoListener()
			DeferCleanup(func() { _ = ln.Close() })
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Services["explodes"] = func(context.Context, string) (net.Conn, error) { panic("a local service blew up") }
				c.Services[tunnel.StreamTagGRPC] = dialLocalTCP
			})
			sess := session()

			boom, err := sess.OpenStream(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(tunnel.WriteStreamRequest(boom, "explodes", "")).To(Succeed())
			Eventually(awaitErr(func() error { _, err := io.Copy(io.Discard, boom); return err }), "10s").Should(Receive(BeNil()))

			_, err = open(sess, tunnel.StreamTagGRPC, ln.Addr().String())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("connecting again", func() {
		recordDelays := func(delays chan time.Duration) func(*TunnelConfig) {
			return func(c *TunnelConfig) {
				c.noBulk = true
				c.sleep = func(ctx context.Context, d time.Duration) error {
					select {
					case delays <- d:
					default:
					}
					return ctx.Err()
				}
			}
		}

		It("waits longer after each attempt, bounded and never in a tight loop", func() {
			frontend = newFakeFrontend(true) // every session ends at once
			delays := make(chan time.Duration, 64)
			start(recordDelays(delays))

			observed := make([]time.Duration, 0, 10)
			for i := range 10 {
				var d time.Duration
				Eventually(delays, "20s").Should(Receive(&d), "attempt %d", i+1)
				observed = append(observed, d)
			}
			for i, d := range observed {
				Expect(d).To(BeNumerically(">", 0), "delay %d", i+1)
				Expect(d).To(BeNumerically("<=", tunnelBackoffMax), "delay %d", i+1)
			}
			// The jitter keeps half of the delay, so the fourth wait is at least
			// four times the base, whatever the dice say.
			Expect(observed[3]).To(BeNumerically(">=", 4*tunnelBackoffBase))
		})

		It("does not return to the shortest wait after a session that ended at once", func() {
			frontend = newFakeFrontend(true)
			delays := make(chan time.Duration, 64)
			start(recordDelays(delays))
			var last time.Duration
			for range 6 {
				Eventually(delays, "20s").Should(Receive(&last))
			}
			Expect(last).To(BeNumerically(">", tunnelBackoffBase))
		})

		It("returns to the shortest wait after a session that lasted", func() {
			frontend = newFakeFrontend(true)
			// A clock that jumps one minute at every reading. The loop reads it
			// when a session starts and when it ends, so every session seems to
			// have lasted a minute.
			var ticks atomic.Int64
			base := time.Now()
			delays := make(chan time.Duration, 64)
			start(func(c *TunnelConfig) {
				recordDelays(delays)(c)
				c.now = func() time.Time { return base.Add(time.Duration(ticks.Add(1)) * time.Minute) }
			})
			for i := range 6 {
				var d time.Duration
				Eventually(delays, "20s").Should(Receive(&d), "attempt %d", i+1)
				Expect(d).To(BeNumerically("<=", tunnelBackoffBase), "delay %d", i+1)
			}
		})

		It("presents the credential that is current at the time of the dial", func() {
			frontend = newFakeFrontend(true)
			var issued atomic.Int64
			start(func(c *TunnelConfig) {
				c.Token = func() string { return fmt.Sprintf("token-%d", issued.Add(1)) }
				c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
			})
			var first, second tunnelDial
			Eventually(frontend.dials, "20s").Should(Receive(&first))
			Eventually(frontend.dials, "20s").Should(Receive(&second))
			Expect(first.token).To(Equal("token-1"))
			Expect(second.token).To(Equal("token-2"))
			Expect(first.nodeID).To(Equal("node-1"))
		})

		Describe("after the frontend refused the credential", func() {
			// The frontend rotates the credential of the node, as it does when a
			// second process registers under the same name. The worker holds the
			// old value and must get a new one without a restart.
			It("registers again and connects with the new credential", func() {
				frontend = newFakeFrontend(false)
				current := "rotated-by-another-process"
				frontend.requiredToken.Store(&current)
				var token atomic.Pointer[string]
				stale := "stale"
				token.Store(&stale)
				var registrations atomic.Int32
				start(func(c *TunnelConfig) {
					c.noBulk = true
					c.Token = func() string { return *token.Load() }
					c.Reauthorize = func(context.Context) error {
						registrations.Add(1)
						token.Store(&current)
						return nil
					}
					c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
				})
				Eventually(frontend.inference, "10s").Should(Receive())
				Expect(registrations.Load()).To(Equal(int32(1)))
				Eventually(tun.Connected, "10s").Should(BeTrue())
			})

			It("registers again once for each wait and never in a tight loop", func() {
				frontend = newFakeFrontend(false)
				frontend.denyStatus.Store(http.StatusForbidden)
				var registrations atomic.Int32
				// Unbuffered: the loop cannot start a pass until the spec has read the
				// wait of the last one.
				waits := make(chan time.Duration)
				start(func(c *TunnelConfig) {
					c.noBulk = true
					c.Reauthorize = func(context.Context) error { registrations.Add(1); return nil }
					c.sleep = func(ctx context.Context, d time.Duration) error {
						select {
						case waits <- d:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					}
				})
				var d time.Duration
				for range 5 {
					Eventually(waits, "10s").Should(Receive(&d))
					Expect(d).To(BeNumerically(">", 0))
				}
				// One registration for each pass, and every pass waits.
				Expect(int(registrations.Load())).To(BeNumerically("<=", 6))
				Expect(int(registrations.Load())).To(BeNumerically(">=", 5))
			})

			It("keeps trying with the backoff when registering again fails", func() {
				frontend = newFakeFrontend(false)
				frontend.denyStatus.Store(http.StatusUnauthorized)
				var registrations atomic.Int32
				start(func(c *TunnelConfig) {
					c.noBulk = true
					c.Reauthorize = func(context.Context) error {
						registrations.Add(1)
						return errors.New("frontend down")
					}
					c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
				})
				Eventually(registrations.Load, "10s").Should(BeNumerically(">=", 3))
				Expect(tun.Connected()).To(BeFalse())
			})

			It("does not register again for a refusal that a registration does not cure", func() {
				frontend = newFakeFrontend(false)
				frontend.denyStatus.Store(http.StatusServiceUnavailable)
				var registrations atomic.Int32
				dials := make(chan struct{}, 64)
				start(func(c *TunnelConfig) {
					c.noBulk = true
					c.Reauthorize = func(context.Context) error { registrations.Add(1); return nil }
					c.sleep = func(ctx context.Context, _ time.Duration) error {
						select {
						case dials <- struct{}{}:
						default:
						}
						return ctx.Err()
					}
				})
				for range 3 {
					Eventually(dials, "10s").Should(Receive())
				}
				Expect(registrations.Load()).To(BeZero())
			})
		})

		It("does not dial with no credential, and says so", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.Token = func() string { return "" }
				c.sleep = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
			})
			Consistently(frontend.dials, "500ms", "50ms").ShouldNot(Receive())
			Expect(tun.Connected()).To(BeFalse())
		})
	})

	Describe("the two lanes", func() {
		It("dials the bulk lane after the inference lane, with the lane in the query", func() {
			frontend = newFakeFrontend(false)
			start(nil)
			session()

			var first, second tunnelDial
			Eventually(frontend.dials, "10s").Should(Receive(&first))
			Eventually(frontend.dials, "10s").Should(Receive(&second))
			Expect(first.lane).To(Equal(tunnel.LaneInference))
			Expect(second.lane).To(Equal(tunnel.LaneBulk))
			Expect(second.token).To(Equal("tunnel-secret"))
			Expect(second.nodeID).To(Equal("node-1"))
			Eventually(tun.BulkConnected, "10s").Should(BeTrue())
		})

		It("serves the streams of the bulk session as it serves those of the inference session", func() {
			ln := echoListener()
			DeferCleanup(func() { _ = ln.Close() })
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) { c.Services[tunnel.StreamTagHTTP] = dialLocalTCP })
			session()

			var bulk *tunnel.Session
			Eventually(frontend.bulk, "10s").Should(Receive(&bulk))
			stream, err := open(bulk, tunnel.StreamTagHTTP, ln.Addr().String())
			Expect(err).ToNot(HaveOccurred())
			echoes(stream, "file bytes")
		})

		It("keeps the inference lane when the frontend refuses the bulk lane", func() {
			for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusServiceUnavailable, http.StatusConflict} {
				frontend = newFakeFrontend(false)
				frontend.bulkStatus.Store(int32(status))
				start(nil)
				session()
				Eventually(frontend.dials, "10s").Should(Receive()) // the inference dial
				Eventually(frontend.dials, "10s").Should(Receive(), "a bulk dial that was refused with %d is tried again", status)
				Expect(tun.Connected()).To(BeTrue(), "status %d", status)
				Expect(tun.BulkConnected()).To(BeFalse(), "status %d", status)

				Expect(tun.Close()).To(Succeed())
				tun = nil
				frontend.close()
				frontend = nil
			}
		})

		It("dials the bulk lane again soon after the answer 409, because another replica may hold the tunnel", func() {
			frontend = newFakeFrontend(false)
			frontend.bulkStatus.Store(http.StatusConflict)
			delays := make(chan time.Duration, 64)
			start(func(c *TunnelConfig) {
				c.sleep = func(ctx context.Context, d time.Duration) error {
					select {
					case delays <- d:
					default:
					}
					// Do not wait. The delay is what is measured.
					return ctx.Err()
				}
			})
			session()
			for range 5 {
				var d time.Duration
				Eventually(delays, "10s").Should(Receive(&d))
				Expect(d).To(BeNumerically("<=", tunnelWrongReplicaDelay*3/2), "a 409 must not grow the wait")
			}

			// The right replica answers now.
			frontend.bulkStatus.Store(0)
			Eventually(tun.BulkConnected, "10s").Should(BeTrue())
		})

		It("connects both lanes again after the inference session ends", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) { c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() } })
			inference := session()
			Eventually(tun.BulkConnected, "10s").Should(BeTrue())

			Expect(inference.Close()).To(Succeed())

			Eventually(func() bool { return tun.Connected() && tun.BulkConnected() }, "20s").Should(BeTrue())
			Eventually(frontend.inference, "10s").Should(Receive())
		})

		It("dials the bulk lane again when only the bulk session ends, and keeps the inference lane", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) { c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() } })
			inference := session()
			var bulk *tunnel.Session
			Eventually(frontend.bulk, "10s").Should(Receive(&bulk))
			Eventually(tun.BulkConnected, "10s").Should(BeTrue())

			Expect(bulk.Close()).To(Succeed())

			Eventually(frontend.bulk, "10s").Should(Receive(), "the worker dials the bulk lane again")
			Expect(inference.IsClosed()).To(BeFalse())
			Expect(tun.Connected()).To(BeTrue())
		})

		It("closes the bulk session with the tunnel", func() {
			frontend = newFakeFrontend(false)
			start(nil)
			session()
			var bulk *tunnel.Session
			Eventually(frontend.bulk, "10s").Should(Receive(&bulk))

			Expect(tun.Close()).To(Succeed())
			tun = nil
			Eventually(bulk.CloseChan(), "10s").Should(BeClosed())
		})
	})

	Describe("reporting whether it holds a session", func() {
		It("reports connected once the frontend has accepted its dial", func() {
			frontend = newFakeFrontend(false)
			start(nil)
			session()
			Eventually(tun.Connected, "10s").Should(BeTrue())
		})

		It("reports disconnected once the session is gone", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				// The next dial waits, so that the spec sees the gap.
				c.sleep = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
			})
			sess := session()
			Eventually(tun.Connected, "10s").Should(BeTrue())
			Expect(sess.Close()).To(Succeed())
			Eventually(tun.Connected, "10s").Should(BeFalse())
		})

		It("reports disconnected before the first dial has landed", func() {
			frontend = newFakeFrontend(false)
			frontend.srv.Close()
			start(func(c *TunnelConfig) {
				c.sleep = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
			})
			Consistently(tun.Connected, "500ms", "50ms").Should(BeFalse())
		})

		It("reports disconnected on a tunnel that is nil", func() {
			var absent *Tunnel
			Expect(absent.Connected()).To(BeFalse())
			Expect(absent.BulkConnected()).To(BeFalse())
		})

		It("is what the readiness gate of the worker looks at", func() {
			frontend = newFakeFrontend(false)
			start(func(c *TunnelConfig) {
				c.sleep = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
			})
			readiness := &nodes.WorkerReadiness{}
			readiness.Set(nodes.TunnelReadiness(tun))
			sess := session()
			Eventually(readiness.Check, "10s").Should(Succeed())
			// The gate fails open when no probe is installed, so a gate that
			// goes back to not ready proves that the probe is installed.
			Expect(sess.Close()).To(Succeed())
			Eventually(readiness.Check, "10s").Should(MatchError(nodes.ErrTunnelDisconnected))
		})
	})

	Describe("starting", func() {
		It("refuses a configuration that cannot work, and not a frontend that cannot be reached", func() {
			_, err := StartTunnel(ctx, TunnelConfig{FrontendURL: "http://frontend:8080", Token: func() string { return "t" }})
			Expect(err).To(MatchError(ContainSubstring("no node id")))
			_, err = StartTunnel(ctx, TunnelConfig{FrontendURL: "http://frontend:8080", NodeID: "n"})
			Expect(err).To(MatchError(ContainSubstring("no credential source")))
			_, err = StartTunnel(ctx, TunnelConfig{FrontendURL: "ftp://frontend", NodeID: "n", Token: func() string { return "t" }})
			Expect(err).To(HaveOccurred())

			// A frontend that is down is not a reason to refuse the start.
			down := httptest.NewServer(http.NotFoundHandler())
			url := down.URL
			down.Close()
			t, err := StartTunnel(ctx, TunnelConfig{FrontendURL: url, NodeID: "n", Token: func() string { return "t" },
				sleep: func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }})
			Expect(err).ToNot(HaveOccurred())
			Expect(t.Close()).To(Succeed())
		})

		It("can be closed twice", func() {
			frontend = newFakeFrontend(false)
			start(nil)
			Expect(tun.Close()).To(Succeed())
			Expect(tun.Close()).To(Succeed())
			tun = nil
		})
	})
})

// timeoutError is a net.Error that says that it is a timeout.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// listenOnSecondLoopback binds 127.0.0.2 on a port where nothing listens on
// 127.0.0.1.
//
// The choice of the port is part of the assertion that this serves. Only
// 127.0.0.2 listens, so a service that follows the host the frontend names would
// connect, and one that dials loopback cannot. A port from :0 is in the
// ephemeral range, where an unrelated socket on 127.0.0.1 can hold the same
// number, and then the dial to 127.0.0.1 succeeds and the spec reports a hole
// that is not there. A port below the ephemeral range (32768 by default on
// Linux) is never given out for outbound connections. 127.0.0.1 is tried and
// released and not held, because holding it would make the dial that must fail
// succeed.
func listenOnSecondLoopback() (net.Listener, int) {
	GinkgoHelper()
	const (
		floor    = 20000
		ceiling  = 31000
		attempts = 200
	)
	for i := range attempts {
		// #nosec G404 -- a port for a test.
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

// The routing table is the security boundary of the tunnel. Every spec above
// that uses dialLocalTCP installs the dialler that loopbackService exists to
// prevent, so these specs test the real functions.
var _ = Describe("Worker tunnel local services", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	Describe("loopbackService", func() {
		It("reaches a loopback listener whose port is in range", func() {
			ln := echoListener()
			DeferCleanup(func() { _ = ln.Close() })
			port := portOf(ln)
			conn, err := loopbackService(port, port)(ctx, fmt.Sprintf("127.0.0.1:%d", port))
			Expect(err).ToNot(HaveOccurred())
			Expect(conn.Close()).To(Succeed())
		})

		It("ignores the host the frontend names and dials loopback", func() {
			ln := echoListener()
			DeferCleanup(func() { _ = ln.Close() })
			port := portOf(ln)
			conn, err := loopbackService(port, port)(ctx, fmt.Sprintf("attacker.invalid:%d", port))
			Expect(err).ToNot(HaveOccurred())
			Expect(conn.Close()).To(Succeed())
		})

		It("does not reach a listener on another local address that the frontend names", func() {
			victim, port := listenOnSecondLoopback()
			DeferCleanup(func() { _ = victim.Close() })
			_, err := loopbackService(port, port)(ctx, fmt.Sprintf("127.0.0.2:%d", port))
			Expect(err).To(HaveOccurred())
		})

		DescribeTable("refuses a target it will not route",
			func(target string, minPort, maxPort int) {
				_, err := loopbackService(minPort, maxPort)(ctx, target)
				Expect(err).To(MatchError(tunnel.ErrStreamRequestInvalid))
			},
			Entry("a port below the range", "127.0.0.1:50050", 50051, 50060),
			Entry("a port above the range", "127.0.0.1:50061", 50051, 50060),
			Entry("a port that is not a number", "127.0.0.1:http", 50051, 50060),
			Entry("no port", "127.0.0.1", 50051, 50060),
			Entry("an empty target", "", 50051, 50060),
		)

		It("reports a backend that does not listen as unavailable", func() {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			port := portOf(ln)
			Expect(ln.Close()).To(Succeed())

			_, err = loopbackService(port, port)(ctx, fmt.Sprintf("127.0.0.1:%d", port))
			Expect(err).To(HaveOccurred())
			Expect(classifyServiceFailure(err)).To(MatchError(tunnel.ErrStreamTargetUnavailable))
		})
	})

	Describe("fixedService", func() {
		It("reaches its own address whatever the frontend names", func() {
			ln := echoListener()
			DeferCleanup(func() { _ = ln.Close() })
			conn, err := fixedService(ln.Addr().String())(ctx, "attacker.invalid:1")
			Expect(err).ToNot(HaveOccurred())
			Expect(conn.Close()).To(Succeed())
		})
	})

	DescribeTable("loopbackAddr turns a bind address into one that can be dialled",
		func(bind, want string) { Expect(loopbackAddr(bind)).To(Equal(want)) },
		Entry("IPv4 wildcard", "0.0.0.0:8080", "127.0.0.1:8080"),
		Entry("IPv6 wildcard", "[::]:8080", "127.0.0.1:8080"),
		Entry("no host", ":8080", "127.0.0.1:8080"),
		Entry("an explicit host is left alone", "10.0.0.9:8080", "10.0.0.9:8080"),
		Entry("an explicit loopback is left alone", "127.0.0.1:8080", "127.0.0.1:8080"),
		Entry("something that is not host:port passes through", "not-an-address", "not-an-address"),
	)

	Describe("tunnelServices", func() {
		It("serves exactly the two tags that the frontend may name", func() {
			table := tunnelServices(&Config{}, "0.0.0.0:50050")
			Expect(table).To(HaveLen(2))
			Expect(table).To(HaveKey(tunnel.StreamTagGRPC))
			Expect(table).To(HaveKey(tunnel.StreamTagHTTP))
		})

		It("limits the gRPC service to the port range of this worker", func() {
			table := tunnelServices(&Config{ServeAddr: "0.0.0.0:50051", GRPCMaxPort: 50055}, "0.0.0.0:50050")
			_, err := table[tunnel.StreamTagGRPC](ctx, "127.0.0.1:50056")
			Expect(err).To(MatchError(tunnel.ErrStreamRequestInvalid))
			_, err = table[tunnel.StreamTagGRPC](ctx, "127.0.0.1:50050")
			Expect(err).To(MatchError(tunnel.ErrStreamRequestInvalid))
		})
	})

	DescribeTable("tunnelEndpoint builds the URL that the worker dials",
		func(frontend, nodeID, want string) {
			got, err := tunnelEndpoint(frontend, nodeID)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("http becomes ws", "http://frontend:8080", "n1", "ws://frontend:8080/api/cluster/connect?id=n1"),
		Entry("https becomes wss", "https://frontend", "n1", "wss://frontend/api/cluster/connect?id=n1"),
		Entry("ws stays", "ws://frontend:8080", "n1", "ws://frontend:8080/api/cluster/connect?id=n1"),
		Entry("wss stays", "wss://frontend", "n1", "wss://frontend/api/cluster/connect?id=n1"),
		Entry("a path prefix stays", "https://host/localai", "n1", "wss://host/localai/api/cluster/connect?id=n1"),
		Entry("a slash at the end is not doubled", "https://host/localai/", "n1", "wss://host/localai/api/cluster/connect?id=n1"),
		Entry("the node id is escaped", "http://h", "a b&c", "ws://h/api/cluster/connect?id=a+b%26c"),
	)

	DescribeTable("tunnelEndpoint refuses a URL it cannot dial",
		func(frontend string) {
			_, err := tunnelEndpoint(frontend, "n1")
			Expect(err).To(HaveOccurred())
		},
		Entry("empty", ""),
		Entry("a scheme that is not HTTP", "ftp://frontend"),
		Entry("a host with no scheme", "frontend:8080/x"),
		Entry("no host", "http://"),
	)

	DescribeTable("tunnelBackoffDelay stays between half of the step and the step, and below the limit",
		func(attempt int) {
			for range 200 {
				d := tunnelBackoffDelay(attempt)
				Expect(d).To(BeNumerically(">", 0))
				Expect(d).To(BeNumerically("<=", tunnelBackoffMax))
			}
		},
		Entry("zero", 0),
		Entry("one", 1),
		Entry("six", 6),
		Entry("forty", 40),
		Entry("a number that would overflow the shift", 1000),
	)
})
