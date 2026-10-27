// SPDX-License-Identifier: MIT
package grpc

import (
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// validateMultimodalRerankSize applies the same strict transport ceiling as the
// embedding RPC to the whole serialized rerank request.
func validateMultimodalRerankSize(in *pb.MultimodalRerankRequest) error {
	if in == nil {
		return status.Error(codes.InvalidArgument, "missing multimodal rerank request")
	}
	if proto.Size(in) >= maxGRPCMessageSize {
		return status.Error(codes.ResourceExhausted, "multimodal rerank request too large")
	}
	return nil
}

// Check the serialized envelope, not just its payload. The ceiling is strict:
// a message exactly at the transport maximum is not dispatched.
func validateMultimodalEmbeddingSize(in *pb.MultimodalEmbeddingRequest) error {
	if in == nil {
		return status.Error(codes.InvalidArgument, "missing multimodal embedding request")
	}
	if proto.Size(in) >= maxGRPCMessageSize {
		return status.Error(codes.ResourceExhausted, "multimodal embedding request too large")
	}
	return nil
}
