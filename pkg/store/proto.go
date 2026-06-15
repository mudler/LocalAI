package store

// pb⇄[][]float32/[][]byte translation helpers shared by the gRPC
// client (this file's package) and the local-store gRPC server in
// backend/go/local-store. Same shape on both sides of the wire so a
// schema bug only needs fixing once.

import (
	"github.com/mudler/LocalAI/pkg/grpc/proto"
)

// WrapKeys wraps each plain []float32 in a *proto.StoresKey.
func WrapKeys(in [][]float32) []*proto.StoresKey {
	out := make([]*proto.StoresKey, len(in))
	for i, k := range in {
		out[i] = &proto.StoresKey{Floats: k}
	}
	return out
}

// WrapValues wraps each []byte in a *proto.StoresValue.
func WrapValues(in [][]byte) []*proto.StoresValue {
	out := make([]*proto.StoresValue, len(in))
	for i, v := range in {
		out[i] = &proto.StoresValue{Bytes: v}
	}
	return out
}

// UnwrapKeys extracts the inner Floats from a slice of *proto.StoresKey.
func UnwrapKeys(in []*proto.StoresKey) [][]float32 {
	out := make([][]float32, len(in))
	for i, k := range in {
		out[i] = k.Floats
	}
	return out
}

// UnwrapValues extracts the inner Bytes from a slice of *proto.StoresValue.
func UnwrapValues(in []*proto.StoresValue) [][]byte {
	out := make([][]byte, len(in))
	for i, v := range in {
		out[i] = v.Bytes
	}
	return out
}
