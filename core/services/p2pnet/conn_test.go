package p2pnet_test

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

var _ = Describe("Streams", func() {
	var server, client *p2pnet.Host

	BeforeEach(func() {
		server = newHost(p2pnet.Config{})
		client = newHost(p2pnet.Config{})
		pair(server, client)
	})

	It("echoes over Dial and Listen, with peer addresses on both ends", func() {
		l, err := server.Listen(protoTest, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(l.Close)
		peers := make(chan net.Conn, 1)
		serve(l, func(c net.Conn) { peers <- c; echo(c) })

		c, err := client.Dial(dialCtx(), server.ID(), protoTest)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = c.Close() }()
		_, err = c.Write([]byte("ping"))
		Expect(err).ToNot(HaveOccurred())
		buf := make([]byte, 4)
		_, err = io.ReadFull(c, buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(buf)).To(Equal("ping"))

		Expect(c.RemoteAddr().Network()).To(Equal("libp2p"))
		Expect(c.RemoteAddr().String()).To(Equal(server.ID().String()))
		Expect(c.LocalAddr().String()).To(Equal(client.ID().String()))
		p, ok := p2pnet.PeerOf(c)
		Expect(ok).To(BeTrue())
		Expect(p).To(Equal(server.ID()))

		var sc net.Conn
		Eventually(peers).Should(Receive(&sc))
		p, err = p2pnet.DecodePeerID(sc.RemoteAddr().String())
		Expect(err).ToNot(HaveOccurred())
		Expect(p).To(Equal(client.ID()))

		Expect(client.Path(server.ID()).Kind).To(Equal(p2pnet.PathDirect))
		Expect(client.Path(server.ID()).Limited).To(BeFalse())
	})

	It("refuses a second listener on the same protocol", func() {
		l, err := server.Listen(protoTest, nil)
		Expect(err).ToNot(HaveOccurred())
		_, err = server.Listen(protoTest, nil)
		Expect(err).To(HaveOccurred())
		Expect(l.Close()).To(Succeed())
		l2, err := server.Listen(protoTest, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(l2.Close()).To(Succeed())
		_, err = l2.Accept()
		Expect(errors.Is(err, net.ErrClosed)).To(BeTrue())
	})

	It("reports a read deadline as a timeout", func() {
		l, err := server.Listen(protoTest, nil)
		Expect(err).ToNot(HaveOccurred())
		hold := make(chan struct{})
		DeferCleanup(func() { close(hold) })
		serve(l, func(c net.Conn) { <-hold; _ = c.Close() })

		c, err := client.Dial(dialCtx(), server.ID(), protoTest)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = c.Close() }()
		_, err = c.Write([]byte("x"))
		Expect(err).ToNot(HaveOccurred())
		Expect(c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))).To(Succeed())
		_, err = c.Read(make([]byte, 1))
		var ne net.Error
		Expect(errors.As(err, &ne)).To(BeTrue(), "error %v is not a net.Error", err)
		Expect(ne.Timeout()).To(BeTrue())
	})

	It("delivers a reply written after the client half-closes", func() {
		l, err := server.Listen(protoTest, nil)
		Expect(err).ToNot(HaveOccurred())
		serve(l, func(c net.Conn) {
			n, _ := io.Copy(io.Discard, c)
			_, _ = fmt.Fprintf(c, "got %d bytes", n)
			_ = c.Close()
		})

		c, err := client.Dial(dialCtx(), server.ID(), protoTest)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = c.Close() }()
		_, err = c.Write([]byte("hello"))
		Expect(err).ToNot(HaveOccurred())
		Expect(c.(*p2pnet.Conn).CloseWrite()).To(Succeed())
		reply, err := io.ReadAll(c)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(reply)).To(Equal("got 5 bytes"))
	})

	It("moves a large transfer intact", func() {
		const size = 16 << 20
		l, err := server.Listen(protoTest, nil)
		Expect(err).ToNot(HaveOccurred())
		serve(l, func(c net.Conn) {
			h := sha256.New()
			_, _ = io.Copy(h, c)
			_, _ = c.Write(h.Sum(nil))
			_ = c.Close()
		})

		c, err := client.Dial(dialCtx(), server.ID(), protoTest)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = c.Close() }()
		h := sha256.New()
		_, err = io.Copy(io.MultiWriter(c, h), io.LimitReader(rand.Reader, size))
		Expect(err).ToNot(HaveOccurred())
		Expect(c.(*p2pnet.Conn).CloseWrite()).To(Succeed())
		sum, err := io.ReadAll(c)
		Expect(err).ToNot(HaveOccurred())
		Expect(sum).To(Equal(h.Sum(nil)))
	})

	It("serves HTTP to HTTPClient with the peer ID as RemoteAddr", func() {
		l, err := server.Listen(p2pnet.ProtoHTTP, nil)
		Expect(err).ToNot(HaveOccurred())
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, err := p2pnet.DecodePeerID(r.RemoteAddr)
			_, _ = fmt.Fprintf(w, "%v %s", err == nil, p)
		}), ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = srv.Serve(l) }()
		DeferCleanup(srv.Close)

		resp, err := client.HTTPClient(server.ID()).Get("http://" + server.ID().String() + "/hello")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal("true " + client.ID().String()))
	})

	It("does not parse a TCP address as a peer", func() {
		_, err := p2pnet.DecodePeerID("127.0.0.1:8080")
		Expect(err).To(HaveOccurred())
		a, b := net.Pipe()
		defer func() { _ = a.Close() }()
		defer func() { _ = b.Close() }()
		_, ok := p2pnet.PeerOf(a)
		Expect(ok).To(BeFalse())
	})

	It("reports connections on WatchPaths", func() {
		ctx := dialCtx()
		events := client.WatchPaths(ctx)
		Expect(client.Libp2p().Connect(ctx, addrInfo(server))).To(Succeed())
		var ev p2pnet.PathEvent
		Eventually(events).Should(Receive(&ev))
		Expect(ev.Peer).To(Equal(server.ID()))
		Expect(ev.Path.Kind).To(Equal(p2pnet.PathDirect))

		Expect(client.Libp2p().Network().ClosePeer(server.ID())).To(Succeed())
		Eventually(func() p2pnet.PathKind {
			select {
			case ev = <-events:
			default:
			}
			return ev.Path.Kind
		}).Should(Equal(p2pnet.PathNone))
	})
})
