// SPDX-License-Identifier: MIT
package diagnostics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeSampling struct {
	mu           sync.Mutex
	mutex, block []int
}

func (f *fakeSampling) SetMutexProfileFraction(v int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mutex = append(f.mutex, v)
	return 0
}
func (f *fakeSampling) SetBlockProfileRate(v int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.block = append(f.block, v)
}
func (f *fakeSampling) calls() [2][]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return [2][]int{append([]int(nil), f.mutex...), append([]int(nil), f.block...)}
}

type failingListener struct {
	net.Listener
	err error
}

func (l failingListener) Accept() (net.Conn, error) { return nil, l.err }

// A read signals that net/http has registered the connection before shutdown.
type readingListener struct {
	net.Listener
	reading chan struct{}
}

func (l readingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &readingConn{Conn: c, reading: l.reading}, nil
}

type readingConn struct {
	net.Conn
	reading chan struct{}
	once    sync.Once
}

func (c *readingConn) Read(p []byte) (int, error) {
	c.once.Do(func() { close(c.reading) })
	return c.Conn.Read(p)
}

func enabledOptions() Options { o := DefaultOptions(); o.Pprof = true; return o }
func launch(sampling samplingControl) *Server {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = l.Close() })
	s, err := start(enabledOptions(), func(network, address string) (net.Listener, error) {
		Expect(network).To(Equal("tcp"))
		Expect(address).To(Equal("127.0.0.1:6060"))
		return l, nil
	}, sampling)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s
}

