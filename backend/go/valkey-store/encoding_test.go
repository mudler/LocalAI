package main

// Unit tests for the vector⇄key encoding. These need no Valkey server: they
// exercise the pure lossless-encoding contract that the whole store relies on,
// including the documented edge cases (-0.0/+0.0 and NaN) where this encoding
// intentionally diverges from local-store's slices.Compare float equality.

import (
	"math"
	"math/rand/v2"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	valkey "github.com/valkey-io/valkey-go"
)

var _ = Describe("vector⇄bytes encoding", func() {
	It("round-trips vectors of varying dimensions", func() {
		r := rand.New(rand.NewPCG(1, 2))
		for _, dim := range []int{1, 3, 4, 16, 128, 768} {
			v := make([]float32, dim)
			for i := range v {
				v[i] = float32(r.NormFloat64())
			}
			got, err := bytesToVec(vecToBytes(v))
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(v))
		}
	})

	It("matches valkey.VectorString32 byte-for-byte", func() {
		// The stored `vec` field uses valkey.VectorString32; the key uses
		// vecToBytes. They must be the same encoding or Get/Find break.
		v := []float32{0.1, -0.2, 3.5, 0}
		Expect(valkey.BinaryString(vecToBytes(v))).To(Equal(valkey.VectorString32(v)))
	})

	It("rejects a byte payload that is not a multiple of 4", func() {
		_, err := bytesToVec([]byte{1, 2, 3})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("key encoding", func() {
	const prefix = "vs:test:"

	It("round-trips key encode/decode", func() {
		v := []float32{0.5, 0.5, 0.5}
		key := encodeKey(prefix, v)
		Expect(key).To(HavePrefix(prefix))
		got, err := decodeKey(prefix, key)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(v))
	})

	It("produces distinct keys for -0.0 and +0.0 (documented divergence)", func() {
		negZero := float32(math.Copysign(0, -1))
		posZero := float32(0)
		Expect(math.Signbit(float64(negZero))).To(BeTrue())
		Expect(encodeKey(prefix, []float32{negZero})).NotTo(Equal(encodeKey(prefix, []float32{posZero})))
	})

	It("produces a stable, distinct key for a NaN component", func() {
		nan := float32(math.NaN())
		k1 := encodeKey(prefix, []float32{nan})
		k2 := encodeKey(prefix, []float32{nan})
		// Deterministic: same NaN bit-pattern → same key.
		Expect(k1).To(Equal(k2))
		// Distinct from a normal value.
		Expect(k1).NotTo(Equal(encodeKey(prefix, []float32{0})))
	})

	It("rejects a key without the expected prefix", func() {
		_, err := decodeKey(prefix, "wrong:deadbeef")
		Expect(err).To(HaveOccurred())
	})
})
