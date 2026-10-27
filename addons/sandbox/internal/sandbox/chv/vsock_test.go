package chv

import (
	"bufio"
	"context"
	"io"
	"net"
	"path/filepath"
	"testing"
)

// fakeMuxer answers the hybrid vsock handshake like Cloud Hypervisor: OK for
// port 1024, then echo; a closed connection for any other port.
func fakeMuxer(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(shortDir(t), "vsock.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				line, _ := r.ReadString('\n')
				if line != "CONNECT 1024\n" {
					return
				}
				_, _ = io.WriteString(c, "OK 1073741824\n")
				_, _ = io.Copy(c, r)
			}()
		}
	}()
	return sock
}

func TestDialGuest(t *testing.T) {
	sock := fakeMuxer(t)
	c, err := dialGuest(context.Background(), sock, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(c, "ping\n"); err != nil {
		t.Fatal(err)
	}
	if line, _ := bufio.NewReader(c).ReadString('\n'); line != "ping\n" {
		t.Fatalf("echo = %q", line)
	}
	c.Close()
	if _, err := dialGuest(context.Background(), sock, 2000); err == nil {
		t.Fatal("a port without a listener connected")
	}
}
