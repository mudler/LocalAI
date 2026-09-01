package cluster_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/cluster"
)

var _ = Describe("Worker tunnel stream framing", func() {
	Describe("the request frame", func() {
		DescribeTable("round-trips a tag and a target",
			func(tag, target string) {
				var buf bytes.Buffer
				Expect(cluster.WriteStreamRequest(&buf, tag, target)).To(Succeed())
				gotTag, gotTarget, err := cluster.ReadStreamRequest(&buf)
				Expect(err).ToNot(HaveOccurred())
				Expect(gotTag).To(Equal(tag))
				Expect(gotTarget).To(Equal(target))
			},
			Entry("a tag and an address", cluster.StreamTagGRPC, "127.0.0.1:50051"),
			Entry("a tag with no target", cluster.StreamTagHTTP, ""),
			// The split is on the FIRST separator, so a target containing one
			// must survive intact.
			Entry("a target containing a space", cluster.StreamTagGRPC, "a b c"),
		)

		It("consumes exactly the frame and not one byte of what follows", func() {
			// Load-bearing: the stream is handed to gRPC or net/http right
			// after this, and a reader that over-read would eat the start of
			// their conversation.
			var buf bytes.Buffer
			Expect(cluster.WriteStreamRequest(&buf, cluster.StreamTagGRPC, "127.0.0.1:1")).To(Succeed())
			buf.WriteString("PRI * HTTP/2.0")

			_, _, err := cluster.ReadStreamRequest(&buf)
			Expect(err).ToNot(HaveOccurred())
			rest, err := io.ReadAll(&buf)
			Expect(err).ToNot(HaveOccurred())
			Expect(string(rest)).To(Equal("PRI * HTTP/2.0"))
		})

		DescribeTable("refuses a tag it could not encode unambiguously",
			func(tag string) {
				var buf bytes.Buffer
				Expect(cluster.WriteStreamRequest(&buf, tag, "x")).ToNot(Succeed())
				Expect(buf.Len()).To(BeZero(), "a refused request must not put a partial frame on the wire")
			},
			Entry("empty", ""),
			// A tag with a space would silently move part of itself into the
			// target, so it is refused at the writer rather than a round trip
			// later.
			Entry("containing a space", "grpc stream"),
		)

		It("refuses an over-long declared length after reading only the header", func() {
			// The name used to say "without allocating it" and the spec
			// measured nothing of the sort. What is actually checkable, and is
			// the mechanism the defence rests on, is that the reader STOPS: it
			// consumes the two length bytes and not one byte of the body, so a
			// peer cannot make it allocate or read on demand.
			//
			// The body is present in the input on purpose. With an input that
			// ends after the header, a reader that went on to read the body
			// would still consume nothing more, and this assertion would pass
			// with the limit check deleted.
			var hdr [2]byte
			binary.BigEndian.PutUint16(hdr[:], 65535)
			src := &countingReader{r: bytes.NewReader(append(hdr[:], make([]byte, 4096)...))}

			_, _, err := cluster.ReadStreamRequest(src)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("over the"))
			Expect(src.n).To(Equal(2), "the reader consumed part of a frame it had already refused")
		})

		It("reports a truncated frame as a truncated read, not as a refusal", func() {
			// ReadStreamRequest must never produce ErrStreamRequestInvalid:
			// that sentinel is what a worker SENDS, and a reader producing it
			// would leave a caller unable to tell "the peer refused me" from
			// "I could not read the peer".
			var hdr [2]byte
			binary.BigEndian.PutUint16(hdr[:], 10)
			_, _, err := cluster.ReadStreamRequest(bytes.NewReader(append(hdr[:], 'a')))
			Expect(err).To(MatchError(io.ErrUnexpectedEOF))
			Expect(err).ToNot(MatchError(cluster.ErrStreamRequestInvalid))
		})
	})

	Describe("the reply frame", func() {
		It("reads an acceptance as nil", func() {
			var buf bytes.Buffer
			Expect(cluster.WriteStreamAccepted(&buf)).To(Succeed())
			Expect(cluster.ReadStreamReply(&buf)).To(Succeed())
		})

		DescribeTable("keeps the three refusals apart",
			func(sent error, others []error) {
				var buf bytes.Buffer
				Expect(cluster.WriteStreamRefusal(&buf, sent)).To(Succeed())
				got := cluster.ReadStreamReply(&buf)
				Expect(got).To(MatchError(sent))
				// The whole point. A caller gives up on an unknown tag, retries
				// an unavailable target, and reports a bad request as its own
				// bug; collapsing any pair makes one of those wrong.
				for _, other := range others {
					Expect(got).ToNot(MatchError(other))
				}
			},
			Entry("unknown tag", cluster.ErrStreamTagUnknown,
				[]error{cluster.ErrStreamTargetUnavailable, cluster.ErrStreamRequestInvalid}),
			Entry("unavailable target", cluster.ErrStreamTargetUnavailable,
				[]error{cluster.ErrStreamTagUnknown, cluster.ErrStreamRequestInvalid}),
			Entry("invalid request", cluster.ErrStreamRequestInvalid,
				[]error{cluster.ErrStreamTagUnknown, cluster.ErrStreamTargetUnavailable}),
		)

		It("carries the reason text to the far side", func() {
			var buf bytes.Buffer
			Expect(cluster.WriteStreamRefusal(&buf, wrapReason(cluster.ErrStreamTagUnknown, "no-such-tag"))).To(Succeed())
			Expect(cluster.ReadStreamReply(&buf).Error()).To(ContainSubstring("no-such-tag"))
		})

		It("reports an unrecognised code as itself, not as the nearest known one", func() {
			// A code from a newer worker. Mapping it onto a known sentinel
			// would make a frontend retry forever against a refusal that means
			// something else entirely.
			var buf bytes.Buffer
			writeRawFrame(&buf, "err teapot short and stout")
			got := cluster.ReadStreamReply(&buf)
			Expect(got).To(HaveOccurred())
			Expect(got.Error()).To(ContainSubstring("teapot"))
			Expect(got).ToNot(MatchError(cluster.ErrStreamTagUnknown))
			Expect(got).ToNot(MatchError(cluster.ErrStreamTargetUnavailable))
			Expect(got).ToNot(MatchError(cluster.ErrStreamRequestInvalid))
		})

		It("reports a failure to READ the reply as itself, never as a refusal", func() {
			// A refusal proves the worker is connected and said no. A read
			// failure means the tunnel broke. A caller that treated the second
			// as the first would report a dead link as a policy decision.
			got := cluster.ReadStreamReply(bytes.NewReader(nil))
			Expect(got).To(MatchError(io.EOF))
			Expect(got).ToNot(MatchError(cluster.ErrStreamTagUnknown))
			Expect(got).ToNot(MatchError(cluster.ErrStreamTargetUnavailable))
			Expect(got).ToNot(MatchError(cluster.ErrStreamRequestInvalid))
		})

		It("truncates an over-long reason on a rune boundary, keeping it decodable", func() {
			// Two-byte runes so a byte-boundary cut lands mid-rune for half of
			// all lengths; the padding tunes the frame to land exactly there.
			reason := wrapReason(cluster.ErrStreamTargetUnavailable, strings.Repeat("é", 2000))
			var buf bytes.Buffer
			Expect(cluster.WriteStreamRefusal(&buf, reason)).To(Succeed())

			got := cluster.ReadStreamReply(&buf)
			Expect(got).To(MatchError(cluster.ErrStreamTargetUnavailable))
			Expect(utf8.ValidString(got.Error())).To(BeTrue(),
				"the truncated reason reached the far side with a split rune in it")
		})

		It("still reports the code when the reason is truncated away", func() {
			// The code must survive truncation: a refusal a frontend cannot
			// classify is indistinguishable from a worker that hung up.
			reason := wrapReason(cluster.ErrStreamTagUnknown, strings.Repeat("x", 4000))
			var buf bytes.Buffer
			Expect(cluster.WriteStreamRefusal(&buf, reason)).To(Succeed())
			Expect(cluster.ReadStreamReply(&buf)).To(MatchError(cluster.ErrStreamTagUnknown))
		})
	})
})

// wrapReason builds the shape the worker sends: a sentinel with a cause.
func wrapReason(sentinel error, text string) error {
	return &reasonErr{sentinel: sentinel, text: text}
}

type reasonErr struct {
	sentinel error
	text     string
}

func (e *reasonErr) Error() string { return e.sentinel.Error() + ": " + e.text }
func (e *reasonErr) Unwrap() error { return e.sentinel }

// countingReader records how many bytes were actually consumed, so a spec can
// assert where a reader stopped rather than only what it returned.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// writeRawFrame puts a payload on the wire without going through the encoder,
// so a spec can present a frame the encoder would never produce.
func writeRawFrame(buf *bytes.Buffer, payload string) {
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(payload)))
	buf.Write(hdr[:])
	buf.WriteString(payload)
}
