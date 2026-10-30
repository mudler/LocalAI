package chv

import (
	"bufio"
	"context"
	"io"
	"net"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeMuxer answers the hybrid vsock handshake like Cloud Hypervisor: OK for
// port 1024, then echo; a closed connection for any other port.
func fakeMuxer() string {
	GinkgoHelper()
	sock := filepath.Join(shortDir(), "vsock.sock")
	l, err := net.Listen("unix", sock)
	Expect(err).To(Succeed())
	DeferCleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
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

var _ = Describe("dialGuest", func() {
	It("connects to the guest port and refuses a port without a listener", func() {
		sock := fakeMuxer()
		c, err := dialGuest(context.Background(), sock, 1024)
		Expect(err).To(Succeed())
		_, err = io.WriteString(c, "ping\n")
		Expect(err).To(Succeed())
		line, _ := bufio.NewReader(c).ReadString('\n')
		Expect(line).To(Equal("ping\n"), "echo")
		_ = c.Close()
		_, err = dialGuest(context.Background(), sock, 2000)
		Expect(err).To(HaveOccurred(), "a port without a listener connected")
	})
})
