package relayproto_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/rand/v2"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

func TestRelayproto(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "relayproto suite")
}

func frame(body []byte) []byte {
	return append(binary.AppendUvarint(nil, uint64(len(body))), body...)
}

func newID() peer.ID {
	_, pub, err := crypto.GenerateEd25519Key(nil)
	Expect(err).ToNot(HaveOccurred())
	id, err := peer.IDFromPublicKey(pub)
	Expect(err).ToNot(HaveOccurred())
	return id
}

var _ = Describe("framing", func() {
	It("round-trips a request and leaves the bytes after it unread", func() {
		var buf bytes.Buffer
		req := relayproto.Request{V: 1, Op: relayproto.OpPass, Seq: 42,
			Peers: []relayproto.Peer{{ID: "a", Label: "phone"}},
			Pass:  &relayproto.Pass{Code: "code-0123456789", TTLSeconds: 600, Label: "laptop"}}
		Expect(relayproto.Write(&buf, req)).To(Succeed())
		buf.WriteString("tail")
		var got relayproto.Request
		Expect(relayproto.Read(&buf, &got)).To(Succeed())
		Expect(got).To(Equal(req))
		rest, _ := io.ReadAll(&buf)
		Expect(string(rest)).To(Equal("tail"))
	})

	It("sends only the limits of a patch that it names, and zero is a value", func() {
		zero := int64(0)
		var buf bytes.Buffer
		Expect(relayproto.Write(&buf, relayproto.Request{V: 1, Op: relayproto.OpLimits,
			Limits: &relayproto.LimitsPatch{CircuitMaxBytes: &zero}})).To(Succeed())
		var raw map[string]any
		body := buf.Bytes()
		_, n := binary.Uvarint(body)
		Expect(json.Unmarshal(body[n:], &raw)).To(Succeed())
		Expect(raw["limits"]).To(Equal(map[string]any{"circuit_max_bytes": float64(0)}))
	})

	It("ignores unknown fields", func() {
		var got relayproto.Response
		Expect(relayproto.Read(bytes.NewReader(frame([]byte(`{"v":1,"ok":true,"count":3,"future":"x"}`))), &got)).To(Succeed())
		Expect(got).To(Equal(relayproto.Response{V: 1, OK: true, Count: 3}))
	})

	DescribeTable("rejects bad input",
		func(in []byte, want string) {
			var v relayproto.Request
			Expect(relayproto.Read(bytes.NewReader(in), &v)).To(MatchError(ContainSubstring(want)))
		},
		Entry("empty", []byte{}, "read length"),
		Entry("truncated varint", []byte{0x80}, "read length"),
		Entry("over 1 MiB", binary.AppendUvarint(nil, relayproto.MaxMessageSize+1), "too large"),
		Entry("short body", append(binary.AppendUvarint(nil, 10), '{'), "read body"),
		Entry("not JSON", frame([]byte("nope")), "decode"),
		Entry("wrong type", frame([]byte(`{"v":"one"}`)), "decode"),
	)

	It("accepts exactly 1 MiB and refuses to write more", func() {
		body := append(append([]byte(`{"code":"`), bytes.Repeat([]byte("x"), relayproto.MaxMessageSize-11)...), '"', '}')
		Expect(body).To(HaveLen(relayproto.MaxMessageSize))
		var v relayproto.Request
		Expect(relayproto.Read(bytes.NewReader(frame(body)), &v)).To(Succeed())
		Expect(v.Code).To(HaveLen(relayproto.MaxMessageSize - 11))
		v.Code += "y"
		Expect(relayproto.Write(io.Discard, v)).To(MatchError(relayproto.ErrTooLarge))
	})

	It("never panics on random input", func() {
		rng := rand.New(rand.NewPCG(1, 2))
		for range 20000 {
			b := make([]byte, rng.IntN(64))
			for i := range b {
				b[i] = byte(rng.UintN(256))
			}
			if rng.IntN(2) == 0 && len(b) > 0 {
				b = frame(b)
			}
			var v relayproto.Request
			_ = relayproto.Read(bytes.NewReader(b), &v)
		}
	})
})

var _ = Describe("Digest", func() {
	It("does not depend on order and tells sets apart", func() {
		a, b, c := newID(), newID(), newID()
		Expect(relayproto.Digest([]peer.ID{a, b})).To(Equal(relayproto.Digest([]peer.ID{b, a})))
		Expect(relayproto.Digest([]peer.ID{a, b})).ToNot(Equal(relayproto.Digest([]peer.ID{a, c})))
		Expect(relayproto.Digest(nil)).To(HaveLen(64))
	})
})

var _ = Describe("limits ceiling", func() {
	It("round-trips the ceiling of a limits response, zero meaning no ceiling", func() {
		var buf bytes.Buffer
		resp := relayproto.Response{V: 1, OK: true, Limits: &relayproto.Limits{
			CircuitMaxBytes: 1, Ceiling: relayproto.LimitsCeiling{CircuitMaxBytes: 8, CircuitMaxSeconds: 9},
		}}
		Expect(relayproto.Write(&buf, resp)).To(Succeed())
		var got relayproto.Response
		Expect(relayproto.Read(&buf, &got)).To(Succeed())
		Expect(got).To(Equal(resp))
		Expect(got.Limits.Ceiling.RateBPS).To(BeZero())
	})
})
