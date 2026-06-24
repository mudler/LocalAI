package main

// Vector⇄key encoding: the "vector IS the key" resolution.
//
// local-store keys entries *by* the vector itself (a []float32). Valkey hashes
// are keyed by strings, so we synthesise a deterministic, lossless key:
//
//	key = prefix + hex(little-endian float32 bytes of the vector)
//
// The same vector always produces the same bytes, so HSET is an upsert and
// HGET/DEL are exact matches — and the encoding is reversible, so we can hand
// the original []float32 back on Get/Find.
//
// Divergence from local-store (documented and tested): local-store compares
// keys with slices.Compare, which treats -0.0 == +0.0 and orders NaN, so those
// collapse to the same logical key. Byte-encoding makes -0.0 and +0.0 (and any
// distinct NaN bit-pattern) *distinct* keys. We accept this on purpose: a
// lossless, deterministic, exact round-trip is more valuable for a persistent
// store than reproducing local-store's float-equality quirk, and callers never
// rely on -0.0/+0.0 aliasing.

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

// _float32Bytes is the wire width of a single FLOAT32 component.
const _float32Bytes = 4

// vecToBytes encodes a vector as little-endian float32 bytes. This is byte-for
// -byte identical to valkey.VectorString32, so the value we store in the hash
// `vec` field and the bytes we hash into the key share one encoding.
func vecToBytes(v []float32) []byte {
	b := make([]byte, len(v)*_float32Bytes)
	for i, e := range v {
		off := i * _float32Bytes
		binary.LittleEndian.PutUint32(b[off:off+_float32Bytes], math.Float32bits(e))
	}
	return b
}

// bytesToVec reverses vecToBytes. It rejects a payload whose length is not a
// multiple of the float32 width, which would indicate a corrupted/foreign value.
func bytesToVec(b []byte) ([]float32, error) {
	if len(b)%_float32Bytes != 0 {
		return nil, fmt.Errorf("valkey-store: vector byte length %d is not a multiple of %d", len(b), _float32Bytes)
	}
	v := make([]float32, len(b)/_float32Bytes)
	for i := range v {
		off := i * _float32Bytes
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[off : off+_float32Bytes]))
	}
	return v, nil
}

// encodeKey builds the Valkey hash key for a vector: prefix + hex(bytes).
// Hex keeps the key printable (so it is safe in FT.CREATE PREFIX and in logs)
// while staying lossless.
func encodeKey(prefix string, v []float32) string {
	return prefix + hex.EncodeToString(vecToBytes(v))
}

// decodeKey reverses encodeKey. It is intentionally retained as the tested,
// symmetric inverse of encodeKey — it is NOT on the hot Find path (StoresFind
// decodes the returned `vec` bytes via bytesToVec directly), but keeping the
// key↔vector mapping provably invertible guards the encoding contract and is
// exercised by the round-trip unit tests.
func decodeKey(prefix, key string) ([]float32, error) {
	if !strings.HasPrefix(key, prefix) {
		return nil, fmt.Errorf("valkey-store: key %q does not have expected prefix %q", key, prefix)
	}
	b, err := hex.DecodeString(strings.TrimPrefix(key, prefix))
	if err != nil {
		return nil, fmt.Errorf("valkey-store: decode key hex: %w", err)
	}
	return bytesToVec(b)
}
