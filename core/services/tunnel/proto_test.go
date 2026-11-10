package tunnel_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mudler/LocalAI/core/services/tunnel"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Stream protocol", func() {
	It("carries a request frame without reading a byte of what follows", func() {
		var buf bytes.Buffer
		Expect(tunnel.WriteStreamRequest(&buf, tunnel.StreamTagGRPC, "127.0.0.1:50052")).To(Succeed())
		buf.WriteString("PRI * HTTP/2.0")

		tag, target, err := tunnel.ReadStreamRequest(&buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(tag).To(Equal("grpc"))
		Expect(target).To(Equal("127.0.0.1:50052"))
		Expect(buf.String()).To(Equal("PRI * HTTP/2.0"))
	})

	It("accepts a tag with no target", func() {
		var buf bytes.Buffer
		Expect(tunnel.WriteStreamRequest(&buf, tunnel.StreamTagHTTP, "")).To(Succeed())
		tag, target, err := tunnel.ReadStreamRequest(&buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(tag).To(Equal("http"))
		Expect(target).To(BeEmpty())
	})

	It("refuses to write an empty tag or a tag with a space", func() {
		Expect(tunnel.WriteStreamRequest(io.Discard, "", "x")).ToNot(Succeed())
		Expect(tunnel.WriteStreamRequest(io.Discard, "gr pc", "x")).ToNot(Succeed())
	})

	It("does not return a malformed request as a refusal sentinel", func() {
		_, _, err := tunnel.ReadStreamRequest(bytes.NewReader([]byte{0, 0}))
		Expect(err).To(HaveOccurred())
		Expect(tunnel.IsStreamRefusal(err)).To(BeFalse())
	})

	It("refuses a frame that declares more than the limit before it reads the payload", func() {
		r := bytes.NewReader([]byte{0xff, 0xff, 'x'})
		_, _, err := tunnel.ReadStreamRequest(r)
		Expect(err).To(MatchError(ContainSubstring("over the")))
	})

	It("reads a frame that is cut short as an unexpected end", func() {
		_, _, err := tunnel.ReadStreamRequest(bytes.NewReader([]byte{0, 9, 'a'}))
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
	})

	It("reads an accepted reply as success", func() {
		var buf bytes.Buffer
		Expect(tunnel.WriteStreamAccepted(&buf)).To(Succeed())
		Expect(tunnel.ReadStreamReply(&buf)).To(Succeed())
	})

	DescribeTable("round-trips every refusal to its sentinel",
		func(sentinel error, evidence bool) {
			var buf bytes.Buffer
			Expect(tunnel.WriteStreamRefusal(&buf, fmt.Errorf("%w: because", sentinel))).To(Succeed())
			err := tunnel.ReadStreamReply(&buf)
			Expect(err).To(MatchError(sentinel))
			Expect(tunnel.IsStreamRefusal(err)).To(BeTrue())
			Expect(tunnel.IsWorkerAnswer(err)).To(Equal(evidence))
		},
		Entry("unknown tag", tunnel.ErrStreamTagUnknown, true),
		Entry("target unavailable", tunnel.ErrStreamTargetUnavailable, true),
		Entry("request invalid", tunnel.ErrStreamRequestInvalid, true),
		Entry("not served", tunnel.ErrStreamNotServed, false),
	)

	It("sends a reason with no classification as not served, never as evidence", func() {
		var buf bytes.Buffer
		Expect(tunnel.WriteStreamRefusal(&buf, errors.New("something unclassified"))).To(Succeed())
		err := tunnel.ReadStreamReply(&buf)
		Expect(err).To(MatchError(tunnel.ErrStreamNotServed))
		Expect(tunnel.IsWorkerAnswer(err)).To(BeFalse())
	})

	It("keeps the first sentinel of the table when a reason wraps two", func() {
		both := errors.Join(tunnel.ErrStreamNotServed, tunnel.ErrStreamTagUnknown)
		var buf bytes.Buffer
		Expect(tunnel.WriteStreamRefusal(&buf, both)).To(Succeed())
		Expect(tunnel.ReadStreamReply(&buf)).To(MatchError(tunnel.ErrStreamTagUnknown))
	})

	It("reads a code it does not know as an error that is no evidence", func() {
		var buf bytes.Buffer
		Expect(tunnel.WriteStreamRefusal(&buf, errors.New("x"))).To(Succeed())
		raw := strings.Replace(buf.String(), "not-served", "from-the-future", 1)
		// The length prefix covers the old text, so build the frame again.
		var framed bytes.Buffer
		framed.Write([]byte{0, byte(len(raw) - 2)})
		framed.WriteString(raw[2:])

		err := tunnel.ReadStreamReply(&framed)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("from-the-future"))
		Expect(tunnel.IsStreamRefusal(err)).To(BeFalse())
		Expect(tunnel.IsWorkerAnswer(err)).To(BeFalse())
	})

	It("does not report a failed read of the reply as a refusal", func() {
		err := tunnel.ReadStreamReply(bytes.NewReader(nil))
		Expect(err).To(HaveOccurred())
		Expect(tunnel.IsStreamRefusal(err)).To(BeFalse())
		Expect(tunnel.IsWorkerAnswer(err)).To(BeFalse())
	})

	It("cuts a long reason on a rune boundary and puts it on one line", func() {
		reason := errors.New(strings.Repeat("é\n", 2000))
		var buf bytes.Buffer
		Expect(tunnel.WriteStreamRefusal(&buf, reason)).To(Succeed())
		Expect(buf.Len()).To(BeNumerically("<=", 2+1024))
		err := tunnel.ReadStreamReply(&buf)
		Expect(err).To(MatchError(tunnel.ErrStreamNotServed))
		Expect(err.Error()).ToNot(ContainSubstring("\n"))
		Expect(err.Error()).To(MatchRegexp(`^[^\x{FFFD}]*$`))
	})

	It("does not treat a plain error or nil as an answer of a worker", func() {
		Expect(tunnel.IsWorkerAnswer(nil)).To(BeFalse())
		Expect(tunnel.IsWorkerAnswer(errors.New("dial"))).To(BeFalse())
	})
})
