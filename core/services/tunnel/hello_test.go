package tunnel_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("The hello of a tunnel stream over P2P", func() {
	It("reads back what was written", func() {
		var buf bytes.Buffer
		in := tunnel.Hello{V: tunnel.HelloVersion, NodeID: "n1", Lane: tunnel.LaneBulk, Token: "secret"}
		Expect(tunnel.WriteHello(&buf, in)).To(Succeed())
		out, err := tunnel.ReadHello(&buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal(in))
	})

	It("does not read past the frame, so the session that follows is intact", func() {
		var buf bytes.Buffer
		Expect(tunnel.WriteHelloReply(&buf, tunnel.HelloReply{Status: http.StatusOK})).To(Succeed())
		buf.WriteString("yamux bytes")
		r, err := tunnel.ReadHelloReply(&buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(r.Accepted()).To(BeTrue())
		rest, _ := io.ReadAll(&buf)
		Expect(string(rest)).To(Equal("yamux bytes"))
	})

	It("refuses a frame that declares more than the bound before it reads the payload", func() {
		frame := binary.AppendUvarint(nil, tunnel.HelloMaxBytes+1)
		_, err := tunnel.ReadHello(bytes.NewReader(frame))
		Expect(errors.Is(err, tunnel.ErrHelloTooLarge)).To(BeTrue())
	})

	It("refuses to write a hello above the bound", func() {
		h := tunnel.Hello{V: tunnel.HelloVersion, NodeID: strings.Repeat("x", tunnel.HelloMaxBytes), Lane: tunnel.LaneInference}
		Expect(errors.Is(tunnel.WriteHello(io.Discard, h), tunnel.ErrHelloTooLarge)).To(BeTrue())
	})

	It("refuses another version and an unknown lane as a protocol error", func() {
		for _, h := range []tunnel.Hello{
			{V: 2, NodeID: "n", Lane: tunnel.LaneInference},
			{V: tunnel.HelloVersion, NodeID: "n", Lane: "other"},
		} {
			var buf bytes.Buffer
			Expect(tunnel.WriteHello(&buf, h)).To(Succeed())
			_, err := tunnel.ReadHello(&buf)
			Expect(errors.Is(err, tunnel.ErrProtocol)).To(BeTrue(), "%+v", h)
		}
	})

	It("refuses a reply with no status", func() {
		var buf bytes.Buffer
		Expect(tunnel.WriteHelloReply(&buf, tunnel.HelloReply{})).To(Succeed())
		_, err := tunnel.ReadHelloReply(&buf)
		Expect(errors.Is(err, tunnel.ErrProtocol)).To(BeTrue())
	})

	It("reports a stream that ends before the frame as EOF", func() {
		_, err := tunnel.ReadHello(bytes.NewReader(nil))
		Expect(err).To(MatchError(io.EOF))
	})
})

var _ = Describe("A session on a plain connection", func() {
	It("carries a stream from the frontend side to the worker side", func() {
		a, b := net.Pipe()
		server, err := tunnel.ServerSessionConn(a, tunnel.LaneInference)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = server.Close() }()
		client, err := tunnel.ClientSessionConn(b, tunnel.LaneInference)
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = client.Close() }()

		accepted := make(chan string, 1)
		go func() {
			s, err := client.AcceptStream()
			if err != nil {
				accepted <- err.Error()
				return
			}
			buf := make([]byte, 5)
			_, _ = io.ReadFull(s, buf)
			accepted <- string(buf)
		}()
		s, err := server.OpenStream(context.Background())
		Expect(err).ToNot(HaveOccurred())
		_, err = s.Write([]byte("hello"))
		Expect(err).ToNot(HaveOccurred())
		Eventually(accepted).Should(Receive(Equal("hello")))
	})
})
