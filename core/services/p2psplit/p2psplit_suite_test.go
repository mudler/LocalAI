package p2psplit_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

func TestP2PSplit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "p2psplit test suite")
}

// newHost starts a loopback host with a fresh identity that admits peers.
func newHost(peers ...p2pnet.ID) (*p2pnet.Host, *p2pnet.Allowlist) {
	GinkgoHelper()
	gate := p2pnet.NewAllowlist(peers...)
	h, err := p2pnet.New(context.Background(), p2pnet.Config{
		IdentityPath: filepath.Join(GinkgoT().TempDir(), "identity.key"),
		ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
		Gate:         gate,
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(h.Close)
	return h, gate
}

// echoServer listens on addr ("127.0.0.1:0" for any port), echoes each
// connection with a prefix, and returns its address.
func echoServer(addr, prefix string) string {
	GinkgoHelper()
	l, err := net.Listen("tcp", addr)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(l.Close)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 64)
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				_, _ = c.Write(append([]byte(prefix), buf[:n]...))
			}()
		}
	}()
	return l.Addr().String()
}

// roundTrip sends msg to addr and returns the answer.
func roundTrip(addr, msg string) (string, error) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return "", err
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte(msg)); err != nil {
		return "", err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}
	b, err := io.ReadAll(c)
	return string(b), err
}

// readHostfile reads an MLX ring hostfile.
func readHostfile(path string) []string {
	GinkgoHelper()
	b, err := os.ReadFile(path)
	Expect(err).ToNot(HaveOccurred())
	var out []string
	Expect(json.Unmarshal(b, &out)).To(Succeed())
	return out
}
