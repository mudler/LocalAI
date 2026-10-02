// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc"
)

func (e *embedBackend) MotionStream(ctx context.Context, opts ...grpc.CallOption) (MotionStreamClient, error) {
	return newChannelStream(ctx, 4, func(stream *channelStreamServer[*pb.MotionRequest, *pb.MotionResponse]) error {
		return e.s.MotionStream(stream)
	}), nil
}