var _ = Describe("Diagnostics server", Serial, func() {
	It("is inert even with debug logging or request timing enabled", func() {
		old := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
		defer slog.SetDefault(old)
		for _, timing := range []bool{false, true} {
			f := &fakeSampling{}
			o := DefaultOptions()
			o.RequestPhaseTiming = timing
			s, err := start(o, func(string, string) (net.Listener, error) { Fail("disabled listener called"); return nil, nil }, f)
			Expect(err).NotTo(HaveOccurred())
			Expect(s).NotTo(BeNil())
			Expect(s.Addr()).To(BeNil())
			Expect(s.Errors()).To(BeNil())
			Expect(s.Shutdown(context.Background())).To(Succeed())
			Expect(s.Shutdown(context.Background())).To(Succeed())
			Expect(f.calls()).To(Equal([2][]int{}))
		}
	})
	It("validates before binding and reports bind failures without sampling", func() {
		f := &fakeSampling{}
		o := enabledOptions()
		o.Address = "localhost:6060"
		s, err := start(o, func(string, string) (net.Listener, error) { Fail("invalid address bound"); return nil, nil }, f)
		Expect(err).To(HaveOccurred())
		Expect(s).To(BeNil())
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer l.Close()
		o.Address = l.Addr().String()
		s, err = start(o, net.Listen, f)
		Expect(err).To(HaveOccurred())
		Expect(s).To(BeNil())
		Expect(f.calls()).To(Equal([2][]int{}))
	})
	It("serves explicit standard profiles without leaking default mux or cmdline", func() {
		old := http.DefaultServeMux
		http.DefaultServeMux = http.NewServeMux()
		defer func() { http.DefaultServeMux = old }()
		http.DefaultServeMux.HandleFunc("/private-sentinel", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("leaked")) })
		s := launch(&fakeSampling{})
		client := httpclient.NewWithTimeout(5 * time.Second)
		defer client.CloseIdleConnections()
		for _, path := range []string{"/debug/pprof/", "/debug/pprof/profile?seconds=1", "/debug/pprof/trace?seconds=0.01", "/debug/pprof/symbol", "/debug/pprof/heap", "/debug/pprof/goroutine?debug=1", "/debug/pprof/mutex", "/debug/pprof/block", "/debug/pprof/allocs", "/debug/pprof/threadcreate"} {
			res, err := client.Get("http://" + s.Addr().String() + path)
			Expect(err).NotTo(HaveOccurred(), path)
			body, err := io.ReadAll(res.Body)
			res.Body.Close()
			Expect(err).NotTo(HaveOccurred())
			Expect(res.StatusCode).To(Equal(200), path)
			Expect(body).NotTo(BeEmpty(), path)
		}
		for _, path := range []string{"/debug/pprof/cmdline", "/debug/pprof/cmdline?debug=1", "/debug/pprof/cmdline/", "/debug/pprof/cmdline/anything?x=1", "/private-sentinel", "/debug/pprof/unknown"} {
			res, err := client.Get("http://" + s.Addr().String() + path)
			Expect(err).NotTo(HaveOccurred())
			res.Body.Close()
			Expect(res.StatusCode).To(Equal(404), path)
		}
		Expect(s.httpServer.ReadHeaderTimeout).To(Equal(5 * time.Second))
		Expect(s.httpServer.IdleTimeout).To(Equal(60 * time.Second))
		Expect(s.httpServer.WriteTimeout).To(BeZero())
		Expect(s.httpServer.MaxHeaderBytes).To(Equal(1 << 20))
	})
	It("applies rates after binding and resets once on repeated shutdown", func() {
		f := &fakeSampling{}
		o := enabledOptions()
		o.MutexProfileFraction = 7
		o.BlockProfileRate = 123
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer l.Close()
		s, err := start(o, func(string, string) (net.Listener, error) { Expect(f.calls()).To(Equal([2][]int{})); return l, nil }, f)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.calls()).To(Equal([2][]int{{7}, {123}}))
		Expect(s.Shutdown(context.Background())).To(Succeed())
		Expect(s.Shutdown(context.Background())).To(Succeed())
		Expect(s.Errors()).To(BeClosed())
		Expect(f.calls()).To(Equal([2][]int{{7, 0}, {123, 0}}))
		rebound, err := net.Listen("tcp", s.Addr().String())
		Expect(err).NotTo(HaveOccurred())
		rebound.Close()
	})
	It("buffers an immediate unexpected Serve failure exactly once", func() {
		f := &fakeSampling{}
		boom := errors.New("accept failed")
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer l.Close()
		s, err := start(enabledOptions(), func(string, string) (net.Listener, error) { return failingListener{l, boom}, nil }, f)
		Expect(err).NotTo(HaveOccurred())
		Eventually(s.Errors()).Should(Receive(MatchError(boom)))
		Eventually(s.Errors()).Should(BeClosed())
		Expect(s.Shutdown(context.Background())).To(Succeed())
		Expect(f.calls()).To(Equal([2][]int{{0, 0}, {0, 0}}))
	})
	It("force closes active connections on graceful expiry and waits for Serve", func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer l.Close()
		reading := make(chan struct{})
		s, err := start(enabledOptions(), func(string, string) (net.Listener, error) {
			return readingListener{Listener: l, reading: reading}, nil
		}, &fakeSampling{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = s.Shutdown(ctx)
		})
		conn, err := net.Dial("tcp", s.Addr().String())
		Expect(err).NotTo(HaveOccurred())
		defer conn.Close()
		_, err = conn.Write([]byte("GET /debug/pprof/ HTTP/1.1\r\nHost: localhost\r\n"))
		Expect(err).NotTo(HaveOccurred())
		Eventually(reading).Should(BeClosed())
		// An incomplete header stays StateNew until header timeout; Shutdown must force-close it.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		Expect(s.Shutdown(ctx)).To(MatchError(context.DeadlineExceeded))
		Expect(s.Shutdown(context.Background())).To(MatchError(context.DeadlineExceeded))
		Expect(s.Errors()).To(BeClosed())
		Expect(conn.SetReadDeadline(time.Now().Add(time.Second))).To(Succeed())
		_, err = conn.Read(make([]byte, 1))
		Expect(err).To(HaveOccurred())
	})
	It("supports serial runtime sampling and leaves it disabled", func() {
		previous := runtime.SetMutexProfileFraction(0)
		defer runtime.SetMutexProfileFraction(previous)
		defer runtime.SetBlockProfileRate(0)
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer l.Close()
		o := enabledOptions()
		o.Address = l.Addr().String()
		l.Close()
		o.MutexProfileFraction = 2
		o.BlockProfileRate = 1
		s, err := Start(o)
		Expect(err).NotTo(HaveOccurred())
		defer s.Shutdown(context.Background())
		Expect(runtime.SetMutexProfileFraction(-1)).To(Equal(2))
		Expect(s.Shutdown(context.Background())).To(Succeed())
		Expect(runtime.SetMutexProfileFraction(-1)).To(BeZero())
	})
})
