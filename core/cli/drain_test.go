package cli

import (
	"context"
	"io"
	"net"
	nethttp "net/http"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("drainHTTPServer", func() {
	var (
		srv      *echo.Echo
		base     string
		started  chan struct{}
		finish   chan struct{}
		drained  atomic.Bool
		serveErr chan error
	)

	BeforeEach(func() {
		started = make(chan struct{})
		finish = make(chan struct{})
		drained.Store(false)
		srv = echo.New()
		srv.HideBanner = true
		srv.HidePort = true
		srv.GET("/slow", func(c echo.Context) error {
			close(started)
			select {
			case <-finish:
			case <-c.Request().Context().Done():
			}
			return c.String(nethttp.StatusOK, "done")
		})
		l, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		srv.Listener = l
		base = "http://" + l.Addr().String()
		serveErr = make(chan error, 1)
		go func() { serveErr <- srv.Start("") }()
		Eventually(func() error {
			c, err := net.Dial("tcp", l.Addr().String())
			if err == nil {
				_ = c.Close()
			}
			return err
		}).Should(Succeed())
	})

	AfterEach(func() { _ = srv.Close() })

	It("lets a slow request finish within the timeout", func() {
		body := make(chan string, 1)
		go func() {
			resp, err := nethttp.Get(base + "/slow")
			if err != nil {
				body <- "error: " + err.Error()
				return
			}
			defer func() { _ = resp.Body.Close() }()
			b, _ := io.ReadAll(resp.Body)
			body <- string(b)
		}()
		Eventually(started).Should(BeClosed())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		drainDone := make(chan struct{})
		go func() {
			defer close(drainDone)
			drainHTTPServer(ctx, srv, func() { drained.Store(true) })
		}()
		Eventually(drained.Load).Should(BeTrue())
		Consistently(drainDone, 200*time.Millisecond).ShouldNot(BeClosed())

		close(finish)
		Eventually(body).Should(Receive(Equal("done")))
		Eventually(drainDone).Should(BeClosed())
		Expect(<-serveErr).To(MatchError(nethttp.ErrServerClosed))
	})

	It("stops accepting new connections while draining", func() {
		go func() { _, _ = nethttp.Get(base + "/slow") }()
		Eventually(started).Should(BeClosed())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		go drainHTTPServer(ctx, srv, func() {})
		Eventually(func() error {
			c, err := net.DialTimeout("tcp", srv.Listener.Addr().String(), 200*time.Millisecond)
			if err == nil {
				_ = c.Close()
			}
			return err
		}).Should(HaveOccurred())
		close(finish)
	})

	It("closes the request when the timeout passes first", func() {
		errc := make(chan error, 1)
		go func() {
			resp, err := nethttp.Get(base + "/slow")
			if err == nil {
				_ = resp.Body.Close()
			}
			errc <- err
		}()
		Eventually(started).Should(BeClosed())

		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		start := time.Now()
		drainHTTPServer(ctx, srv, func() {})
		Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
		Eventually(errc).Should(Receive())
	})
})
