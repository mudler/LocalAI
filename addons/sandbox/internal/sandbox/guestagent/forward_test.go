package guestagent

import (
	"bufio"
	"errors"
	"io"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// flakyListener fails its first Accept with a transient error.
type flakyListener struct {
	net.Listener
	failed bool
}

func (l *flakyListener) Accept() (net.Conn, error) {
	if !l.failed {
		l.failed = true
		return nil, errors.New("transient accept error")
	}
	return l.Listener.Accept()
}

var _ = Describe("Forward", func() {
	It("half-closes", func() {
		up, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = up.Close() }()
		go func() {
			c, err := up.Accept()
			if err != nil {
				return
			}
			b, _ := io.ReadAll(c) // until the client half-closes
			_, _ = c.Write(append([]byte("got "), b...))
			_ = c.Close()
		}()
		front, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = front.Close() }()
		go func() { _ = Forward(front, func() (net.Conn, error) { return net.Dial("tcp", up.Addr().String()) }) }()
		c, err := net.Dial("tcp", front.Addr().String())
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = c.Close() }()
		_, err = c.Write([]byte("hello"))
		Expect(err).ToNot(HaveOccurred())
		_ = c.(*net.TCPConn).CloseWrite()
		line, _ := bufio.NewReader(c).ReadString('\n')
		Expect(line).To(Equal("got hello"))
	})

	It("survives a transient accept error", func() {
		up, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = up.Close() }()
		go func() {
			for {
				c, err := up.Accept()
				if err != nil {
					return
				}
				go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
			}
		}()
		base, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		front := &flakyListener{Listener: base}
		done := make(chan error, 1)
		go func() {
			done <- Forward(front, func() (net.Conn, error) { return net.Dial("tcp", up.Addr().String()) })
		}()
		c, err := net.Dial("tcp", base.Addr().String())
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, err = c.Write([]byte("ping\n"))
		Expect(err).ToNot(HaveOccurred())
		line, err := bufio.NewReader(c).ReadString('\n')
		Expect(err).ToNot(HaveOccurred())
		Expect(line).To(Equal("ping\n"), "echo after a transient accept error")
		_ = base.Close()
		Expect(<-done).To(MatchError(net.ErrClosed), "Forward on a closed listener")
	})
})
