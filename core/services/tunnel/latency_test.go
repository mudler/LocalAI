package tunnel_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/core/services/tunnel/slowlink"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// newSlowLink starts a link in front of target and closes it after the spec.
func newSlowLink(target string, bytesPerSecond float64, oneWay time.Duration) *slowlink.Link {
	GinkgoHelper()
	l, err := slowlink.New(target, bytesPerSecond, oneWay)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(l.Close)
	return l
}

// probeStats are the delays of the probes in milliseconds.
type probeStats struct {
	count         int
	p50, p99, max float64
}

func summarize(samples []time.Duration) probeStats {
	if len(samples) == 0 {
		return probeStats{}
	}
	slices.Sort(samples)
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	return probeStats{
		count: len(samples),
		p50:   ms(samples[len(samples)/2]),
		p99:   ms(samples[min(len(samples)-1, len(samples)*99/100)]),
		max:   ms(samples[len(samples)-1]),
	}
}

// probeWhile calls probe every few milliseconds until stop is closed.
func probeWhile(stop <-chan struct{}, probe func() (time.Duration, error)) []time.Duration {
	var samples []time.Duration
	for {
		select {
		case <-stop:
			return samples
		case <-time.After(5 * time.Millisecond):
		}
		d, err := probe()
		if err != nil {
			return samples
		}
		samples = append(samples, d)
	}
}

// echoTCP answers every byte that it reads with the same byte.
func echoTCP(c net.Conn) {
	defer func() { _ = c.Close() }()
	_, _ = io.Copy(c, c)
}

// sinkTCP reads and drops everything.
func sinkTCP(c net.Conn) {
	defer func() { _ = c.Close() }()
	_, _ = io.Copy(io.Discard, c)
}

// tcpService starts a server that runs handle for every connection.
func tcpService(handle func(net.Conn)) string {
	GinkgoHelper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(lis.Close)
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			go handle(c)
		}
	}()
	return lis.Addr().String()
}

// sourceOrEchoTCP is the worker side of the transfer specs. The frontend opens
// every stream, as it does in production. A stream that starts with 's' asks for
// transferSize bytes in the direction from the worker to the frontend, and any
// other stream is a probe that is echoed.
func sourceOrEchoTCP(c net.Conn) {
	defer func() { _ = c.Close() }()
	kind := make([]byte, 1)
	if _, err := io.ReadFull(c, kind); err != nil {
		return
	}
	if kind[0] == 's' {
		_ = pushBytes(c, transferSize)
		return
	}
	if _, err := c.Write(kind); err != nil {
		return
	}
	_, _ = io.Copy(c, c)
}

// pullTransfer opens a stream from the frontend, asks the worker for the
// transfer and reads it to the end.
func pullTransfer(frontend *tunnel.Session) error {
	st, err := frontend.OpenStream(context.Background())
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if _, err := st.Write([]byte{'s'}); err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, st)
	return err
}

func pushBytes(w io.Writer, size int) error {
	chunk := make([]byte, 256<<10)
	for sent := 0; sent < size; sent += len(chunk) {
		if _, err := w.Write(chunk); err != nil {
			return err
		}
	}
	return nil
}

// tcpProbe returns a probe that sends 8 bytes on one connection and waits for
// the echo.
func tcpProbe(c net.Conn) func() (time.Duration, error) {
	req, rep := make([]byte, 8), make([]byte, 8)
	return func() (time.Duration, error) {
		start := time.Now()
		if _, err := c.Write(req); err != nil {
			return 0, err
		}
		if _, err := io.ReadFull(c, rep); err != nil {
			return 0, err
		}
		return time.Since(start), nil
	}
}

// lanedFrontend is a frontend that serves websockets and keeps the sessions
// that it gets, by lane, as the connect endpoint does.
type lanedFrontend struct {
	addr      string
	inference chan *tunnel.Session
	bulk      chan *tunnel.Session
}

func startLanedFrontend() *lanedFrontend {
	GinkgoHelper()
	f := &lanedFrontend{inference: make(chan *tunnel.Session, 2), bulk: make(chan *tunnel.Session, 2)}
	up := tunnel.NewUpgrader()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lane, err := tunnel.ParseLane(r.URL.Query().Get("lane"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s, err := tunnel.ServerSession(ws, lane)
		if err != nil {
			_ = ws.Close()
			return
		}
		if lane == tunnel.LaneBulk {
			f.bulk <- s
		} else {
			f.inference <- s
		}
	}))
	DeferCleanup(srv.Close)
	f.addr = srv.Listener.Addr().String()
	return f
}

// dialThrough opens the worker side of a session on lane, through the link.
func dialThrough(linkAddr string, lane tunnel.Lane) *tunnel.Session {
	GinkgoHelper()
	ws, _, err := tunnel.NewDialer(10*time.Second).Dial("ws://"+linkAddr+"/?lane="+string(lane), nil)
	Expect(err).ToNot(HaveOccurred())
	worker, err := tunnel.ClientSession(ws, lane)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = worker.Close() })
	return worker
}

// serveStreams runs handle for every stream that the other side opens.
func serveStreams(s *tunnel.Session, handle func(net.Conn)) {
	go func() {
		for {
			st, err := s.AcceptStream()
			if err != nil {
				return
			}
			go handle(st)
		}
	}()
}

// streamProbe returns a probe that opens a stream on a session, sends 8 bytes
// and waits for the echo.
func streamProbe(s *tunnel.Session) func() (time.Duration, error) {
	req, rep := make([]byte, 8), make([]byte, 8)
	return func() (time.Duration, error) {
		start := time.Now()
		st, err := s.OpenStream(context.Background())
		if err != nil {
			return 0, err
		}
		defer func() { _ = st.Close() }()
		if _, err := st.Write(req); err != nil {
			return 0, err
		}
		if _, err := io.ReadFull(st, rep); err != nil {
			return 0, err
		}
		return time.Since(start), nil
	}
}

