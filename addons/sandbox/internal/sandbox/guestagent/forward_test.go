package guestagent

import (
	"bufio"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestForwardHalfClose(t *testing.T) {
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = front.Close() }()
	go func() { _ = Forward(front, func() (net.Conn, error) { return net.Dial("tcp", up.Addr().String()) }) }()
	c, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	_ = c.(*net.TCPConn).CloseWrite()
	line, _ := bufio.NewReader(c).ReadString('\n')
	if line != "got hello" {
		t.Fatalf("reply = %q", line)
	}
}

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

func TestForwardSurvivesTransientAcceptError(t *testing.T) {
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = up.Close() }()
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); c.Close() }()
		}
	}()
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	front := &flakyListener{Listener: base}
	done := make(chan error, 1)
	go func() {
		done <- Forward(front, func() (net.Conn, error) { return net.Dial("tcp", up.Addr().String()) })
	}()
	c, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || line != "ping\n" {
		t.Fatalf("echo after a transient accept error: %q, %v", line, err)
	}
	_ = base.Close()
	if err := <-done; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Forward on a closed listener returned %v, want net.ErrClosed", err)
	}
}
