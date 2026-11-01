//go:build linux

package gvisor

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

var _ = Describe("port serving", func() {
	It("caps connections and closeConns closes them", func() {
		dir := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(dir, "ctl"), 0o700)).To(Succeed())
		held := make(chan net.Conn, maxPortConns+4)
		ps, err := servePorts(dir, map[int]sandbox.PortHandler{7000: func(ctx context.Context, c net.Conn) {
			held <- c
			// Returns when the host closes the connection.
			_, _ = io.Copy(io.Discard, c)
		}})
		Expect(err).ToNot(HaveOccurred())
		defer ps.close()
		var guest []net.Conn
		for range maxPortConns + 2 {
			c, err := net.Dial("unix", portSock(dir, 7000))
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = c.Close() }()
			guest = append(guest, c)
		}
		// Wait until the host closed the two connections over the cap.
		deadline := time.Now().Add(10 * time.Second)
		closed := 0
		for closed != 2 {
			Expect(time.Now().After(deadline)).To(BeFalse(), "connections closed by the host = %d, want 2", closed)
			closed = 0
			for _, c := range guest {
				_ = c.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
				if _, err := c.Read(make([]byte, 1)); err != nil && !isTimeout(err) {
					closed++
				}
			}
		}
		for len(held) < maxPortConns && time.Now().Before(deadline) {
			runtime.Gosched()
		}
		Expect(held).To(HaveLen(maxPortConns), "handlers running, want the cap")
		ps.closeConns()
		for _, c := range guest {
			_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, err := c.Read(make([]byte, 1))
			Expect(err).To(HaveOccurred(), "a connection survived closeConns")
			Expect(isTimeout(err)).To(BeFalse(), "a connection survived closeConns: %v", err)
		}
		for len(held) > 0 {
			<-held
		}
		// New connections are served after closeConns: the handlers returned, so
		// slots free up. A slot may free a moment after the connection closed,
		// so try until a handler gets one.
		for {
			Expect(time.Now().After(deadline.Add(10*time.Second))).To(BeFalse(), "no connection was served after closeConns")
			c, err := net.Dial("unix", portSock(dir, 7000))
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = c.Close() }()
			select {
			case <-held:
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	})

	It("refuses connections to a connSet after shutdown", func() {
		var set connSet
		a, b := net.Pipe()
		defer func() { _ = b.Close() }()
		tc := set.add(a)
		Expect(tc).ToNot(BeNil(), "add refused before shutdown")
		set.shutdown()
		_, err := b.Read(make([]byte, 1))
		Expect(err).To(HaveOccurred(), "a tracked connection survived shutdown")
		c, d := net.Pipe()
		defer func() { _ = d.Close() }()
		Expect(set.add(c)).To(BeNil(), "add accepted a connection after shutdown")
		_, err = d.Read(make([]byte, 1))
		Expect(err).To(HaveOccurred(), "the late connection was not closed")
	})

	It("compares ports with samePorts", func() {
		h := func(context.Context, net.Conn) {}
		Expect(samePorts([]int{3128, 7000}, map[int]sandbox.PortHandler{7000: h, 3128: h})).To(BeTrue())
		Expect(samePorts([]int{3128}, map[int]sandbox.PortHandler{7000: h})).To(BeFalse())
	})
})

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}