// The transfer goes from the worker to the frontend, which is the direction
// that the benchmark of the buffers found to be the slow one. The probe is a
// call from the frontend to the worker: its request travels in the direction
// with no load, and its reply travels in the direction of the transfer, where
// it has to wait behind the bytes that are already queued.
var _ = Describe("Delay of a small call during a large transfer", Label("benchmark"), Ordered, func() {
	const (
		linkRate   = 125e6 // 1 Gbit/s
		linkOneWay = 10 * time.Millisecond

		directName = "direct: transfer and probe on two TCP connections"
		sharedName = "shared session: transfer and probe on one session"
		bulkName   = "bulk lane: transfer on the bulk session, probe on the inference session"
	)
	results := map[string]probeStats{}

	// The specs compare latencies, and a loaded runner moves them by more than
	// the margin. They run when the label is asked for:
	//   go run github.com/onsi/ginkgo/v2/ginkgo --label-filter=benchmark ./core/services/tunnel
	BeforeAll(func() {
		if !strings.Contains(GinkgoLabelFilter(), "benchmark") {
			Skip("a latency benchmark; run it with --label-filter=benchmark")
		}
	})

	// measure runs the transfer while the probes run, and records the stats of
	// the probes.
	measure := func(name string, transfer func() error, probe func() (time.Duration, error)) {
		GinkgoHelper()
		stop := make(chan struct{})
		sampled := make(chan []time.Duration, 1)
		go func() { sampled <- probeWhile(stop, probe) }()
		start := time.Now()
		Expect(transfer()).To(Succeed())
		elapsed := time.Since(start)
		close(stop)
		var samples []time.Duration
		Eventually(sampled, 30*time.Second).Should(Receive(&samples))
		Expect(len(samples)).To(BeNumerically(">=", 15), "too few probes completed during the transfer")
		// The first probes run while the transfer fills the link, so the first
		// tenth does not count.
		stats := summarize(samples[len(samples)/10:])
		results[name] = stats
		AddReportEntry(name, map[string]any{
			"transfer MiB":   transferSize >> 20,
			"transfer s":     elapsed.Seconds(),
			"transfer MiB/s": float64(transferSize>>20) / elapsed.Seconds(),
			"probes":         stats.count,
			"probe p50 ms":   stats.p50,
			"probe p99 ms":   stats.p99,
			"probe max ms":   stats.max,
		})
	}

	It("measures the baseline: the probe and the transfer on two TCP connections", func() {
		// One service behind the link. The first byte of a connection says what
		// the connection is for.
		front := tcpService(func(c net.Conn) {
			kind := make([]byte, 1)
			if _, err := io.ReadFull(c, kind); err != nil {
				_ = c.Close()
				return
			}
			if kind[0] == 'e' {
				echoTCP(c)
			} else {
				sinkTCP(c)
			}
		})
		link := newSlowLink(front, linkRate, linkOneWay)

		dial := func(kind byte) net.Conn {
			c, err := net.Dial("tcp", link.Addr())
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(c.Close)
			_, err = c.Write([]byte{kind})
			Expect(err).ToNot(HaveOccurred())
			return c
		}
		bulk, probe := dial('s'), dial('e')

		measure(directName,
			func() error { return pushBytes(bulk, transferSize) },
			tcpProbe(probe))
	})

	It("measures the probe on the same session as the transfer", func() {
		f := startLanedFrontend()
		link := newSlowLink(f.addr, linkRate, linkOneWay)
		worker := dialThrough(link.Addr(), tunnel.LaneInference)
		var frontend *tunnel.Session
		Eventually(f.inference).Should(Receive(&frontend))
		DeferCleanup(func() { _ = frontend.Close() })
		serveStreams(worker, sourceOrEchoTCP)

		measure(sharedName,
			func() error { return pullTransfer(frontend) },
			streamProbe(frontend))
	})

	It("measures the probe on the inference lane with the transfer on the bulk lane", func() {
		f := startLanedFrontend()
		// Both sessions go through the same link, as the two websockets of one
		// worker do.
		link := newSlowLink(f.addr, linkRate, linkOneWay)
		inferenceWorker := dialThrough(link.Addr(), tunnel.LaneInference)
		bulkWorker := dialThrough(link.Addr(), tunnel.LaneBulk)
		var inferenceFrontend, bulkFrontend *tunnel.Session
		Eventually(f.inference).Should(Receive(&inferenceFrontend))
		Eventually(f.bulk).Should(Receive(&bulkFrontend))
		DeferCleanup(func() { _ = inferenceFrontend.Close(); _ = bulkFrontend.Close() })
		serveStreams(inferenceWorker, sourceOrEchoTCP)
		serveStreams(bulkWorker, sourceOrEchoTCP)

		measure(bulkName,
			func() error { return pullTransfer(bulkFrontend) },
			streamProbe(inferenceFrontend))
	})

	It("keeps the probe on the bulk lane within 10 ms of the direct baseline at the 99th percentile", func() {
		direct, bulk, shared := results[directName], results[bulkName], results[sharedName]
		Expect(bulk.p99).To(BeNumerically("<=", direct.p99+10),
			"bulk lane p99 %.1f ms, direct p99 %.1f ms", bulk.p99, direct.p99)
		Expect(shared.p99).To(BeNumerically(">", bulk.p99),
			"a shared session must delay the probe more than the bulk lane: shared p99 %.1f ms, bulk p99 %.1f ms", shared.p99, bulk.p99)
	})
})
