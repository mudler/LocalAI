package guestagent

import (
	"errors"
	"net"
	"os"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// unixListener returns a listening unix socket wrapped the way ListenVsock
// wraps a vsock socket, so the accept path runs on a real kernel socket.
func unixListener() (*vsockListener, string) {
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC, 0)
	Expect(err).ToNot(HaveOccurred())
	path := GinkgoT().TempDir() + "/s"
	Expect(syscall.Bind(fd, &syscall.SockaddrUnix{Name: path})).To(Succeed())
	Expect(syscall.Listen(fd, 8)).To(Succeed())
	// A unix socket has no CID, so the test accepts every peer.
	return &vsockListener{f: os.NewFile(uintptr(fd), "test-listener"), port: 1, allow: func(sockaddrVM) bool { return true }}, path
}

var _ = Describe("vsock listener", func() {
	It("returns ErrClosed from Accept after Close", func() {
		l, _ := unixListener()
		errc := make(chan error, 1)
		go func() {
			_, err := l.Accept() // blocks: nobody dials
			errc <- err
		}()
		time.Sleep(50 * time.Millisecond)
		Expect(l.Close()).To(Succeed())
		select {
		case err := <-errc:
			Expect(err).To(MatchError(net.ErrClosed), "Accept after Close")
		case <-time.After(5 * time.Second):
			Fail("Accept did not return after Close")
		}
		_, err := l.Accept()
		Expect(err).To(MatchError(net.ErrClosed), "Accept on a closed listener")
	})

	It("still accepts", func() {
		l, path := unixListener()
		defer func() { _ = l.Close() }()
		go func() {
			c, err := net.Dial("unix", path)
			if err == nil {
				_ = c.Close()
			}
		}()
		c, err := l.Accept()
		Expect(err).ToNot(HaveOccurred())
		_ = c.Close()
	})

	It("allows only the host as a peer", func() {
		for cid, want := range map[uint32]bool{HostCID: true, 0: false, 1: false, 3: false, 4: false, cidAny: false} {
			Expect(peerAllowed(cid)).To(Equal(want), "peerAllowed(%d)", cid)
		}
	})

	// Accept closes a connection from a peer the check refuses and returns the
	// next one it accepts.
	It("drops other peers", func() {
		l, path := unixListener()
		defer func() { _ = l.Close() }()
		var seen int
		l.allow = func(sockaddrVM) bool { seen++; return seen > 1 }
		first, err := net.Dial("unix", path)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = first.Close() }()
		second, err := net.Dial("unix", path)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = second.Close() }()
		c, err := l.Accept()
		Expect(err).ToNot(HaveOccurred())
		_ = c.Close()
		Expect(seen).To(Equal(2), "the check ran twice")
		// The refused connection is closed by the listener side.
		_ = first.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = first.Read(make([]byte, 1))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, os.ErrDeadlineExceeded)).To(BeFalse(), "the refused connection was not closed: %v", err)
	})
})
