package p2pnet_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

func testHandler() http.Handler {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "hello %s", r.RemoteAddr)
	})
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for i := range 3 {
			_, _ = fmt.Fprintf(w, "data: event %d\n\n", i)
			f.Flush()
		}
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		for {
			t, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(t, b); err != nil {
				return
			}
		}
	})
	return mux
}

// freePort returns a loopback port that is free now.
func freePort() string {
	GinkgoHelper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	addr := l.Addr().String()
	Expect(l.Close()).To(Succeed())
	return addr
}

var _ = Describe("Forward", func() {
	var server, client *p2pnet.Host
	var fwd *p2pnet.Forwarder
	var states chan p2pnet.ForwardStatus
	var base string

	BeforeEach(func() {
		server = newHost(p2pnet.Config{})
		client = newHost(p2pnet.Config{})
		pair(server, client)
		l, err := server.Listen(p2pnet.ProtoHTTP, nil)
		Expect(err).ToNot(HaveOccurred())
		srv := &http.Server{Handler: testHandler(), ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = srv.Serve(l) }()
		DeferCleanup(srv.Close)

		states = make(chan p2pnet.ForwardStatus, 16)
		addr := freePort()
		fwd, err = client.Forward(context.Background(), server.ID(), p2pnet.ProtoHTTP, addr,
			p2pnet.WithStatus(func(s p2pnet.ForwardStatus) { states <- s }))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(fwd.Close)
		Expect(fwd.Addr().String()).To(Equal(addr))
		base = "http://" + addr
	})

	It("forwards HTTP and reports the path", func() {
		resp, err := httpclient.New().Get(base + "/hello")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal("hello " + client.ID().String()))
		var st p2pnet.ForwardStatus
		Eventually(states).Should(Receive(&st))
		Expect(st.State).To(Equal(p2pnet.ForwardUp))
		Expect(st.Path.Kind).To(Equal(p2pnet.PathDirect))
	})

	It("forwards server-sent events", func() {
		resp, err := httpclient.New().Get(base + "/sse")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		var events []string
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if after, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				events = append(events, after)
			}
		}
		Expect(sc.Err()).ToNot(HaveOccurred())
		Expect(events).To(Equal([]string{"event 0", "event 1", "event 2"}))
	})

	It("forwards a websocket upgrade", func() {
		ws, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(base, "http")+"/ws", nil)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		defer func() { _ = ws.Close() }()
		for i := range 3 {
			msg := fmt.Sprintf("message %d", i)
			Expect(ws.WriteMessage(websocket.TextMessage, []byte(msg))).To(Succeed())
			_, got, err := ws.ReadMessage()
			Expect(err).ToNot(HaveOccurred())
			Expect(string(got)).To(Equal(msg))
		}
	})

	It("keeps the port, reports down when the peer is gone and closes", func() {
		_, err := client.Forward(context.Background(), server.ID(), p2pnet.ProtoHTTP, fwd.Addr().String())
		Expect(err).To(HaveOccurred(), "a busy port must be an error")

		Expect(server.Close()).To(Succeed())
		Expect(client.Libp2p().Network().ClosePeer(server.ID())).To(Succeed())
		_, err = httpclient.NewWithTimeout(30 * time.Second).Get(base + "/hello")
		Expect(err).To(HaveOccurred())
		var st p2pnet.ForwardStatus
		Eventually(states, 30*time.Second).Should(Receive(&st))
		Expect(st.State).To(Equal(p2pnet.ForwardDown))
		Expect(st.Err).To(HaveOccurred())

		Expect(fwd.Close()).To(Succeed())
		Eventually(states).Should(Receive(&st))
		Expect(st.State).To(Equal(p2pnet.ForwardClosed))
		_, err = net.DialTimeout("tcp", fwd.Addr().String(), time.Second)
		Expect(err).To(HaveOccurred())
	})

	It("refuses an address that is not loopback", func() {
		_, err := client.Forward(context.Background(), server.ID(), p2pnet.ProtoHTTP, "0.0.0.0:0")
		Expect(err).To(HaveOccurred())
	})

	It("serves concurrent requests", func() {
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Go(func() {
				resp, err := httpclient.New().Get(base + "/hello")
				if err == nil {
					_, err = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			Expect(err).ToNot(HaveOccurred())
		}
	})
})
