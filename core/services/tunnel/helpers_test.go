package tunnel_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// sessionPair opens a real websocket on a loopback socket and starts the two
// sides of a session on it. The server side is the frontend and the client side
// is the worker.
func sessionPair(lane tunnel.Lane) (server, client *tunnel.Session) {
	GinkgoHelper()
	upgrader := tunnel.NewUpgrader()
	serverCh := make(chan *tunnel.Session, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s, err := tunnel.ServerSession(ws, lane)
		if err != nil {
			_ = ws.Close()
			return
		}
		serverCh <- s
	}))
	DeferCleanup(srv.Close)

	url := "ws" + srv.URL[len("http"):]
	ws, _, err := tunnel.NewDialer(5*time.Second).Dial(url, nil)
	Expect(err).ToNot(HaveOccurred())
	client, err = tunnel.ClientSession(ws, lane)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = client.Close() })

	Eventually(serverCh).Should(Receive(&server))
	DeferCleanup(func() { _ = server.Close() })
	return server, client
}

// tcpPair returns the two ends of a loopback TCP connection.
func tcpPair() (a, b *net.TCPConn) {
	GinkgoHelper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = lis.Close() }()
	accepted := make(chan *net.TCPConn, 1)
	go func() {
		c, err := lis.Accept()
		if err == nil {
			accepted <- c.(*net.TCPConn)
		}
	}()
	c, err := net.Dial("tcp", lis.Addr().String())
	Expect(err).ToNot(HaveOccurred())
	a = c.(*net.TCPConn)
	Eventually(accepted).Should(Receive(&b))
	DeferCleanup(func() { _ = a.Close(); _ = b.Close() })
	return a, b
}
